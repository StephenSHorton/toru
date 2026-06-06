// Package overlay owns the shared dim/crop capture overlay and the single
// source of truth for screen enumeration. Per the plan, Developer 2 leads this
// package (the video path consumes monitor-relative coordinates, so the
// rebasing and the screen source-of-truth must not fork).
//
// The real overlay is a per-monitor FROZEN-STILL session: on BeginSession, Go
// captures an opaque still of every monitor (BEFORE any overlay window is
// shown), opens one frameless/always-on-top/opaque window per monitor covering
// its full DIP bounds, and pre-places a crop on the primary. A committed
// SCREENSHOT crops the FROZEN still in memory (never a live re-capture, which
// would photograph the dim overlays). VIDEO dismisses overlays first, then
// records the live region.
package overlay

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// OverlayService is the Wails-bound overlay/coordination API (JS: OverlayService.*).
type OverlayService struct {
	app *application.App
	cap capture.Capturer

	// openHub re-opens the dev Hub on dismiss-to-hub (injected by main via
	// SetHubOpener). Held as a Go-only field — never exposed to JS.
	openHub func()
	// editorOpener opens the screenshot editor for a committed image path
	// (injected by main via SetEditorOpener). Go-only; never exposed to JS.
	editorOpener func(imagePath string)

	mu sync.RWMutex
	// windows are the live overlay windows (one per monitor); handles kept so
	// DismissSession can Close() each — emitting overlay:dismiss alone does NOT
	// destroy the native windows.
	windows []*application.WebviewWindow
	// stills maps screenID (string) -> frozen PNG path, served via /__shot/<id>.
	stills map[string]string
	// frozen maps monitorID (kbinani idx) -> frozen PNG path, used to crop on
	// screenshot commit.
	frozen map[int]string
	// screens is the enumeration snapshot taken at BeginSession (ID == kbinani idx).
	screens []capture.ScreenInfo
}

// NewService wires the overlay to the shared capture seam.
func NewService(cap capture.Capturer) *OverlayService {
	return &OverlayService{
		cap:    cap,
		stills: map[string]string{},
		frozen: map[int]string{},
	}
}

// SetApp injects the running app (called from main after application.New).
func (s *OverlayService) SetApp(app *application.App) { s.app = app }

// SetHubOpener injects the Hub-opener callback. Called once from main with
// windowsSvc.OpenHub so Cancel/Esc can return to the dev Hub. This takes a func
// param and is therefore NOT a bindable method — it is invoked from Go only.
func (s *OverlayService) SetHubOpener(fn func()) { s.openHub = fn }

// SetEditorOpener injects the screenshot-editor opener callback. Go-only.
func (s *OverlayService) SetEditorOpener(fn func(imagePath string)) { s.editorOpener = fn }

// ListScreens is THE single source of truth for monitor enumeration that both
// halves trust. ID == kbinani idx == ddagrab output_idx.
//
// It enumerates via capture.EnumDisplays (kbinani; Windows-only behind a build
// tag) for physical, virtual-desktop-origin bounds, then enriches ScaleFactor +
// IsPrimary from the Wails ScreenManager matched by PhysicalBounds origin. The
// slice is never sorted or deduped so the index stays == output_idx.
func (s *OverlayService) ListScreens() ([]capture.ScreenInfo, error) {
	displays := capture.EnumDisplays()
	out := make([]capture.ScreenInfo, 0, len(displays))
	for _, d := range displays {
		info := capture.ScreenInfo{
			ID:          d.Index,
			X:           d.X,
			Y:           d.Y,
			W:           d.W,
			H:           d.H,
			ScaleFactor: 1.0,
			IsPrimary:   d.X == 0 && d.Y == 0,
		}
		// Enrich scale + primary from the Wails screen layout, matched by
		// physical origin (NEVER array index — kbinani order != Wails order).
		if s.app != nil {
			for _, scr := range s.app.Screen.GetAll() {
				if scr.PhysicalBounds.X == d.X && scr.PhysicalBounds.Y == d.Y {
					if scr.ScaleFactor > 0 {
						info.ScaleFactor = float64(scr.ScaleFactor)
					}
					info.IsPrimary = scr.IsPrimary
					break
				}
			}
		}
		out = append(out, info)
	}
	return out, nil
}

// BeginSession is the launch entrypoint. It enumerates screens, freezes EVERY
// monitor's still BEFORE any overlay window is shown, restores the persisted
// primary crop (or a centered default), opens one window per monitor, and
// returns the per-monitor session payloads. Returning []MonitorSession also lets
// the binding generator emit the MonitorSession TS type.
func (s *OverlayService) BeginSession() ([]MonitorSession, error) {
	// If a session is already live, tear it down first (idempotent launch).
	s.DismissSession()

	screens, err := s.ListScreens()
	if err != nil {
		return nil, err
	}
	if len(screens) == 0 {
		return nil, fmt.Errorf("overlay: no active displays")
	}

	s.mu.Lock()
	s.screens = screens
	s.mu.Unlock()

	// (1) Freeze every monitor FIRST (records s.frozen / s.stills). No window has
	// been shown yet, so no still photographs a dim overlay.
	sessions, err := s.buildSessions(screens)
	if err != nil {
		return nil, err
	}

	// (2) Only now open the per-monitor overlay windows.
	s.openOverlayWindows(sessions)

	return sessions, nil
}

