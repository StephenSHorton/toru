package replay

import (
	"log"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/capture"
)

// capture_health.go tells the user when the buffer's screen capture keeps
// stopping. The buffer retries by itself (capture/replay_retry.go), and
// Settings shows its error, but while a game is fullscreen nobody is looking
// at Settings: so once a failure streak reaches captureTroubleAfter, one
// notice goes up and the tray shows a line until capture recovers.

const (
	captureNoticeID = "replay-capture"
	// captureTroubleAfter is how many failed runs in a row count as trouble
	// (one or two are a display mode change or the UAC desktop).
	captureTroubleAfter = 3
)

// captureHealth is the trouble state derived from buffer statuses.
type captureHealth struct {
	troubled bool
	msg      string
}

// next returns the state after st, and what changed: trouble started, or
// capture recovered (recording again after trouble). Stopping the buffer
// clears trouble quietly.
func (h captureHealth) next(st capture.ReplayStatus) (captureHealth, bool, bool) {
	switch {
	case !st.Running:
		return captureHealth{}, false, false
	case st.Failures >= captureTroubleAfter:
		return captureHealth{troubled: true, msg: st.Error}, !h.troubled, false
	case h.troubled && st.Failures == 0 && st.Recording:
		return captureHealth{}, false, true
	}
	return h, false, false
}

// SetOnCaptureTrouble registers a callback fired when capture trouble starts
// or ends; main rebuilds the tray menu with it.
//
//wails:ignore
func (s *ReplayService) SetOnCaptureTrouble(fn func()) {
	s.mu.Lock()
	s.onTrouble = fn
	s.mu.Unlock()
}

// CaptureTrouble returns why screen capture keeps stopping, or "" while it
// works.
//
//wails:ignore
func (s *ReplayService) CaptureTrouble() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health.msg
}

// watchCapture is the buffer's status callback: it tracks trouble, posts
// its notices and tells the tray.
func (s *ReplayService) watchCapture(st capture.ReplayStatus) {
	s.mu.Lock()
	was := s.health.troubled
	h, started, recovered := s.health.next(st)
	s.health = h
	cb := s.onTrouble
	s.mu.Unlock()
	switch {
	case started:
		log.Printf("toru: replay: capture keeps stopping: %s", st.Error)
		toast.Post(toast.Note{ID: captureNoticeID, Title: "Replay & recording keeps stopping",
			Body: st.Error + " Settings > Replay & recording shows the details.", Kind: toast.Error})
	case recovered:
		log.Printf("toru: replay: capturing again")
		toast.Post(toast.Note{ID: captureNoticeID, Title: "Replay & recording is capturing again", Kind: toast.Success})
	}
	if cb != nil && was != h.troubled {
		cb()
	}
}
