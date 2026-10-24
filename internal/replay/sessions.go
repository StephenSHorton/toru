package replay

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Session is one recording on the Library's Recordings tab: consecutive runs
// of the same game/mode with no real gap, played back as one timeline.
type Session struct {
	ID          int             `json:"id"` // first run
	Runs        []int           `json:"runs"`
	Game        string          `json:"game"` // "" = desktop / no game
	Mode        string          `json:"mode"` // ModeBackground | ModeManual
	StartMs     int64           `json:"startMs"`
	EndMs       int64           `json:"endMs"`
	DurationSec float64         `json:"durationSec"`
	SizeBytes   int64           `json:"sizeBytes"`
	Kept        bool            `json:"kept"`       // exempt from auto-delete
	Live        bool            `json:"live"`       // still recording
	DeleteAtMs  int64           `json:"deleteAtMs"` // background, not kept: when its oldest footage expires
	ThumbURL    string          `json:"thumbUrl"`
	PlaylistURL string          `json:"playlistUrl"`
	Markers     []SessionMarker `json:"markers"`
}

// SessionMarker is a marker placed on a session's timeline.
type SessionMarker struct {
	Kind      string  `json:"kind"`
	Label     string  `json:"label"`
	T         int64   `json:"t"`
	OffsetSec float64 `json:"offsetSec"`
}

// sessionGap is the longest pause between runs that still counts as one
// session (the supervisor restarts ffmpeg on audio device changes etc.).
// Shared with the engine's whole-session pruning so both agree.
const sessionGap = capture.SessionGap

const recPrefix = "/__rec/"

// timedSeg is a finished segment placed on its session's timeline.
type timedSeg struct {
	seg       capture.RecSegment
	dur       float64
	offset    float64 // seconds from the session start
	wallStart int64   // Unix ms
	discontAt bool    // first segment of a run after another run
}

type builtSession struct {
	Session
	segs []timedSeg
}

// ListSessions returns the recordings newest first.
func (s *ReplayService) ListSessions() []Session {
	built := s.buildSessions()
	out := make([]Session, len(built))
	for i, b := range built {
		out[len(built)-1-i] = b.Session
	}
	return out
}

func (s *ReplayService) buildSessions() []builtSession {
	st := s.GetSettings()
	dir := recordingsDirFor(st)
	segs := capture.ListRecSegments(dir)
	if len(segs) == 0 {
		return nil
	}
	byRun := map[int][]capture.RecSegment{}
	var runs []int
	for _, seg := range segs {
		if _, ok := byRun[seg.Run]; !ok {
			runs = append(runs, seg.Run)
		}
		byRun[seg.Run] = append(byRun[seg.Run], seg)
	}
	slices.Sort(runs)

	s.mu.Lock()
	liveCfg := s.runningCfg
	s.mu.Unlock()
	liveRun := 0
	if liveCfg.Persist && s.buf.Running() && filepath.Clean(liveCfg.Dir) == filepath.Clean(dir) {
		liveRun = runs[len(runs)-1]
	}
	kept := loadKept(dir)
	markers := loadMarkers(dir)

	var out []builtSession
	for _, run := range runs {
		info, _ := capture.ReadRunInfo(dir, run)
		mode, game := decodeTag(info.Tag)
		if mode == "" {
			mode = ModeBackground
		}
		times := capture.SegmentTimes(dir, run)
		startMs := info.StartMs
		if startMs == 0 {
			startMs = int64(run) * 1000
		}

		var rsegs []timedSeg
		for _, seg := range byRun[run] {
			t, ok := times[seg.Name]
			if !ok {
				continue // still being written (or no timing): not playable yet
			}
			rsegs = append(rsegs, timedSeg{seg: seg, dur: t[1] - t[0], wallStart: startMs + int64(t[0]*1000)})
		}
		if len(rsegs) == 0 {
			continue
		}
		last := rsegs[len(rsegs)-1]
		runEnd := last.wallStart + int64(last.dur*1000)

		var cur *builtSession
		if n := len(out); n > 0 {
			prev := &out[n-1]
			if prev.Mode == mode && prev.Game == game && time.Duration(rsegs[0].wallStart-prev.EndMs)*time.Millisecond < sessionGap {
				cur = prev
				rsegs[0].discontAt = true
			}
		}
		if cur == nil {
			out = append(out, builtSession{Session: Session{ID: run, Game: game, Mode: mode, StartMs: rsegs[0].wallStart}})
			cur = &out[len(out)-1]
		}
		cur.Runs = append(cur.Runs, run)
		for _, ts := range rsegs {
			ts.offset = cur.DurationSec
			cur.DurationSec += ts.dur
			cur.SizeBytes += ts.seg.Size
			cur.segs = append(cur.segs, ts)
		}
		cur.EndMs = runEnd
		if run == liveRun {
			cur.Live = true
		}
		if kept[run] {
			cur.Kept = true
		}
	}

	keepMs := int64(st.BackgroundMinutes) * 60_000
	for i := range out {
		b := &out[i]
		b.ThumbURL = fmt.Sprintf("%sthumb/%d", recPrefix, b.ID)
		b.PlaylistURL = fmt.Sprintf("%s%d.m3u8", recPrefix, b.ID)
		if b.Mode == ModeBackground && !b.Kept && keepMs > 0 && st.Retention == RetentionTime {
			b.DeleteAtMs = b.segs[0].seg.ModTime.UnixMilli() + keepMs
		}
		b.Markers = []SessionMarker{}
		for _, m := range markers {
			if off, ok := b.offsetOf(m.T); ok {
				b.Markers = append(b.Markers, SessionMarker{Kind: m.Kind, Label: m.Label, T: m.T, OffsetSec: off})
			}
		}
	}
	return out
}

