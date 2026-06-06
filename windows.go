package main

import (
	"net/url"
	"path/filepath"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/overlay"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
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

	// settingsWin is the single live Settings/home window. Toru is a tray app that
	// lives forever, so the tray left-click + tray "Settings…" + editor gear all
	// route through OpenSettings repeatedly; without a singleton each call would
	// stack ANOTHER frameless window (and DisableQuitOnLastWindowClosed keeps the
	// orphans alive). Held here so OpenSettings can Show().Focus() the existing one
	// instead. Cleared on the window's WindowClosing event. Only ever touched from
	// the main thread (ApplicationStarted listener, tray callbacks marshal to it,
	// the editor gear's JS call lands on the main thread), so no extra locking.
	settingsWin *application.WebviewWindow
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

// OpenSettings opens Toru's Settings/home window: the tray-driven hub that hosts
// the Shortcuts panel, the updater/about, and a Capture button. Reached from the
// tray menu, the tray left-click, the ApplicationStarted launch, and the editor's
// floating gear button.
//
// SINGLETON: the Settings window is the app's persistent home, hit repeatedly from
// the tray. If one is already open this Show().Focus()es it (restoring it first if
// minimised) instead of spawning a duplicate frameless window — without this, every
// tray click would stack another window that DisableQuitOnLastWindowClosed then
// keeps alive forever, reintroducing the exact multi-window confusion this redesign
// removed. The handle is cleared when the window closes.
func (w *WindowsService) OpenSettings() {
	if w.app == nil {
		return
	}
	if w.settingsWin != nil {
		w.settingsWin.Restore() // no-op if normal; un-minimises if minimised
		w.settingsWin.Show().Focus()
		return
	}
	win := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Settings",
		URL:              "/?view=settings",
		Width:            720,
		Height:           560,
		BackgroundColour: dark,
		// AlwaysOnTop so summoning Settings from the editor's ⚙ raises it ABOVE the
		// always-on-top capture/edit overlay — otherwise Show().Focus() lands it
		// behind the overlay and the user (mid-edit) never sees it appear.
		AlwaysOnTop: true,
	})
	w.settingsWin = win
	// Drop the handle when this window closes so the next OpenSettings creates a
	// fresh one (a closed WebviewWindow's Show/Focus are no-ops, which would
	// otherwise leave the tray's "open home" silently dead).
	win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
		w.settingsWin = nil
	})
}

// OpenEditor opens Developer 1's screenshot annotation editor for imagePath.
//
// The SPA routes on the ?view= query param (App.tsx) and the embedded asset
// server has no /editor path, so the window URL MUST be /?view=editor (a bare
// /editor 404s). The webview also can't load a raw C:\ path as <img src>, so the
// committed PNG is handed over as the /__file/<basename> served URL.
func (w *WindowsService) OpenEditor(imagePath string) {
	if w.app == nil {
		return
	}
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
	if w.app == nil {
		return
	}
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Trim Recording",
		URL:              "/?view=trim&vid=" + url.QueryEscape(servedFileURL(videoPath)),
		Width:            900,
		Height:           620,
		BackgroundColour: dark,
	})
}

// OpenRecordingControls opens the small frameless always-on-top "recording
// pill" (timer + Stop) for an in-flight recording. The overlay service calls
// this from Go right after StartRecording — without it a recording has NO
// stop affordance (the tray Stop square is still a Phase-0 stub).
//
// Placement: top-center of a monitor that is NOT being recorded when one
// exists (so the pill never appears inside a fullscreen recording); otherwise
// top-center of the recorded monitor's work area — same trade-off Loom makes.
func (w *WindowsService) OpenRecordingControls(handleID string, monitorID int) {
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
	if scr := pillScreen(w.app, monitorID); scr != nil {
		opts.X = scr.WorkArea.X + (scr.WorkArea.Width-pillW)/2
		opts.Y = scr.WorkArea.Y + 16
		opts.InitialPosition = application.WindowXY
	}
	w.app.Window.NewWithOptions(opts)
}

// pillScreen picks the Wails screen for the recording pill: prefer one whose
// PHYSICAL bounds do not overlap the recorded monitor (contract MonitorID ==
// kbinani EnumDisplays index), fall back to the primary. Matching is by
// rectangle overlap, same as the overlay's screen enrichment.
func pillScreen(app *application.App, monitorID int) *application.Screen {
	if app == nil {
		return nil
	}
	screens := app.Screen.GetAll()
	if len(screens) == 0 {
		return nil
	}
	var rec *capture.DisplayBounds
	for _, d := range capture.EnumDisplays() {
		if d.Index == monitorID {
			d := d
			rec = &d
			break
		}
	}
	if rec != nil {
		for _, scr := range screens {
			b := scr.PhysicalBounds
			overlapsX := b.X < rec.X+rec.W && rec.X < b.X+b.Width
			overlapsY := b.Y < rec.Y+rec.H && rec.Y < b.Y+b.Height
			if !(overlapsX && overlapsY) {
				return scr
			}
		}
	}
	return app.Screen.GetPrimary()
}

// SimulateCapture runs the capture seam for the given mode and opens the matching
// editor window. It exercises the whole path — capture -> CaptureResult ->
// route-by-mode -> editor window — without going through the overlay/hotkey.
//
// Go-only test/dev hook: the dev hub that used to call it is gone and the frontend
// no longer references it, so it is marked //wails:ignore to drop it from the JS
// binding rather than widen the bound surface with an unused method.
//
//wails:ignore
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
