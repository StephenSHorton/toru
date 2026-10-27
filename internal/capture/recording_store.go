package capture

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Session recording on disk (ReplayConfig.Persist). One ffmpeg run writes:
//
//	r<run>-<index>.ts  2-second segments
//	r<run>.csv         ffmpeg's segment list: "<file>,<start>,<end>" (seconds)
//	r<run>.json        RunInfo: wall-clock start and the caller's tag
//
// <run> is the run's start time in Unix seconds, so names sort
// chronologically across restarts.

// RunInfo is one run's sidecar.
type RunInfo struct {
	Run     int    `json:"run"`
	StartMs int64  `json:"startMs"` // wall clock when ffmpeg was started
	Tag     string `json:"tag"`
}

// RecSegment is one segment file of a persistent recording.
type RecSegment struct {
	Name    string
	Path    string
	Run     int
	Index   int
	Size    int64
	ModTime time.Time // when the segment was finished
}

var recSidecarName = regexp.MustCompile(`^r(\d{4,})\.(json|csv)$`)

func writeRunInfo(dir string, info RunInfo) {
	raw, err := json.Marshal(info)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("r%04d.json", info.Run)), raw, 0o644)
}

// ReadRunInfo reads run's sidecar.
func ReadRunInfo(dir string, run int) (RunInfo, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("r%04d.json", run)))
	if err != nil {
		return RunInfo{}, false
	}
	var info RunInfo
	if json.Unmarshal(raw, &info) != nil {
		return RunInfo{}, false
	}
	return info, true
}

// ListRecSegments returns dir's segments oldest-first with size and time.
func ListRecSegments(dir string) []RecSegment {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]RecSegment, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := replaySegmentName.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		run, _ := strconv.Atoi(m[1])
		idx, _ := strconv.Atoi(strings.TrimSuffix(strings.SplitN(e.Name(), "-", 2)[1], filepath.Ext(e.Name())))
		out = append(out, RecSegment{
			Name: e.Name(), Path: filepath.Join(dir, e.Name()),
			Run: run, Index: idx, Size: fi.Size(), ModTime: fi.ModTime(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SegmentTimes reads run's segment list: file name -> [start, end] seconds
// from the run's start. Segments still being written aren't listed yet.
func SegmentTimes(dir string, run int) map[string][2]float64 {
	f, err := os.Open(filepath.Join(dir, fmt.Sprintf("r%04d.csv", run)))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	out := map[string][2]float64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Split(strings.TrimSpace(sc.Text()), ",")
		if len(parts) != 3 {
			continue
		}
		start, err1 := strconv.ParseFloat(parts[1], 64)
		end, err2 := strconv.ParseFloat(parts[2], 64)
		if err1 == nil && err2 == nil && end > start {
			out[filepath.Base(parts[0])] = [2]float64{start, end}
		}
	}
	return out
}

// StitchSegments joins consecutive segments (any runs) into an MP4 at
// outPath, stream-copied, with the given audio track titles.
func StitchSegments(segs []RecSegment, outPath string, trackTitles []string) error {
	bin, err := LocateFFmpeg()
	if err != nil {
		return err
	}
	rs := make([]replaySegment, len(segs))
	for i, s := range segs {
		rs[i] = replaySegment{name: s.Name, path: s.Path, run: s.Run}
	}
	return stitchReplay(bin, rs, outPath, trackTitles)
}

// DeleteRun removes every file of run (segments and sidecars).
func DeleteRun(dir string, run int) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	prefix := fmt.Sprintf("r%04d", run)
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && (strings.HasPrefix(n, prefix+"-") || strings.HasPrefix(n, prefix+".")) {
			_ = os.Remove(filepath.Join(dir, n))
		}
	}
	return nil
}

// SetMaxBytes changes a running persistent recorder's size cap in place
// (no restart); the next prune applies it. Passing 0 falls back to the cap
// the recorder was started with (Start resets the override), so it cannot
// turn a configured cap off live; SetMaxFootageSec can.
func (b *ReplayBuffer) SetMaxBytes(n int64) {
	b.mu.Lock()
	b.maxBytes = n
	b.cfg.MaxBytes = n
	b.mu.Unlock()
}

// SetMaxFootageSec changes a running persistent recorder's footage budget in
// place (no restart); the next prune applies it. 0 turns the limit off.
func (b *ReplayBuffer) SetMaxFootageSec(n int) {
	b.mu.Lock()
	b.maxFootageSet = true
	b.cfg.MaxFootageSec = n
	b.mu.Unlock()
}