// offsetOf maps a wall-clock time onto the session timeline (ok=false when
// it falls outside the session).
func (b *builtSession) offsetOf(t int64) (float64, bool) {
	if t < b.StartMs-2000 || t > b.EndMs+2000 {
		return 0, false
	}
	for i, ts := range b.segs {
		end := ts.wallStart + int64(ts.dur*1000)
		if t < end || i == len(b.segs)-1 {
			return max(0, ts.offset+min(float64(t-ts.wallStart)/1000, ts.dur)), true
		}
	}
	return 0, false
}

func (s *ReplayService) findSession(id int) (builtSession, error) {
	for _, b := range s.buildSessions() {
		if b.ID == id {
			return b, nil
		}
	}
	return builtSession{}, fmt.Errorf("recording %d not found", id)
}

// playlist renders a session as an HLS playlist of its segments.
func (b *builtSession) playlist() string {
	var sb strings.Builder
	// TARGETDURATION must be >= every segment's duration (rounded up).
	target := 2
	for _, ts := range b.segs {
		target = max(target, int(math.Ceil(ts.dur)))
	}
	fmt.Fprintf(&sb, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n", target)
	if b.Live {
		sb.WriteString("#EXT-X-PLAYLIST-TYPE:EVENT\n")
	} else {
		sb.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	}
	for _, ts := range b.segs {
		if ts.discontAt {
			sb.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		fmt.Fprintf(&sb, "#EXTINF:%.3f,\n%sseg/%s\n", ts.dur, recPrefix, ts.seg.Name)
	}
	if !b.Live {
		sb.WriteString("#EXT-X-ENDLIST\n")
	}
	return sb.String()
}

// CreateClip saves [startSec, endSec] of a recording as a clip in the clips
// folder (in its game's folder) and returns its path. Cuts land on segment
// boundaries (2s); fine-tune in the clip editor.
func (s *ReplayService) CreateClip(id int, startSec, endSec float64) (string, error) {
	b, err := s.findSession(id)
	if err != nil {
		return "", err
	}
	if endSec <= startSec {
		return "", errors.New("select a range first")
	}
	var pick []capture.RecSegment
	var wallStart int64
	for _, ts := range b.segs {
		if ts.offset+ts.dur <= startSec || ts.offset >= endSec {
			continue
		}
		if len(pick) == 0 {
			wallStart = ts.wallStart
		}
		pick = append(pick, ts.seg)
	}
	if len(pick) == 0 {
		return "", errors.New("nothing recorded in that range")
	}
	st := s.GetSettings()
	dir, prefix := clipLocation(st, b.Game)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := uniquePath(filepath.Join(dir, prefix+" "+time.UnixMilli(wallStart).Format("2006-01-02 15.04.05")), ".mp4")
	if err := capture.StitchSegments(pick, out, nil); err != nil {
		return "", err
	}
	s.addMarkerAt(wallStart, MarkerClip, filepath.Base(out))
	emit(EventSaved, out)
	return out, nil
}

// KeepSession exempts a background recording from auto-delete (or undoes it).
func (s *ReplayService) KeepSession(id int, keep bool) error {
	b, err := s.findSession(id)
	if err != nil {
		return err
	}
	dir := recordingsDirFor(s.GetSettings())
	kept := loadKept(dir)
	for _, r := range b.Runs {
		if keep {
			kept[r] = true
		} else {
			delete(kept, r)
		}
	}
	if err := saveKept(dir, kept); err != nil {
		return err
	}
	s.invalidateProtect()
	emit(EventRecordings, nil)
	return nil
}

// DeleteSession deletes a recording (not one that is still recording).
func (s *ReplayService) DeleteSession(id int) error {
	b, err := s.findSession(id)
	if err != nil {
		return err
	}
	if b.Live {
		return errors.New("this recording is still running; stop it first")
	}
	dir := recordingsDirFor(s.GetSettings())
	kept := loadKept(dir)
	for _, r := range b.Runs {
		if err := capture.DeleteRun(dir, r); err != nil {
			return err
		}
		delete(kept, r)
	}
	_ = saveKept(dir, kept)
	s.invalidateProtect()
	emit(EventRecordings, nil)
	return nil
}

// GetSession returns one recording (fresh markers, live state).
func (s *ReplayService) GetSession(id int) (Session, error) {
	b, err := s.findSession(id)
	return b.Session, err
}

// SetSessionOpener registers how OpenSession shows a recording (main passes
// the timeline window opener).
//
//wails:ignore
func (s *ReplayService) SetSessionOpener(fn func(id int)) {
	s.mu.Lock()
	s.sessionOpener = fn
	s.mu.Unlock()
}

// OpenSession opens a recording in the timeline viewer.
func (s *ReplayService) OpenSession(id int) error {
	if _, err := s.findSession(id); err != nil {
		return err
	}
	s.mu.Lock()
	open := s.sessionOpener
	s.mu.Unlock()
	if open == nil {
		return errors.New("timeline viewer unavailable")
	}
	open(id)
	return nil
}

// AddSessionMarker drops a marker offsetSec into a recording (the viewer's
// add-marker button).
func (s *ReplayService) AddSessionMarker(id int, offsetSec float64) error {
	b, err := s.findSession(id)
	if err != nil {
		return err
	}
	for i, ts := range b.segs {
		if offsetSec < ts.offset+ts.dur || i == len(b.segs)-1 {
			s.addMarkerAt(ts.wallStart+int64(max(0, offsetSec-ts.offset)*1000), MarkerUser, "")
			return nil
		}
	}
	return errors.New("empty recording")
}

// PickRecordingsDir lets the user move where recordings are stored (new
// recordings only; existing ones stay where they are). Returns "" if
// cancelled.
func (s *ReplayService) PickRecordingsDir() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("replay: no running application")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle("Choose where recordings are stored").
		SetDirectory(recordingsDirFor(s.GetSettings()))
	if win := app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil
	}
	st := s.GetSettings()
	st.RecordingsDir = path
	if _, err := s.SetSettings(st); err != nil {
		return "", err
	}
	return path, nil
}