// ShotMiddleware serves the frozen stills at /__shot/<screenID>. It streams the
// PNG file (NOT a base64 data URL) with Cache-Control: no-store so a re-opened
// session never serves a stale image. Registered in main via AssetOptions.Middleware.
func (s *OverlayService) ShotMiddleware() application.Middleware {
	const prefix = "/__shot/"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, prefix) {
				id := strings.TrimPrefix(r.URL.Path, prefix)
				s.mu.RLock()
				path, ok := s.stills[id]
				s.mu.RUnlock()
				if ok {
					w.Header().Set("Content-Type", "image/png")
					w.Header().Set("Cache-Control", "no-store")
					http.ServeFile(w, r, path)
					return
				}
				http.NotFound(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DismissSession closes ALL overlay windows (via the kept handles), clears the
// session state, and best-effort deletes the temp frozen PNGs. Emitting
// overlay:dismiss alone does NOT destroy the native windows — Close() does.
func (s *OverlayService) DismissSession() {
	s.mu.Lock()
	wins := s.windows
	frozen := s.frozen
	s.windows = nil
	s.stills = map[string]string{}
	s.frozen = map[int]string{}
	s.screens = nil
	s.mu.Unlock()

	for _, w := range wins {
		if w != nil {
			w.Close()
		}
	}
	for _, p := range frozen {
		_ = removeFile(p)
	}

	s.emit(EventOverlayDismiss, nil)
}

// Commit is a thin compatibility shim kept so existing bindings/tests don't
// break. For screenshots it routes through the frozen-still crop using req.Rect
// as the contract Rect and deriving the monitor-local sub-rect from the owning
// screen's origin. New code should call CommitScreenshot directly. Video is
// delegated to StartRecording (the overlay records the live region).
func (s *OverlayService) Commit(req capture.CaptureRequest) (capture.CaptureResult, error) {
	if req.Mode == capture.ModeVideo {
		handle, err := s.StartRecording(req)
		if err != nil {
			return capture.CaptureResult{}, err
		}
		return capture.CaptureResult{Mode: capture.ModeVideo, HandleID: handle, Rect: req.Rect, MonitorID: req.MonitorID}, nil
	}

	// Derive the monitor-local sub-rect from the owning screen's physical origin.
	var sub capture.Rect
	s.mu.RLock()
	for _, sc := range s.screens {
		if sc.ID == req.MonitorID {
			sub = capture.Rect{X: req.Rect.X - sc.X, Y: req.Rect.Y - sc.Y, W: req.Rect.W, H: req.Rect.H}
			break
		}
	}
	s.mu.RUnlock()
	if sub.W == 0 && sub.H == 0 {
		// No screen snapshot match (e.g. legacy caller): assume rect is already
		// monitor-local.
		sub = req.Rect
	}
	return s.CommitScreenshot(req.MonitorID, req.Rect, sub, req.CopyOnCommit)
}

// CommitScreenshot is THE screenshot path. It crops the FROZEN still for
// monitorID (NEVER a live re-capture), dismisses the overlay, emits capture:done,
// and opens the editor. rect is the contract Rect (virtual-desktop physical px)
// carried in the result; sub is the monitor-local physical crop region applied to
// the frozen PNG. Both are computed by the front end via CropToPhysical.
func (s *OverlayService) CommitScreenshot(monitorID int, rect capture.Rect, sub capture.Rect, copyOnCommit bool) (capture.CaptureResult, error) {
	s.mu.RLock()
	frozenPath := s.frozen[monitorID]
	s.mu.RUnlock()
	if frozenPath == "" {
		return capture.CaptureResult{}, fmt.Errorf("overlay: no frozen still for monitor %d (session not active?)", monitorID)
	}

	// Persist the crop (monitor-local physical px) before tearing down.
	_ = s.SaveCrop(monitorID, sub)

	out, err := capture.CropStill(frozenPath, sub)
	if err != nil {
		return capture.CaptureResult{}, err
	}

	s.DismissSession()

	res := capture.CaptureResult{
		Mode:      capture.ModeScreenshot,
		ImagePath: out,
		Rect:      rect,
		MonitorID: monitorID,
	}
	s.emit(EventCaptureDone, res)
	if s.editorOpener != nil {
		s.editorOpener(out)
	}
	return res, nil
}

// Cancel dismisses ALL overlay windows then re-opens the dev Hub so editors stay
// reachable during Phase 0, and notifies the UI.
func (s *OverlayService) Cancel() error {
	s.DismissSession()
	if s.openHub != nil {
		s.openHub()
	}
	s.emit(EventCaptureCancelled, nil)
	return nil
}

// StartRecording dismisses the overlay FIRST (so ffmpeg records the live region,
// not the dim overlays), THEN begins recording. req.Rect is the virtual-desktop
// physical Rect the front end emits.
func (s *OverlayService) StartRecording(req capture.CaptureRequest) (string, error) {
	s.DismissSession()
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

// SaveCrop persists the monitor-local PHYSICAL-px crop for monitorID. Called by
// the front end on crop drag-end (debounced) and again inside CommitScreenshot
// before dismiss.
func (s *OverlayService) SaveCrop(monitorID int, sub capture.Rect) error {
	st := loadCrops()
	st.Crops[strconv.Itoa(monitorID)] = sub
	return saveCrops(st)
}

func (s *OverlayService) emit(name string, data any) {
	if s.app != nil {
		s.app.Event.Emit(name, data)
	}
}

// removeFile is a best-effort temp-file delete (errors ignored by callers).
func removeFile(path string) error {
	if path == "" {
		return nil
	}
	return os.Remove(path)
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
