package replay

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// EventRecordings fires when the list of session recordings changes (a
// recording started or stopped, a clip/marker was added, or one was deleted).
const EventRecordings = "recording:changed"

// Marker kinds on a session timeline.
const (
	MarkerStart = "start"      // recording started (label: game)
	MarkerUser  = "marker"     // the add-marker hotkey / viewer button
	MarkerClip  = "clip"       // a clip was saved here
	MarkerShot  = "screenshot" // a Toru screenshot was taken here
)

// Marker is one timeline marker, stored in <recordings>\markers.jsonl.
type Marker struct {
	T     int64  `json:"t"` // Unix ms
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

// RecorderState is the recording status the Settings page and tray show.
type RecorderState struct {
	Mode      string `json:"mode"`
	Enabled   bool   `json:"enabled"`
	Recording bool   `json:"recording"` // a session/manual recording is capturing now
	Game      string `json:"game"`      // game the background recording follows
	Manual    bool   `json:"manual"`    // a manual recording is on
	DiskBytes int64  `json:"diskBytes"` // recordings on disk
	MaxBytes  int64  `json:"maxBytes"`
	Dir       string `json:"dir"`
}

// encodeTag/decodeTag store a run's mode and game in its sidecar.
func encodeTag(mode, game string) string { return mode + "|" + game }

func decodeTag(tag string) (mode, game string) {
	mode, game, _ = strings.Cut(tag, "|")
	return mode, game
}

// GetRecorderState returns the live recording state.
func (s *ReplayService) GetRecorderState() RecorderState {
	st := s.GetSettings()
	s.mu.Lock()
	game, manual, cfg := s.game, s.manualOn, s.runningCfg
	s.mu.Unlock()
	bs := s.buf.Status()
	disk := bs.DiskBytes
	if !cfg.Persist {
		disk = dirSegmentBytes(recordingsDirFor(st))
	}
	return RecorderState{
		Mode:      st.Mode,
		Enabled:   st.Enabled,
		Recording: cfg.Persist && bs.Running,
		Game:      game,
		Manual:    manual,
		DiskBytes: disk,
		MaxBytes:  int64(st.MaxDiskGB) << 30,
		Dir:       recordingsDirFor(st),
	}
}

func dirSegmentBytes(dir string) int64 {
	var n int64
	for _, seg := range capture.ListRecSegments(dir) {
		n += seg.Size
	}
	return n
}

// ToggleRecording starts or stops a manual recording (the record hotkey).
// Returns whether a recording is now on.
func (s *ReplayService) ToggleRecording() (bool, error) {
	st := s.GetSettings()
	if !st.Enabled || st.Mode != ModeManual {
		return false, errors.New("manual recording is off: choose Manual recording in Settings")
	}
	s.mu.Lock()
	s.manualOn = !s.manualOn
	on := s.manualOn
	s.mu.Unlock()
	err := s.reconcile()
	if err != nil {
		s.mu.Lock()
		s.manualOn = false
		s.mu.Unlock()
		return false, err
	}
	if st.RecordingNotices {
		if on {
			showToast("Recording", "Press the record hotkey again to stop", true)
		} else {
			showToast("Recording saved", "Open it from the Library's Recordings tab", true)
		}
	}
	emit(EventRecordings, nil)
	return on, nil
}

// AddMarker drops a marker on the current recording's timeline (the marker
// hotkey).
func (s *ReplayService) AddMarker() error {
	if !s.recordingSession() {
		return errors.New("not recording a session")
	}
	s.addMarker(MarkerUser, "")
	if s.GetSettings().RecordingNotices {
		showToast("Marker added", "Find it on the recording's timeline", true)
	}
	return nil
}

// NoteScreenshot marks a screenshot on the current recording's timeline.
//
//wails:ignore
func (s *ReplayService) NoteScreenshot() {
	if s.recordingSession() {
		s.addMarker(MarkerShot, "")
	}
}

func (s *ReplayService) recordingSession() bool {
	s.mu.Lock()
	persist := s.runningCfg.Persist
	s.mu.Unlock()
	return persist && s.buf.Running()
}

var markersMu sync.Mutex

func (s *ReplayService) addMarker(kind, label string) {
	s.addMarkerAt(time.Now().UnixMilli(), kind, label)
}

func (s *ReplayService) addMarkerAt(t int64, kind, label string) {
	dir := recordingsDirFor(s.GetSettings())
	if dir == "" {
		return
	}
	raw, err := json.Marshal(Marker{T: t, Kind: kind, Label: label})
	if err != nil {
		return
	}
	markersMu.Lock()
	defer markersMu.Unlock()
	_ = os.MkdirAll(dir, 0o755)
	f, err := os.OpenFile(filepath.Join(dir, "markers.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Close()
	emit(EventRecordings, nil)
}

func loadMarkers(dir string) []Marker {
	markersMu.Lock()
	defer markersMu.Unlock()
	f, err := os.Open(filepath.Join(dir, "markers.jsonl"))
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []Marker
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m Marker
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.T > 0 {
			out = append(out, m)
		}
	}
	return out
}

// --- game watcher (background recording that follows games) ---------------

func (s *ReplayService) startGameWatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchStop != nil {
		return
	}
	stop := make(chan struct{})
	s.watchStop = stop
	go s.watchGames(stop)
}

func (s *ReplayService) stopGameWatch() {
	s.mu.Lock()
	stop := s.watchStop
	s.watchStop = nil
	s.game, s.gamePID = "", 0
	s.mu.Unlock()
	if stop != nil {
		close(stop)
	}
}

// watchGames starts a recording when a game takes focus and stops it when
// that game's process exits (alt-tabbing out keeps recording, like Steam).
func (s *ReplayService) watchGames(stop chan struct{}) {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		s.mu.Lock()
		game, pid := s.game, s.gamePID
		s.mu.Unlock()

		if game != "" {
			if processAlive(pid) {
				continue
			}
			s.mu.Lock()
			if s.watchStop != stop {
				s.mu.Unlock()
				return
			}
			s.game, s.gamePID = "", 0
			s.mu.Unlock()
			_ = s.reconcile()
			if s.GetSettings().RecordingNotices {
				showToast("Recording saved", game+" closed · it's in the Library's Recordings tab", true)
			}
			emit(EventRecordings, nil)
			continue
		}

		app, ok := foreground()
		if !ok || app.PID == 0 {
			continue
		}
		g := gameFolderFor(app)
		if g == "" {
			continue
		}
		s.mu.Lock()
		if s.watchStop != stop {
			s.mu.Unlock()
			return
		}
		s.game, s.gamePID = g, app.PID
		s.mu.Unlock()
		if err := s.reconcile(); err == nil && s.GetSettings().RecordingNotices {
			showToast("Recording in the background", g, true)
		}
		emit(EventRecordings, nil)
	}
}

// --- protection: runs pruning must keep -------------------------------------

// protectCache answers "is this run protected?" for the pruner, which asks
// once per segment. Only manual recordings are protected (the user started
// them on purpose); background footage the user wants to keep must be saved
// as a clip.
type protectCache struct {
	mu     sync.Mutex
	dir    string
	manual map[int]bool
}

func (s *ReplayService) isProtected(run int) bool {
	dir := recordingsDirFor(s.GetSettings())
	c := &s.protect
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.dir != dir || c.manual == nil {
		c.dir, c.manual = dir, map[int]bool{}
	}
	manual, seen := c.manual[run]
	if !seen {
		info, _ := capture.ReadRunInfo(dir, run)
		mode, _ := decodeTag(info.Tag)
		manual = mode == ModeManual
		c.manual[run] = manual
	}
	return manual
}

func (s *ReplayService) invalidateProtect() {
	s.protect.mu.Lock()
	s.protect.manual = nil
	s.protect.mu.Unlock()
}