// ResetRecordingsDir goes back to the default recordings folder.
func (s *ReplayService) ResetRecordingsDir() error {
	st := s.GetSettings()
	st.RecordingsDir = ""
	_, err := s.SetSettings(st)
	return err
}

// OpenRecordingsDir reveals the recordings folder.
func (s *ReplayService) OpenRecordingsDir() error {
	dir := recordingsDirFor(s.GetSettings())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return openFolder(dir)
}

var tsSegName = regexp.MustCompile(`^r\d{4,}-\d{8}\.ts$`)

// serveRecording handles /__rec/<id>.m3u8, /__rec/seg/<file> and
// /__rec/thumb/<id>. Everything is confined to the recordings folder.
func (s *ReplayService) serveRecording(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, recPrefix)
	dir := recordingsDirFor(s.GetSettings())
	switch {
	case strings.HasPrefix(rest, "seg/"):
		name := strings.TrimPrefix(rest, "seg/")
		if !tsSegName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Set("Cache-Control", "max-age=3600")
		http.ServeFile(w, r, filepath.Join(dir, name))
	case strings.HasPrefix(rest, "thumb/"):
		id, err := strconv.Atoi(strings.TrimPrefix(rest, "thumb/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		b, err := s.findSession(id)
		if err != nil || len(b.segs) == 0 {
			http.NotFound(w, r)
			return
		}
		thumb, err := ensureThumb(b.segs[min(1, len(b.segs)-1)].seg.Path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=86400")
		http.ServeFile(w, r, thumb)
	case strings.HasSuffix(rest, ".m3u8"):
		id, err := strconv.Atoi(strings.TrimSuffix(rest, ".m3u8"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		b, err := s.findSession(id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(b.playlist()))
	default:
		http.NotFound(w, r)
	}
}
