package replay

import (
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/toast"
)

// Notification IDs: the start and stop notices share one, so a recording's
// "on" card is replaced by its "saved" card instead of stacking.
const (
	recordingNoticeID = "recording"
	markerNoticeID    = "recording-marker"
)

// startedNoticeFor is how long a "recording is on" card stays.
const startedNoticeFor = 6 * time.Second

// SetHotkeyLabel registers how notices name a hotkey ("Ctrl+F11") from its
// action ID (replaySave, recordToggle). main passes the live bindings so a
// rebound key reads right.
//
//wails:ignore
func (s *ReplayService) SetHotkeyLabel(fn func(action string) string) {
	s.mu.Lock()
	s.hotkeyLabel = fn
	s.mu.Unlock()
}

// keyName returns the hotkey for action, or fallback when it is unbound.
func (s *ReplayService) keyName(action, fallback string) string {
	s.mu.Lock()
	fn := s.hotkeyLabel
	s.mu.Unlock()
	if fn != nil {
		if l := fn(action); l != "" {
			return l
		}
	}
	return fallback
}

// announce posts the notice for a change in what the buffer records: from is
// the config it ran with before reconcile, to the one it runs with now (zero
// = stopped). A restart for a new config says nothing; turning on says what
// records, and a background or hotkey recording ending says its Game session
// was saved.
func (s *ReplayService) announce(from, to capture.ReplayConfig) {
	if !s.GetSettings().RecordingNotices {
		return
	}
	switch started, saved := transition(from, to); {
	case started:
		toast.Post(s.startedNote(to))
	case saved:
		s.savedNotice(from)
	}
}

// transition classifies a reconcile: recording started from nothing, or a
// Game session (background or hotkey recording) ended. A restart with a new
// config is neither.
func transition(from, to capture.ReplayConfig) (started, saved bool) {
	zero := capture.ReplayConfig{}
	switch {
	case from == to:
		return false, false
	case from == zero:
		return true, false
	}
	return false, from.Persist && !to.Persist
}

// startedNote says recording is on, in the glossary's terms: instant replay
// keeps a clip's worth, background and hotkey recording make a Game session.
// It stays longer than the default: at a login launch the user may not be
// looking yet.
func (s *ReplayService) startedNote(cfg capture.ReplayConfig) toast.Note {
	n := toast.Note{ID: recordingNoticeID, Title: "Game recording is on", Kind: toast.Info,
		Expire: startedNoticeFor}
	mode, game := ModeReplay, ""
	if cfg.Persist {
		mode, game = decodeTag(cfg.Tag)
	}
	switch mode {
	case ModeReplay:
		n.Body = "Instant replay · " + s.keyName("replaySave", "the save hotkey") +
			" saves the last " + formatClipLength(cfg.KeepSeconds) + " as a clip"
	case ModeManual:
		n.Title = "Hotkey recording started"
		n.Body = "Press " + s.keyName("recordToggle", "the record hotkey") + " again to stop"
	default:
		n.Title = "Background recording started"
		n.Body = "Recording your screen as a Game session"
		if game != "" {
			n.Title = "Recording " + game
			n.Body = "Background recording · saved as a Game session"
		}
	}
	return n
}

// savedNotice says a Game session ended and offers to open it in the
// timeline viewer.
func (s *ReplayService) savedNotice(cfg capture.ReplayConfig) {
	mode, game := decodeTag(cfg.Tag)
	body := "It's in the Library's Game sessions tab"
	if game != "" {
		body = game + " closed · it's in the Library's Game sessions tab"
	}
	toast.Post(toast.Note{
		ID: recordingNoticeID, Title: "Game session saved", Body: body, Kind: toast.Success,
		Actions: []toast.Action{{Label: "Open", Run: func() { s.openLatestSession(mode, game) }}},
	})
}

// openLatestSession opens the newest Game session of mode and game (the one
// a saved notice is about), else the newest one.
func (s *ReplayService) openLatestSession(mode, game string) {
	sessions := s.ListSessions()
	if len(sessions) == 0 {
		return
	}
	id := sessions[0].ID
	for _, ss := range sessions {
		if ss.Mode == mode && ss.Game == game {
			id = ss.ID
			break
		}
	}
	_ = s.OpenSession(id)
}

// startupNotice runs once from Init. When recording started, reconcile has
// already said so; background recording that waits for a game hasn't
// started yet, so this says it is armed, which is what a login launch (no
// window) otherwise never shows. A start that failed for a reason other than
// the FFmpeg download in progress is reported whatever RecordingNotices says:
// the user turned recording on and it is not running.
func (s *ReplayService) startupNotice(err error) {
	st := s.GetSettings()
	if !st.Enabled {
		return
	}
	if err != nil {
		if ff := s.GetFFmpegState(); ff.Ready && !ff.Downloading {
			toast.Post(toast.Note{ID: recordingNoticeID, Title: "Game recording couldn't start",
				Body: err.Error(), Kind: toast.Error})
		}
		return
	}
	if st.RecordingNotices && st.Mode == ModeBackground && st.GamesOnly && !s.buf.Running() {
		toast.Post(toast.Note{ID: recordingNoticeID, Title: "Game recording is on",
			Body: "Background recording starts when you play a game", Kind: toast.Info,
			Expire: startedNoticeFor})
	}
}

// markerNotice confirms the marker hotkey. Its own ID, so a burst of markers
// replaces one card instead of stacking.
func markerNotice() {
	toast.Post(toast.Note{ID: markerNoticeID, Title: "Marker added",
		Body: "On this Game session's timeline", Kind: toast.Success})
}