// prunePersistent enforces a persistent recording dir's three limits, in
// this order:
//
//  1. Footage (MaxFootageSec): finished footage is counted newest-first
//     across every unprotected run; once the budget is spent, that segment
//     and everything older go, trimming inside a session if needed.
//  2. Age (KeepSeconds; segment ModTime), when not WholeSessions.
//  3. Size (MaxBytes) and the free-space floor (MinFreeBytes): oldest
//     unprotected segments go first, or whole sessions when WholeSessions.
//
// The newest segment (still being written) and protected runs are never
// touched or counted. Sidecars of runs with no segments left are removed.
func (b *ReplayBuffer) prunePersistent(cfg ReplayConfig) {
	b.mu.Lock()
	protect, current := b.protect, b.run
	if b.maxBytes > 0 {
		cfg.MaxBytes = b.maxBytes
	}
	if b.maxFootageSet {
		cfg.MaxFootageSec = b.cfg.MaxFootageSec
	}
	b.mu.Unlock()
	b.segMu.Lock()
	defer b.segMu.Unlock()

	segs := ListRecSegments(cfg.Dir)
	var total int64
	for _, s := range segs {
		total += s.Size
	}
	live := map[int]bool{current: true}
	if cfg.MaxFootageSec > 0 {
		segs, total = pruneFootage(cfg, segs, total, protect, live)
	}
	// Free-space floor: if the drive has less than MinFreeBytes free, prune
	// the shortfall too (on top of the size cap).
	var shortfall int64
	if cfg.MinFreeBytes > 0 {
		if free, ok := diskFreeBytes(cfg.Dir); ok && free < cfg.MinFreeBytes {
			shortfall = cfg.MinFreeBytes - free
		}
	}
	cutoff := time.Time{}
	if cfg.KeepSeconds > 0 {
		cutoff = time.Now().Add(-time.Duration(cfg.KeepSeconds) * time.Second)
	}
	if cfg.WholeSessions {
		total, shortfall = b.pruneWholeSessions(cfg, segs, total, shortfall, protect, current, live)
		segs = nil // handled
	}
	for i, s := range segs {
		if i == len(segs)-1 || (protect != nil && protect(s.Run)) {
			live[s.Run] = true
			continue
		}
		tooOld := !cutoff.IsZero() && s.ModTime.Before(cutoff)
		overCap := cfg.MaxBytes > 0 && total > cfg.MaxBytes
		if !tooOld && !overCap && shortfall <= 0 {
			live[s.Run] = true
			continue
		}
		if os.Remove(s.Path) == nil {
			total -= s.Size
			shortfall -= s.Size
		} else {
			live[s.Run] = true
		}
	}

	if entries, err := os.ReadDir(cfg.Dir); err == nil {
		for _, e := range entries {
			if m := recSidecarName.FindStringSubmatch(e.Name()); m != nil {
				if run, _ := strconv.Atoi(m[1]); !live[run] {
					_ = os.Remove(filepath.Join(cfg.Dir, e.Name()))
				}
			}
		}
	}

	driveFull := shortfall > 0
	b.mu.Lock()
	b.diskUsed = total
	b.diskFull = driveFull
	b.mu.Unlock()
	full := cfg.MaxBytes > 0 && total > cfg.MaxBytes
	// Every prune publishes a status (setStatus always notifies), with the
	// bytes left on disk: that is how an open viewer learns that footage was
	// trimmed from a finished session, which otherwise changes silently.
	b.setStatus(func(s *ReplayStatus) {
		const capMsg = "Recording storage is full of hotkey recordings; delete some or raise the limit"
		const driveMsg = "Your drive is almost full, so recording is paused. Free up space or delete recordings"
		s.DiskBytes = total
		switch {
		case driveFull:
			s.Error = driveMsg
			s.Recording = false
		case full:
			s.Error = capMsg
		case s.Error == capMsg || s.Error == driveMsg:
			s.Error = ""
		}
	})
}

