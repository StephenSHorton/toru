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

// prunePersistent enforces the age and size limits on a persistent recording
// dir: oldest unprotected segments go first; the newest segment (still being
// written) and protected runs are never touched. Sidecars of runs with no
// segments left are removed too.
func (b *ReplayBuffer) prunePersistent(cfg ReplayConfig) {
	b.mu.Lock()
	protect, current := b.protect, b.run
	b.mu.Unlock()
	b.segMu.Lock()
	defer b.segMu.Unlock()

	segs := ListRecSegments(cfg.Dir)
	var total int64
	for _, s := range segs {
		total += s.Size
	}
	cutoff := time.Time{}
	if cfg.KeepSeconds > 0 {
		cutoff = time.Now().Add(-time.Duration(cfg.KeepSeconds) * time.Second)
	}
	live := map[int]bool{current: true}
	for i, s := range segs {
		if i == len(segs)-1 || (protect != nil && protect(s.Run)) {
			live[s.Run] = true
			continue
		}
		tooOld := !cutoff.IsZero() && s.ModTime.Before(cutoff)
		overCap := cfg.MaxBytes > 0 && total > cfg.MaxBytes
		if !tooOld && !overCap {
			live[s.Run] = true
			continue
		}
		if os.Remove(s.Path) == nil {
			total -= s.Size
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

	b.mu.Lock()
	b.diskUsed = total
	b.mu.Unlock()
	full := cfg.MaxBytes > 0 && total > cfg.MaxBytes
	b.setStatus(func(s *ReplayStatus) {
		const msg = "Recording storage is full of kept recordings; delete some or raise the limit"
		switch {
		case full:
			s.Error = msg
		case s.Error == msg:
			s.Error = ""
		}
	})
}
