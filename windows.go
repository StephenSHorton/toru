package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

	// recFrameWin is the single live recorded-region border window (click-through,
	// transparent). Held so StopRecording can close it via CloseRecordingFrame.
	//
	// Open (StartRecording's bound-call goroutine) and Close (StopRecording's
	// bound-call goroutine) run on DIFFERENT goroutines, but never concurrently: the
	// StartRecording call fully returns and JS renders the pill before any Stop can
	// be issued, so the Start→…→Stop lifecycle serializes the field access (a
	// happens-before through the call round-trip). The window ops themselves
	// (NewWithOptions/Close) marshal to the main thread internally and Close no-ops
	// on an already-destroyed window, so no extra locking is needed here.
	recFrameWin *application.WebviewWindow
}

// recFrameMargin is the DIP gap between the recorded region's edge and the
// indicator window's edge. The glowing outline is drawn within this band, OUTSIDE
// the recorded rect, so ffmpeg never captures it (even after DIP->physical
// rounding, margin*scale leaves several px of clearance).
const recFrameMargin = 10

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
//
// TEMP vs HISTORY lifecycle: the straddle-screenshot path (EnterEditMulti) hands
// over a stitched %TEMP%/toru PNG that is NOT tracked by the overlay's session
// cleanup, so THIS window removes it on close (otherwise every straddle capture
// leaks one PNG for the process lifetime). History re-opens under
// %AppData%/toru/captures MUST NOT be deleted — those files power the tray
// Recent menu. isToruTempPath gates the removal.
func (w *WindowsService) OpenEditor(imagePath string) {
	if w.app == nil {
		return
	}
	win := w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru — Edit Screenshot",
		URL:              "/?view=editor&img=" + url.QueryEscape(servedFileURL(imagePath)),
		Width:            1000,
		Height:           720,
		BackgroundColour: dark,
	})
	if imagePath != "" && isToruTempPath(imagePath) {
		path := imagePath
		win.OnWindowEvent(events.Common.WindowClosing, func(*application.WindowEvent) {
			_ = os.Remove(path)
		})
	}
}

