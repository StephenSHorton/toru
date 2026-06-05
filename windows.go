package main

import (
	"net/url"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// WindowsService opens Toru's separate windows (overlay, screenshot editor,
// trim editor). Each window loads a distinct frontend route. This is the
// multi-window backbone that lets Developer 1 and Developer 2 own independent
// windows. (JS binding name: WindowsService.*)
type WindowsService struct {
	app *application.App
	cap capture.Capturer
}

// dark is Toru's window background (matches the dark theme; sharp, no chrome rounding).
var dark = application.NewRGB(10, 10, 12)

// OpenOverlay opens the shared dim/crop capture overlay.
//
// NOTE: the real overlay is transparent + frameless + always-on-top + one per
// monitor (the Phase 0 spike). Kept as a normal window here so the skeleton
// builds and runs before that spike lands.
func (w *WindowsService) OpenOverlay() {
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Capture",
		URL:              "/overlay",
		Width:            1100,
		Height:           700,
		BackgroundColour: dark,
	})
}

// OpenEditor opens Developer 1's screenshot annotation editor for imagePath.
func (w *WindowsService) OpenEditor(imagePath string) {
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Edit Screenshot",
		URL:              "/editor?img=" + url.QueryEscape(imagePath),
		Width:            1000,
		Height:           720,
		BackgroundColour: dark,
	})
}

// OpenTrim opens Developer 2's trim editor for videoPath.
func (w *WindowsService) OpenTrim(videoPath string) {
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Trim Recording",
		URL:              "/trim?vid=" + url.QueryEscape(videoPath),
		Width:            900,
		Height:           620,
		BackgroundColour: dark,
	})
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
