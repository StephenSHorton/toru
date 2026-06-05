// Package overlay owns the shared dim/crop capture overlay and the single
// source of truth for screen enumeration. Per the plan, Developer 2 leads this
// package (the video path consumes monitor-relative coordinates, so the
// rebasing and the screen source-of-truth must not fork).
package overlay

import (
	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// OverlayService is the Wails-bound overlay/coordination API (JS: OverlayService.*).
type OverlayService struct {
	app *application.App
	cap capture.Capturer
}

// NewService wires the overlay to the shared capture seam.
func NewService(cap capture.Capturer) *OverlayService { return &OverlayService{cap: cap} }

// SetApp injects the running app (called from main after application.New).
func (s *OverlayService) SetApp(app *application.App) { s.app = app }

// ListScreens is THE single source of truth for monitor enumeration that both
// halves trust. ID == ddagrab output_idx.
//
// TODO(overlay-lead): replace this stub with real enumeration via
// EnumDisplayMonitors + GetMonitorInfo + per-monitor DPI (Per-Monitor-V2).
// Returns physical-pixel, virtual-desktop-origin coordinates.
func (s *OverlayService) ListScreens() ([]capture.ScreenInfo, error) {
	return []capture.ScreenInfo{
		{ID: 0, X: 0, Y: 0, W: 1920, H: 1080, ScaleFactor: 1.0, IsPrimary: true},
	}, nil
}

// Commit runs a screenshot (synchronous) capture and broadcasts capture:done.
func (s *OverlayService) Commit(req capture.CaptureRequest) (capture.CaptureResult, error) {
	res, err := s.cap.Capture(req)
	if err != nil {
		return capture.CaptureResult{}, err
	}
	s.emit(EventCaptureDone, res)
	return res, nil
}

// Cancel dismisses ALL overlay windows (one broadcast) and notifies the UI.
func (s *OverlayService) Cancel() error {
	s.emit(EventOverlayDismiss, nil)
	s.emit(EventCaptureCancelled, nil)
	return nil
}

// StartRecording begins a long-lived recording and returns its handle.
func (s *OverlayService) StartRecording(req capture.CaptureRequest) (string, error) {
	return s.cap.StartRecording(req)
}

// StopRecording finalizes a recording and broadcasts capture:done.
func (s *OverlayService) StopRecording(handleID string) (capture.CaptureResult, error) {
	res, err := s.cap.StopRecording(handleID)
	if err != nil {
		return capture.CaptureResult{}, err
	}
	s.emit(EventCaptureDone, res)
	return res, nil
}

func (s *OverlayService) emit(name string, data any) {
	if s.app != nil {
		s.app.Event.Emit(name, data)
	}
}

// Event names broadcast Go->JS. The "capture:done" payload's Mode field is what
// routes the result to Developer 1's editor (screenshot) vs Developer 2's (video).
const (
	EventCaptureDone      = "capture:done"
	EventCaptureCancelled = "capture:cancelled"
	EventRecordProgress   = "record:progress"
	EventOverlayDismiss   = "overlay:dismiss"
	EventCaptureThumbnail = "capture:thumbnail"
)
