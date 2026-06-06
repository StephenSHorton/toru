package main

import (
	"net/url"
	"path/filepath"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/overlay"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// servedFileURL turns an absolute temp-file path (a committed screenshot crop or
// recording, always under %TEMP%/toru) into the /__file/<basename> URL the
// ShotMiddleware serves, so the webview can actually fetch it. The raw path stays
// the contract value in CaptureResult; only the window's media src is rewritten.
func servedFileURL(absPath string) string {
	return "/__file/" + url.PathEscape(filepath.Base(absPath))
}

// WindowsService opens Toru's separate windows (overlay, screenshot editor,
// trim editor). Each window loads a distinct frontend route. This is the
// multi-window backbone that lets Developer 1 and Developer 2 own independent
// windows. (JS binding name: WindowsService.*)
type WindowsService struct {
	app     *application.App
	cap     capture.Capturer
	overlay *overlay.OverlayService
}

// dark is Toru's window background (matches the dark theme; sharp, no chrome rounding).
var dark = application.NewRGB(10, 10, 12)

// OpenOverlay opens the shared dim/crop capture overlay session: one frameless,
// always-on-top, opaque window per monitor showing a FROZEN still dimmed with a
// crop hole. Delegates to OverlayService.BeginSession, which freezes every
// monitor BEFORE any window is shown. This is the launch + hotkey + tray
// entrypoint.
func (w *WindowsService) OpenOverlay() {
	if w.overlay == nil {
		return
	}
	_, _ = w.overlay.BeginSession()
}

// OpenHub opens the dev Hub window (buttons to drive both editors during Phase
// 0). Cancel/Esc on the overlay returns here.
func (w *WindowsService) OpenHub() {
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru",
		URL:              "/?view=hub",
		Width:            720,
		Height:           560,
		BackgroundColour: dark,
	})
}

// OpenEditor opens Developer 1's screenshot annotation editor for imagePath.
//
// The SPA routes on the ?view= query param (App.tsx) and the embedded asset
// server has no /editor path, so the window URL MUST be /?view=editor (a bare
// /editor 404s). The webview also can't load a raw C:\ path as <img src>, so the
// committed PNG is handed over as the /__file/<basename> served URL.
func (w *WindowsService) OpenEditor(imagePath string) {
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Edit Screenshot",
		URL:              "/?view=editor&img=" + url.QueryEscape(servedFileURL(imagePath)),
		Width:            1000,
		Height:           720,
		BackgroundColour: dark,
	})
}

// OpenTrim opens Developer 2's trim editor for videoPath. Same routing + served-
// file rules as OpenEditor (/?view=trim, /__file/<basename> for the media src).
func (w *WindowsService) OpenTrim(videoPath string) {
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Trim Recording",
		URL:              "/?view=trim&vid=" + url.QueryEscape(servedFileURL(videoPath)),
		Width:            900,
		Height:           620,
		BackgroundColour: dark,
	})
}

// OpenRecordingControls opens the small frameless always-on-top "recording
// pill" (timer + Stop) for an in-flight recording. The overlay calls this
// right after StartRecording — without it a recording has NO stop affordance
// (the tray Stop square is still a Phase-0 stub).
//
// Placement: top-center of the PRIMARY monitor's work area. The pill can
// appear inside the recorded region (e.g. fullscreen recordings) — acceptable
// for v1, same trade-off Loom makes; the tray Stop square later removes it.
func (w *WindowsService) OpenRecordingControls(handleID string) {
	const pillW, pillH = 240, 64
	opts := application.WebviewWindowOptions{
		Name:             "toru-recording-pill",
		Title:            "Toru — Recording",
		URL:              "/?view=recording&handle=" + url.QueryEscape(handleID),
		Width:            pillW,
		Height:           pillH,
		Frameless:        true,
		AlwaysOnTop:      true,
		DisableResize:    true,
		BackgroundColour: dark,
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
			HiddenOnTaskbar:                   true,
		},
	}
	// Position at top-center of the primary screen's work area (DIP coords,
	// matching the overlay windows' use of InitialPosition WindowXY).
	if scr := w.app.Screen.GetPrimary(); scr != nil {
		opts.X = scr.WorkArea.X + (scr.WorkArea.Width-pillW)/2
		opts.Y = scr.WorkArea.Y + 16
		opts.InitialPosition = application.WindowXY
	}
	w.app.Window.NewWithOptions(opts)
}

// SimulateCapture runs the (stubbed) capture seam for the given mode and opens
// the matching editor window. This is the dev-hub shortcut that exercises the
// whole path — capture -> CaptureResult -> route-by-mode -> editor window —
// before global hotkeys + the real overlay are wired.
func (w *WindowsService) SimulateCapture(mode string) (capture.CaptureResult, error) {
	req := capture.CaptureRequest{
		Mode:      mode,
		Sub:       capture.SubRegion,
		MonitorID: 0,
		Rect:      capture.Rect{X: 0, Y: 0, W: 1280, H: 800},
	}
	res, err := w.cap.Capture(req)
	if err != nil {
		return capture.CaptureResult{}, err
	}
	if mode == capture.ModeVideo {
		w.OpenTrim(res.VideoPath)
	} else {
		w.OpenEditor(res.ImagePath)
	}
	return res, nil
}