// isToruTempPath reports whether p lives under %TEMP%/toru (session-only
// artifacts). History captures live under %AppData%/toru/captures and must be
// retained across editor close.
func isToruTempPath(p string) bool {
	if p == "" {
		return false
	}
	tmp, err := filepath.Abs(filepath.Join(os.TempDir(), "toru"))
	if err != nil {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	// Case-insensitive prefix match (Windows paths); require a path separator
	// after the temp root so we don't match e.g. %TEMP%/toru-other/file.
	absLower := strings.ToLower(abs)
	tmpLower := strings.ToLower(tmp)
	if absLower == tmpLower {
		return true
	}
	prefix := tmpLower + string(os.PathSeparator)
	return strings.HasPrefix(absLower, prefix)
}

// OpenTrim opens Developer 2's trim editor for videoPath. Same routing + served-
// file rules as OpenEditor (/?view=trim, /__file/<basename> for the media src).
// The raw absolute path rides along as ?path= — the webview needs the served
// URL to PLAY the file, but Copy/Save-As/Trim are Go-side operations on the
// real path, which cannot be reconstructed from the served URL.
func (w *WindowsService) OpenTrim(videoPath string) {
	if w.app == nil {
		return
	}
	w.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Toru — Trim Recording",
		URL: "/?view=trim&vid=" + url.QueryEscape(servedFileURL(videoPath)) +
			"&path=" + url.QueryEscape(videoPath),
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
// Placement: for a REGION recording the pill anchors just BELOW the recorded
// region (centred on it, flipped ABOVE when there's no room below), on the
// recorded monitor and clamped to its work area — so it appears right where the
// user drew the crop. The old behaviour put it top-centre of a monitor that was
// NOT being recorded, which on multi-monitor setups stranded the only Stop control
// on a different screen ("I don't see the time box anywhere"). For a FULLSCREEN
// recording there is no off-region space on the recorded monitor, so it falls back
// to top-centre of an idle monitor (pillScreen) when one exists. regionX/Y/W/H are
// the recorded region's DIP bounds (ignored when fullscreen).
func (w *WindowsService) OpenRecordingControls(handleID string, monitorID, regionX, regionY, regionW, regionH int, fullscreen bool) {
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
	if x, y, ok := pillPlacement(w.app, monitorID, regionX, regionY, regionW, regionH, fullscreen, pillW, pillH); ok {
		opts.X = x
		opts.Y = y
		opts.InitialPosition = application.WindowXY
	}
	w.app.Window.NewWithOptions(opts)
}

// pillPlacement computes the recording pill's top-left DIP position. For a region
// recording it sits just OUTSIDE the recorded region on the recorded monitor —
// below it, or above when there's no room below — centred and clamped to the work
// area so it is always fully on-screen and never overlaps the captured pixels. For
// a fullscreen recording (or when the region/monitor can't be resolved) it uses
// pillScreen's top-centre-of-an-idle-monitor fallback. ok is false only when no
// screen at all could be found (the caller then lets Wails default-place it).
func pillPlacement(app *application.App, monitorID, rx, ry, rw, rh int, fullscreen bool, pillW, pillH int) (int, int, bool) {
	if !fullscreen && rw > 0 && rh > 0 {
		if scr := screenForMonitor(app, monitorID); scr != nil {
			wa := scr.WorkArea
			const gap = recFrameMargin + 8 // clear the glowing border band
			// Centred on the region horizontally; the Y we pick below is always a band
			// that is fully off the region (>= gap+pillH tall), so this X can never put
			// the pill over the captured pixels.
			x := clampInt(rx+(rw-pillW)/2, wa.X, wa.X+wa.Width-pillW)
			roomBelow := (wa.Y + wa.Height) - (ry + rh)
			roomAbove := ry - wa.Y
			switch {
			case roomBelow >= gap+pillH:
				return x, ry + rh + gap, true // just below the region
			case roomAbove >= gap+pillH:
				return x, ry - gap - pillH, true // no room below -> just above
			}
			// The region fills the monitor vertically: no off-region band fits. Fall
			// through to the idle-monitor top-centre rather than CLAMPING the pill back
			// onto the captured pixels — a clamped pill would bake into the video, the
			// very bug this indicator exists to avoid.
		}
	}
	if scr := pillScreen(app, monitorID); scr != nil {
		return scr.WorkArea.X + (scr.WorkArea.Width-pillW)/2, scr.WorkArea.Y + 16, true
	}
	return 0, 0, false
}

// screenForMonitor returns the Wails screen that OVERLAPS the recorded monitor
// (contract MonitorID == kbinani EnumDisplays index) — i.e. the monitor being
// recorded, the inverse of pillScreen. Falls back to the primary.
func screenForMonitor(app *application.App, monitorID int) *application.Screen {
	if app == nil {
		return nil
	}
	for _, d := range capture.EnumDisplays() {
		if d.Index != monitorID {
			continue
		}
		for _, scr := range app.Screen.GetAll() {
			b := scr.PhysicalBounds
			overlapsX := b.X < d.X+d.W && d.X < b.X+b.Width
			overlapsY := b.Y < d.Y+d.H && d.Y < b.Y+b.Height
			if overlapsX && overlapsY {
				return scr
			}
		}
		break
	}
	return app.Screen.GetPrimary()
}

// clampInt clamps v into [lo, hi]; if the range is empty (hi < lo, e.g. a window
// wider than the work area) it returns lo so the window stays anchored to X/Y.
func clampInt(v, lo, hi int) int {
	if hi < lo || v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// OpenRecordingError surfaces a FAILED StartRecording as a small dismissible pill.
// The overlay is already hidden by the time ffmpeg reports it can't capture, so
// without this the user is left with a blank screen and no idea why nothing
// recorded. Same frameless placement as the live pill; the recording route renders
// the error state off the ?startError= param.
func (w *WindowsService) OpenRecordingError(message string, monitorID int) {
	if w.app == nil {
		return
	}
	const pillW, pillH = 360, 64
	opts := application.WebviewWindowOptions{
		Name:             "toru-recording-error",
		Title:            "Toru — Recording failed",
		URL:              "/?view=recording&startError=" + url.QueryEscape(message),
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

// OpenRecordingFrame opens the "glowing border" window that outlines the recorded
// region while recording. The overlay passes the EXACT region DIP bounds; we
// expand by recFrameMargin on every side and hand the margin to React, which draws
// the animated outline within that band — OUTSIDE the recorded rect, so ffmpeg
// never captures it.
//
// The window is TRANSPARENT (DirectComposition) + click-through: every pixel,
// including the outline band, passes mouse input through to whatever is being
// recorded, so the indicator never steals a click. The click-through is applied
// via makeWindowClickThrough (WS_EX_TRANSPARENT only) rather than Wails'
// IgnoreMouseEvents, which on a transparent window ALSO adds WS_EX_LAYERED and
// composites it opaquely — whiting out the see-through region with a solid
// rectangle. Singleton: a stale frame from a previous recording is closed first.
func (w *WindowsService) OpenRecordingFrame(dipX, dipY, dipW, dipH, monitorID int) {
	if w.app == nil {
		return
	}
	w.CloseRecordingFrame() // never stack two outlines
	opts := application.WebviewWindowOptions{
		Name:            "toru-recording-frame",
		Title:           "Toru — Recording region",
		URL:             "/?view=recframe&m=" + url.QueryEscape(itoa(recFrameMargin)),
		X:               dipX - recFrameMargin,
		Y:               dipY - recFrameMargin,
		Width:           dipW + 2*recFrameMargin,
		Height:          dipH + 2*recFrameMargin,
		InitialPosition: application.WindowXY,
		Frameless:       true,
		AlwaysOnTop:     true,
		DisableResize:   true,
		// Transparent (DirectComposition) so only the outline paints and the recorded
		// region shows through. We deliberately do NOT set Wails' IgnoreMouseEvents:
		// on a transparent window it also adds WS_EX_LAYERED, which composites the
		// window OPAQUELY and whites out the see-through region (a solid white
		// rectangle over what you're recording). makeWindowClickThrough adds
		// WS_EX_TRANSPARENT alone for the click-through, leaving transparency intact.
		BackgroundType:   application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		Windows: application.WindowsWindow{
			DisableFramelessWindowDecorations: true,
			HiddenOnTaskbar:                   true,
		},
	}
	w.recFrameWin = w.app.Window.NewWithOptions(opts)
	makeWindowClickThrough(w.recFrameWin)
}

// CloseRecordingFrame tears down the recorded-region border window if one is up.
func (w *WindowsService) CloseRecordingFrame() {
	if w.recFrameWin != nil {
		w.recFrameWin.Close()
		w.recFrameWin = nil
	}
}

// itoa is a tiny strconv.Itoa shim kept local so windows.go's imports stay lean.
func itoa(n int) string { return strconv.Itoa(n) }

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