// pruneFootage keeps the newest cfg.MaxFootageSec seconds of finished
// footage across all unprotected runs and deletes the rest, regardless of
// session boundaries. The newest segment (in progress) and protected runs are
// neither counted nor deleted. A segment's duration comes from its run's
// segment list; one not listed yet counts as replaySegmentSec. It returns the
// surviving segments (oldest first) and the new total; only runs whose
// segment could not be removed are marked live.
func pruneFootage(cfg ReplayConfig, segs []RecSegment, total int64, protect func(int) bool, live map[int]bool) ([]RecSegment, int64) {
	if len(segs) < 2 {
		return segs, total
	}
	times := map[int]map[string][2]float64{}
	duration := func(s RecSegment) float64 {
		t, ok := times[s.Run]
		if !ok {
			t = SegmentTimes(cfg.Dir, s.Run)
			times[s.Run] = t
		}
		if span, ok := t[s.Name]; ok {
			return span[1] - span[0]
		}
		return replaySegmentSec
	}
	budget := float64(cfg.MaxFootageSec)
	var used float64
	drop := map[int]bool{} // index into segs
	over := false
	for i := len(segs) - 2; i >= 0; i-- {
		s := segs[i]
		if protect != nil && protect(s.Run) {
			continue
		}
		if !over {
			d := duration(s)
			if used+d <= budget {
				used += d
				continue
			}
			over = true
		}
		if os.Remove(s.Path) == nil {
			total -= s.Size
			drop[i] = true
		} else {
			live[s.Run] = true
		}
	}
	if len(drop) == 0 {
		return segs, total
	}
	kept := make([]RecSegment, 0, len(segs)-len(drop))
	for i, s := range segs {
		if !drop[i] {
			kept = append(kept, s)
		}
	}
	return kept, total
}

// SessionGap is the longest pause between two runs of the same tag that
// still counts as one session (ffmpeg restarts on audio-device changes etc.).
const SessionGap = 15 * time.Second

// GroupRuns groups segments into sessions: consecutive runs with the same
// tag and less than SessionGap between them. Groups are oldest first.
func GroupRuns(dir string, segs []RecSegment) [][]RecSegment {
	var groups [][]RecSegment
	var lastTag string
	var lastEnd time.Time
	lastRun := -1
	for _, s := range segs {
		if s.Run != lastRun {
			info, _ := ReadRunInfo(dir, s.Run)
			start := time.UnixMilli(info.StartMs)
			if info.StartMs == 0 {
				start = time.Unix(int64(s.Run), 0)
			}
			if len(groups) == 0 || info.Tag != lastTag || start.Sub(lastEnd) >= SessionGap {
				groups = append(groups, nil)
			}
			lastTag, lastRun = info.Tag, s.Run
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], s)
		lastEnd = s.ModTime
	}
	return groups
}

// pruneWholeSessions frees space a whole session at a time (oldest first,
// skipping protected sessions and the one being recorded). If that isn't
// enough, it trims the oldest footage of the live session, never its
// segment in progress. It returns the new total and remaining shortfall and
// marks runs that still have footage in live.
func (b *ReplayBuffer) pruneWholeSessions(cfg ReplayConfig, segs []RecSegment, total, shortfall int64,
	protect func(int) bool, current int, live map[int]bool) (int64, int64) {
	need := func() bool {
		return (cfg.MaxBytes > 0 && total > cfg.MaxBytes) || shortfall > 0
	}
	remove := func(s RecSegment) bool {
		if os.Remove(s.Path) != nil {
			return false
		}
		total -= s.Size
		shortfall -= s.Size
		return true
	}
	groups := GroupRuns(cfg.Dir, segs)
	var liveGroup []RecSegment
	for _, g := range groups {
		isLive, isProtected := false, false
		for _, s := range g {
			isLive = isLive || s.Run == current
			isProtected = isProtected || (protect != nil && protect(s.Run))
		}
		if isLive {
			liveGroup = g
		}
		if !need() || isLive || isProtected {
			for _, s := range g {
				live[s.Run] = true
			}
			continue
		}
		for _, s := range g {
			if !remove(s) {
				live[s.Run] = true
			}
		}
	}
	// The session being recorded is bigger than the cap on its own: trim its
	// oldest footage (rolling) rather than exceed the limit.
	for i := 0; need() && i < len(liveGroup)-1; i++ {
		remove(liveGroup[i])
	}
	return total, shortfall
}

func (b *ReplayBuffer) isDiskFull() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.diskFull
}

// diskFreeBytes is swappable so tests can simulate a nearly full drive.
var diskFreeBytes = diskFreeBytesOS
