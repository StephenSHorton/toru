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
	"image"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/export"
	"github.com/StephenSHorton/toru/internal/history"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// OverlayService is the Wails-bound overlay/coordination API (JS: OverlayService.*).
type OverlayService struct {
	app *application.App
	cap capture.Capturer

	// recordingControlsOpener opens the floating recording pill (timer + Stop)
	// for a started recording (injected by main via SetRecordingControlsOpener).
	// It MUST be opened from Go: StartRecording hides the overlay windows
	// first, which leaves no live JS context owning the recording — a frontend
	// follow-up call after StartRecording is dead code. The recorded region's DIP
	// bounds + the fullscreen flag let the opener anchor the pill right at the crop
	// (region) instead of stranding it top-centre of another monitor.
	recordingControlsOpener func(handleID string, monitorID, regionX, regionY, regionW, regionH int, fullscreen bool)
	// recordingErrorOpener opens a small dismissible pill that surfaces a FAILED
	// StartRecording (injected by main). Without it a failed start would leave the
	// user staring at a dismissed overlay with no clue why nothing recorded — the
	// overlay is already hidden by the time ffmpeg reports it can't capture.
	recordingErrorOpener func(message string, monitorID int)
	// recordingFrameOpener opens the click-through "glowing border" window that
	// outlines the recorded region for the duration of a recording (injected by
	// main). It is given the region's DIP bounds; main expands them by the border
	// margin so the visible outline sits OUTSIDE the captured rect (never baked
	// into the video). recordingFrameCloser tears it down on stop.
	recordingFrameOpener func(dipX, dipY, dipW, dipH, monitorID int)
	recordingFrameCloser func()
	// audioConfigSetter replaces the recorder's audio-source selection
	// (injected by main via SetAudioConfigSetter; the bound SetAudioSources
	// calls through). Held as a func so the frozen Capturer seam stays
	// untouched.
	audioConfigSetter func(cfg capture.AudioConfig)
	// escArmer toggles the global Escape-to-cancel hook (injected by main via
	// SetEscapeArmer -> hotkey.Manager.ArmEscape). Armed only while the capture
	// overlay is up, so a global Escape cancels even when the transparent overlay
	// never received WebView2 keyboard focus (the in-page DOM Esc handler's blind
	// spot). Disarmed on hide / enter-edit / record / teardown.
	escArmer func(on bool)
	// editorOpener opens the standalone annotation editor window for a finished
	// screenshot PNG (injected by main via SetEditorOpener; = windowsSvc.OpenEditor).
	// A STRADDLE screenshot (crop spanning >1 monitor) cannot morph in place — no
	// single overlay window spans two monitors — so EnterEditMulti stitches the
	// region and hands the PNG to this opener instead of emitting overlay:edit.
	editorOpener func(imagePath string)
	// history is the recent-captures store (injected by main). On every successful
	// screenshot crop / recording stop we Add a durable copy under
	// %AppData%/toru/captures so the tray menu can re-open it. Optional: a nil
	// history is a no-op (tests / stubs).
	history *history.Store

	// suspendDismiss is set while a native Save dialog (or similar) is open so
	// WindowLostFocus does not Cancel the session mid-dialog. Alt-Tab to another
	// app still cancels once the dialog restores this to false and focus is gone.
	suspendDismiss atomic.Bool
	// inEdit is true only after a successful screenshot Capture morphs into the
	// annotation editor (EnterEdit / EnterEditLive / EnterEditMulti). Focus-loss
	// cancel is gated on this: Win+Shift+S engage steals/loses focus constantly
	// (Win key / freeze dance), so cancelling on ANY visible overlay made the
	// hotkey appear to "open then instantly close". Edit-only matches the intended
	// "Alt-Tab away from the editor" behaviour.
	inEdit atomic.Bool

	mu sync.RWMutex
	// windows are the REUSED overlay windows, keyed by monitorID. Created once
	// (Hidden), kept alive across captures, Hidden on Done/Cancel/Record, and
	// re-positioned + Shown on engage. Closed only by Teardown (app shutdown).
	windows map[int]*application.WebviewWindow
	// frozenImg holds the in-memory frozen pixels per monitor for the duration of
	// an ACTIVE session (one *image.RGBA each; ~33MB per 4K monitor). Cropped
	// LOSSLESSLY on commit/save; freed on HideOverlay and overwritten on engage.
	frozenImg map[int]*image.RGBA
	// jpegCache holds the pre-encoded fast dim-backdrop JPEG per monitor, served
	// from memory by ShotMiddleware. Freed on HideOverlay, overwritten on engage.
	jpegCache map[int][]byte
	// screens is the enumeration snapshot taken at BeginSession (ID == kbinani idx).
	screens []capture.ScreenInfo
	// gen is the engage generation; it cache-busts the reused webview's backdrop
	// <img> URL (/__shot/<id>?g=<gen>) so a re-engaged window never shows a stale
	// JPEG.
	gen int
	// pending holds the CURRENT engage's per-monitor sessions, keyed by monitorID,
	// for the duration between BeginSession and the window being shown. It is the
	// source a late-mounting overlay window pulls via RequestEngage (defense against
	// a missed overlay:engage broadcast), and what OverlayReady reads to know which
	// window to reveal. Cleared on HideOverlay/Teardown.
	pending map[int]MonitorSession
	// cropTemps are the served crop PNG temp files (toru-shot-*.png) produced by
	// EnterEdit/CommitScreenshot for THIS session. They are served at /__file/<base>
	// while the editor holds them as the base-image src; once the session ends they
	// are dead (Save-As writes its OWN file via ExportService). Removed + reset on
	// HideOverlay/Teardown so captures don't leak a small PNG each.
	cropTemps []string

	// pendingEditShow marks the overlay windows that EnterEditLive hid to grab a
	// clean live frame and must RE-SHOW once React acks the edit morph (EditReady).
	// The frozen path never hides its window, so its entries are never set and
	// EditReady is a harmless no-op there.
	pendingEditShow map[int]bool

	// freeze is the cached "freeze during capture" preference (mirrors the
	// persisted overlay.json field). freezeLoaded gates a one-time disk read so the
	// hot capture path never re-reads the file; SetFreezeOnCapture keeps both the
	// cache and disk in lockstep. Guarded by mu.
	freeze       bool
	freezeLoaded bool

	// sharedCrop is the latest cross-monitor selection (VIRTUAL-DESKTOP PHYSICAL px)
	// relayed by SetSharedCrop. The crop lives in the front end; Go only relays it
	// between the per-monitor windows (which can't message each other) and persists
	// it. Kept here as the last-known value for diagnostics. Guarded by mu.
	sharedCrop capture.Rect
}

// NewService wires the overlay to the shared capture seam.
func NewService(cap capture.Capturer) *OverlayService {
	return &OverlayService{
		cap:             cap,
		windows:         map[int]*application.WebviewWindow{},
		frozenImg:       map[int]*image.RGBA{},
		jpegCache:       map[int][]byte{},
		pending:         map[int]MonitorSession{},
		pendingEditShow: map[int]bool{},
	}
}

// SetApp injects the running app (called from main after application.New).
//
//wails:ignore
func (s *OverlayService) SetApp(app *application.App) { s.app = app }

// SetRecordingControlsOpener injects the recording-pill opener callback. Go-only.
//
//wails:ignore
func (s *OverlayService) SetRecordingControlsOpener(fn func(handleID string, monitorID, regionX, regionY, regionW, regionH int, fullscreen bool)) {
	s.recordingControlsOpener = fn
}

// SetRecordingErrorOpener injects the failed-start pill opener. Go-only.
//
//wails:ignore
func (s *OverlayService) SetRecordingErrorOpener(fn func(message string, monitorID int)) {
	s.recordingErrorOpener = fn
}

// SetRecordingFrameOpener / SetRecordingFrameCloser inject the recorded-region
// border window's open/close callbacks. Go-only.
//
//wails:ignore
func (s *OverlayService) SetRecordingFrameOpener(fn func(dipX, dipY, dipW, dipH, monitorID int)) {
	s.recordingFrameOpener = fn
}

//wails:ignore
func (s *OverlayService) SetRecordingFrameCloser(fn func()) {
	s.recordingFrameCloser = fn
}

// SetAudioConfigSetter injects the recorder's audio-source setter. Go-only.
//
//wails:ignore
func (s *OverlayService) SetAudioConfigSetter(fn func(cfg capture.AudioConfig)) {
	s.audioConfigSetter = fn
}

// SetEscapeArmer injects the global Escape-to-cancel toggle (hotkey.Manager.
// ArmEscape). Go-only.
//
//wails:ignore
func (s *OverlayService) SetEscapeArmer(fn func(on bool)) {
	s.escArmer = fn
}

// armEscape toggles the global Escape-to-cancel hook if an armer was injected.
// Nil-safe so tests / non-Windows wiring that skip the hotkey engine still work.
func (s *OverlayService) armEscape(on bool) {
	if s.escArmer != nil {
		s.escArmer(on)
	}
}

// SetEditorOpener injects the standalone screenshot-editor window opener (used by
// the STRADDLE capture path, which can't morph in place). Go-only.
//
//wails:ignore
func (s *OverlayService) SetEditorOpener(fn func(imagePath string)) {
	s.editorOpener = fn
}

// SetHistory injects the recent-captures store used by auto-copy + the tray
// "Recent" menu. Optional (tests leave it nil).
//
//wails:ignore
func (s *OverlayService) SetHistory(h *history.Store) {
	s.history = h
}

// SetSuspendDismiss freezes alt-tab/focus-loss cancellation (e.g. while a native
// Save dialog is open). Pair with false when the dialog returns.
//
//wails:ignore
func (s *OverlayService) SetSuspendDismiss(on bool) {
	s.suspendDismiss.Store(on)
}

// rememberScreenshot auto-copies the fresh crop PNG to the clipboard so the
// user can paste without pressing Copy. Library archival happens later when the
// user hits Done (annotated PNG via HistoryService.Add) — saving here would
// store the unannotated crop and race the final export.
// Best-effort: a clipboard failure never fails the capture.
func (s *OverlayService) rememberScreenshot(cropPath string) {
	if cropPath == "" {
		return
	}
	// Auto-copy: macOS-style — capture lands on the clipboard immediately.
	// The toolbar Copy button still re-exports after annotation.
	_ = export.CopyImageFile(cropPath)
}

// rememberRecording archives a finished recording for the tray Recent menu.
func (s *OverlayService) rememberRecording(videoPath string) {
	if videoPath == "" || s.history == nil {
		return
	}
	_, _ = s.history.Add(videoPath, history.KindVideo)
}

// GetFreezeOnCapture reports whether the screen is frozen during capture (the
// default) or shown live through a see-through overlay. Read by the Settings
// toggle and the in-overlay pill toggle. Reads the persisted preference once,
// then serves the cache.
func (s *OverlayService) GetFreezeOnCapture() bool {
	return s.currentFreeze()
}

// SetFreezeOnCapture persists the freeze preference and updates the in-memory
// cache so the NEXT BeginSession honours it. The overlay pill re-engages after
// calling this so the change is visible immediately; Settings just persists it.
func (s *OverlayService) SetFreezeOnCapture(enabled bool) {
	cropFileMu.Lock()
	st := loadCrops()
	st.Freeze = &enabled
	_ = saveCrops(st)
	cropFileMu.Unlock()

	s.mu.Lock()
	s.freeze = enabled
	s.freezeLoaded = true
	s.mu.Unlock()
}

// currentFreeze returns the cached freeze preference, lazily loading it from disk
// on first use (under cropFileMu, like every other overlay.json access).
func (s *OverlayService) currentFreeze() bool {
	s.mu.RLock()
	if s.freezeLoaded {
		v := s.freeze
		s.mu.RUnlock()
		return v
	}
	s.mu.RUnlock()

	cropFileMu.Lock()
	v := loadCrops().freezeEnabled()
	cropFileMu.Unlock()

	s.mu.Lock()
	s.freeze = v
	s.freezeLoaded = true
	s.mu.Unlock()
	return v
}

// SetAudioSources is the USER OPT-IN for audio capture. EVERY source —
// system mix, individual applications, microphone — must be explicitly
// enabled here by the user (the overlay's Audio picker); the zero config
// records no audio. Applies to future recordings.
func (s *OverlayService) SetAudioSources(cfg capture.AudioConfig) {
	if s.audioConfigSetter != nil {
		s.audioConfigSetter(cfg)
	}
}

// ListAudioSessions returns the applications currently producing audio — the
// rows of the Audio picker's per-app section.
func (s *OverlayService) ListAudioSessions() []capture.AudioSession {
	return capture.EnumAudioSessions()
}

// ListMicrophones returns the system's microphone device names for the Audio
// picker's mic section.
func (s *OverlayService) ListMicrophones() []string {
	return capture.ListMicrophones()
}

// ListWindows returns visible top-level app windows for the "capture a window"
// mode. Rects are virtual-desktop physical px so the front end can snap the crop
// and reuse the existing region capture path for stills + video.
func (s *OverlayService) ListWindows() []capture.WindowInfo {
	list := capture.ListTopLevelWindows()
	if list == nil {
		return []capture.WindowInfo{}
	}
	return list
}

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
		// Enrich scale + primary from the Wails screen layout. Match by
		// rectangle OVERLAP, not exact-origin equality: kbinani (EnumDisplay
		// bounds) and Wails (MONITORINFOEX.RcMonitor) can disagree on a
		// secondary monitor's origin by a pixel in a mixed-DPI layout, and an
		// exact-equality miss silently leaves ScaleFactor=1.0 and the wrong
		// IsPrimary — corrupting every crop on that monitor.
		if scr := s.matchWailsScreen(d.X, d.Y, d.W, d.H); scr != nil {
			if scr.ScaleFactor > 0 {
				info.ScaleFactor = float64(scr.ScaleFactor)
			}
			info.IsPrimary = scr.IsPrimary
		} else if s.app != nil && len(s.app.Screen.GetAll()) > 0 {
			// We had screens to match against but found no overlap. Do NOT
			// silently default scale to 1.0 / origin-based primary — log loudly.
			s.warnf("overlay: no Wails screen overlaps display %d (%d,%d %dx%d); scale defaulting to 1.0", d.Index, d.X, d.Y, d.W, d.H)
		}
		out = append(out, info)
	}
	return out, nil
}

// matchWailsScreen returns the Wails Screen whose PhysicalBounds best matches the
// kbinani display rect (x,y,w,h) by maximum rectangle overlap, falling back to
// the screen whose physical center contains the kbinani center. It returns nil
// when no Wails screen overlaps/contains the rect (or the cache is empty, e.g.
// before app.Run() populates it). This is robust to the 1px origin disagreements
// between EnumDisplay bounds and MONITORINFOEX.RcMonitor in mixed-DPI layouts.
func (s *OverlayService) matchWailsScreen(x, y, w, h int) *application.Screen {
	if s.app == nil {
		return nil
	}
	want := application.Rect{X: x, Y: y, Width: w, Height: h}
	var best *application.Screen
	bestOverlap := 0
	for _, scr := range s.app.Screen.GetAll() {
		ov := want.Intersect(scr.PhysicalBounds)
		area := ov.Width * ov.Height
		if area > bestOverlap {
			bestOverlap = area
			best = scr
		}
	}
	if best != nil {
		return best
	}
	// No overlap (rare): fall back to center-containment.
	cx, cy := x+w/2, y+h/2
	for _, scr := range s.app.Screen.GetAll() {
		b := scr.PhysicalBounds
		if cx >= b.X && cx < b.X+b.Width && cy >= b.Y && cy < b.Y+b.Height {
			return scr
		}
	}
	return nil
}

// warnf logs a warning via the Wails app logger when available (best-effort).
func (s *OverlayService) warnf(format string, args ...any) {
	if s.app != nil && s.app.Logger != nil {
		s.app.Logger.Warn(fmt.Sprintf(format, args...))
	}
}

// BeginSession is THE engage entrypoint (Win+Shift+S / tray / Settings Capture).
// In overlay-v2 it REUSES the per-monitor windows (created once, kept hidden):
//
//	(1) ensureWindows — create any missing window Hidden; Hide() any still-visible,
//	    and (only if one WAS visible, e.g. New-from-edit) settle one DWM frame so the
//	    fading overlay can't be baked into the next freeze.
//	(2) freezeAll — concurrent in-memory freeze + JPEG backdrop, while HIDDEN.
//	(3) install the fresh frozenImg/jpegCache maps (dropping the previous refs).
//	(4) per monitor (STILL hidden): SetBounds, publish the session to s.pending,
//	    then Emit(overlay:engage) so the reused React window swaps its backdrop <img>
//	    to the fresh ?g= URL.
//
// It DOES NOT Show the windows here. Showing is gated on a JS ACK: the React
// overlay:engage (or RequestEngage-pull) handler, AFTER it has set the session and
// the new backdrop <img> has DECODED, calls OverlayReady(monitorID), which Shows
// THAT window. Firing-and-hoping (Emit-then-Show) is racy — app.Event.Emit
// dispatches to windows on a NEWLY SPAWNED goroutine and returns immediately, so a
// Go-side Show could win the main-thread queue and reveal the PRIOR session's DOM
// (stale backdrop, or a whole stale annotation editor) before React repaints. The
// ACK closes that race AND the "first capture before WebView2 finished loading"
// blank-overlay window (a window that hasn't mounted simply never ACKs until it
// has, and RequestEngage lets a late mount pull the session it missed).
//
// Returning []MonitorSession also lets the binding generator emit the
// MonitorSession TS type.
func (s *OverlayService) BeginSession() ([]MonitorSession, error) {
	// Guard the launch-path DPI hazard: Wails only populates the Screen cache
	// inside app.Run() (newPlatformApp). If BeginSession runs before that, every
	// monitor's ScaleFactor/IsPrimary and DIP bounds fall back to scale=1.0 and
	// the overlay is mis-sized on HiDPI. main wires this on ApplicationStarted to
	// avoid it; log loudly if that invariant ever regresses.
	if s.app != nil && len(s.app.Screen.GetAll()) == 0 {
		s.warnf("overlay: BeginSession ran with an EMPTY Wails screen cache — DPI scale/primary will be wrong (open the overlay on ApplicationStarted, not before app.Run())")
	}

	screens, err := s.ListScreens()
	if err != nil {
		return nil, err
	}
	if len(screens) == 0 {
		return nil, fmt.Errorf("overlay: no active displays")
	}

	freeze := s.currentFreeze()

	s.mu.Lock()
	s.screens = screens
	s.gen++
	gen := s.gen
	s.mu.Unlock()

	// (1) Reuse-or-create windows, hiding any still-visible one BEFORE the freeze.
	// settleDWM is true iff at least one window was actually visible (e.g. New from
	// edit mode) — only then must we wait a frame for the compositor to drop the
	// overlay's pixels before grabbing the live desktop.
	settleDWM := s.ensureWindows(screens)

	// (1b) On the New-from-edit path the windows were VISIBLE microseconds ago;
	// ShowWindow(SW_HIDE) returns synchronously but DWM composition is async, so a
	// freeze fired immediately could photograph the still-fading overlay. Wait one+
	// DWM frame. Skipped on the cold/idle paths (windows hidden long before) so
	// instant re-engage from the tray stays instant.
	if settleDWM {
		settleCompositor()
	}

	// (2) Freeze every monitor in memory + pre-encode its backdrop JPEG, concurrently
	// and while every overlay window is hidden, so no still photographs an overlay.
	// FREEZE-OFF: skip entirely — the (transparent) overlay shows the LIVE desktop
	// during selection, and a screenshot grabs live pixels at Capture time
	// (EnterEditLive) instead of cropping a pre-frozen still.
	var frozen map[int]*image.RGBA
	var jpegs map[int][]byte
	if freeze {
		frozen, jpegs, err = s.freezeAll(screens)
		if err != nil {
			return nil, err
		}
	} else {
		frozen = map[int]*image.RGBA{}
		jpegs = map[int][]byte{}
	}

	// (3) Swap in the fresh maps; drop the previous frozen refs so at most one set
	// is ever resident (never accumulating per engage).
	s.mu.Lock()
	s.dropImagesLocked()
	s.frozenImg = frozen
	s.jpegCache = jpegs
	s.mu.Unlock()

	// (4) Build payloads. Freeze-on: StillURL carries ?g=<gen> to cache-bust the
	// reused webview's backdrop. Freeze-off: StillURL is empty (no backdrop) and
	// Freeze=false tells React to render the see-through live overlay.
	sessions := s.buildSessionPayloads(screens, gen, freeze)

	// (5) Publish the sessions (so a late-mounting window can RequestEngage-pull the
	// one it missed), SetBounds each STILL-HIDDEN window, then broadcast
	// overlay:engage. Show is deferred to OverlayReady (the JS ACK) — see the doc.
	s.mu.Lock()
	s.pending = make(map[int]MonitorSession, len(sessions))
	for _, mon := range sessions {
		s.pending[mon.MonitorID] = mon
	}
	s.mu.Unlock()

	for _, mon := range sessions {
		win := s.window(mon.MonitorID)
		if win == nil {
			continue
		}
		dip := s.dipBoundsFor(mon)
		win.SetBounds(application.Rect{X: dip.X, Y: dip.Y, Width: dip.Width, Height: dip.Height})
		s.emit(EventOverlayEngage, mon) // broadcast; React filters by ?mon=
	}

	// Capture mode is now live: arm the global Escape-to-cancel hook so a press
	// cancels even if the transparent overlay never took WebView2 keyboard focus.
	// Disarmed again on hide / enter-edit / record / teardown.
	s.armEscape(true)

	return sessions, nil
}

// OverlayReady is the JS ACK that gates Show. React calls it from the
// overlay:engage / RequestEngage handler AFTER it has applied the fresh session
// (capture mode) AND the new backdrop <img> has DECODED, so revealing the window
// can never flash the prior session's DOM (stale backdrop or stale editor). It
// Shows the window for monitorID (and Focuses it if primary), then drops that
// monitor's pending entry. Idempotent: a duplicate ACK after the pending entry is
// gone is a no-op.
func (s *OverlayService) OverlayReady(monitorID int) {
	s.mu.Lock()
	mon, ok := s.pending[monitorID]
	if ok {
		delete(s.pending, monitorID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if win := s.window(monitorID); win != nil {
		win.Show()
		if mon.IsPrimary {
			win.Focus()
		}
	}
}

// EditReady is the JS ACK for the FREEZE-OFF screenshot morph. EnterEditLive hid
// the overlay window to grab a clean live frame; React calls this from the
// overlay:edit handler AFTER the served crop PNG has DECODED, so the window is
// re-shown already painted as the editor (never flashing the bare live overlay or
// an empty stage). The frozen path leaves no pendingEditShow entry, so React's
// call there is a harmless no-op (that window was never hidden).
func (s *OverlayService) EditReady(monitorID int) {
	s.mu.Lock()
	show := s.pendingEditShow[monitorID]
	if show {
		delete(s.pendingEditShow, monitorID)
	}
	s.mu.Unlock()
	if !show {
		return
	}
	if win := s.window(monitorID); win != nil {
		win.Show()
		win.Focus()
	}
}

// RequestEngage is the defense-in-depth pull: a freshly-mounted overlay window
// (e.g. the FIRST capture, where WebView2 finished navigating only AFTER
// BeginSession broadcast overlay:engage) calls this to fetch the CURRENT engage's
// session for its monitor instead of waiting for a broadcast it may have missed.
// Returns nil when there is no active engage for that monitor (already shown, or
// idle). The React mount applies the returned session exactly like overlay:engage,
// then ACKs via OverlayReady once the backdrop decodes.
func (s *OverlayService) RequestEngage(monitorID int) *MonitorSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if mon, ok := s.pending[monitorID]; ok {
		m := mon
		return &m
	}
	return nil
}

// PrewarmWindows creates the per-monitor overlay WebviewWindow objects at launch
// (called from main on ApplicationStarted, after OpenSettings) so the FIRST
// capture has its window handles ready.
//
// DELIBERATELY ensureWindows-only: it creates the window objects (impl stays nil,
// no navigation, no paint) but does NOT Show/Hide them. A Show/Hide pre-warm WOULD
// force webview navigation up-front, but at the cost of a black-window
// micro-flicker at launch. The honest tradeoff: the FIRST real engage pays the
// one-time navigation cost; every subsequent RE-engage (the primary goal) is then
// instant. We choose no launch flicker.
//
//wails:ignore
func (s *OverlayService) PrewarmWindows() {
	if s.app != nil && len(s.app.Screen.GetAll()) == 0 {
		s.warnf("overlay: PrewarmWindows ran with an EMPTY Wails screen cache — call it on ApplicationStarted, not before app.Run()")
	}
	screens, err := s.ListScreens()
	if err != nil || len(screens) == 0 {
		return
	}
	s.ensureWindows(screens)
}

// window returns the reused window for monitorID (nil if none), under the lock.
func (s *OverlayService) window(monitorID int) *application.WebviewWindow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.windows[monitorID]
}

// ShotMiddleware serves two families of bytes the webview cannot otherwise fetch
// (a webview <img>/<video> can't load a raw C:\ path):
//
//   - /__shot/<monitorID>?g=<gen> : the session dim BACKDROP, the pre-encoded fast
//     JPEG held IN MEMORY in s.jpegCache (overlay-v2 — no disk round-trip). The
//     ?g= cache-buster forces a reused webview to refetch the fresh backdrop.
//   - /__file/<basename> : a committed artifact living under EITHER %TEMP%/toru
//     (session crop / live recording) OR %AppData%/toru/captures (tray Recent
//     history). Served by basename only — filepath.Base strips any path traversal
//     and the lookup is confined to those two dirs — so this cannot read arbitrary
//     disk files.
//
// Both respond with Cache-Control: no-store so a re-opened session never serves a
// stale image. Registered in main via AssetOptions.Middleware. Returns an http
// middleware — nonsensical over the wire, so keep it Go-only.
//
//wails:ignore
func (s *OverlayService) ShotMiddleware() application.Middleware {
	const shotPrefix = "/__shot/"
	const filePrefix = "/__file/"
	toruTmp := filepath.Join(os.TempDir(), "toru")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasPrefix(r.URL.Path, shotPrefix):
				// The path tail is the monitor ID; the ?g= cache-buster lives in
				// r.URL.RawQuery and is excluded from r.URL.Path, so TrimPrefix
				// yields a bare integer.
				mid, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, shotPrefix))
				if err != nil {
					http.NotFound(w, r)
					return
				}
				// RLock only to copy the []byte slice header out; release BEFORE
				// w.Write so the engage goroutine is never blocked on the response.
				s.mu.RLock()
				jpg := s.jpegCache[mid]
				s.mu.RUnlock()
				if jpg == nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "image/jpeg")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Length", strconv.Itoa(len(jpg)))
				_, _ = w.Write(jpg)
				return
			case strings.HasPrefix(r.URL.Path, filePrefix):
				// filepath.Base collapses any ../ so only a leaf name survives;
				// joining it onto the allow-listed dirs confines the read there.
				base := filepath.Base(strings.TrimPrefix(r.URL.Path, filePrefix))
				// Prefer the session temp (hot path for live edit), then the live
				// library folder (user-configurable via Settings).
				var capturesDir string
				if s.history != nil {
					capturesDir = s.history.Dir()
				}
				for _, dir := range []string{toruTmp, capturesDir} {
					if dir == "" || base == "" || base == "." {
						continue
					}
					full := filepath.Join(dir, base)
					if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
						w.Header().Set("Cache-Control", "no-store")
						http.ServeFile(w, r, full) // content-type inferred from extension
						return
					}
				}
				http.NotFound(w, r)
				return
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}

// HideOverlay is the NORMAL idle path (Done / Cancel / Record). It HIDES every
// visible overlay window — keeping the windows ALIVE for the next instant engage —
// and frees the in-memory frozen pixels + backdrop JPEGs (~100MB). It does NOT
// Close() the windows; Teardown does that, only at app shutdown.
func (s *OverlayService) HideOverlay() {
	s.armEscape(false) // overlay is going away — stop intercepting global Escape
	s.inEdit.Store(false) // focus-loss cancel only applies in annotation edit mode

	s.mu.RLock()
	wins := make([]*application.WebviewWindow, 0, len(s.windows))
	for _, w := range s.windows {
		wins = append(wins, w)
	}
	s.mu.RUnlock()

	for _, w := range wins {
		if w != nil && w.IsVisible() {
			w.Hide()
		}
	}

	s.mu.Lock()
	s.dropImagesLocked()
	s.jpegCache = map[int][]byte{}
	s.pending = map[int]MonitorSession{}
	s.pendingEditShow = map[int]bool{}
	temps := s.takeCropTempsLocked()
	s.mu.Unlock()

	removeFiles(temps)
	s.emit(EventOverlayDismiss, nil)
}

// dropImagesLocked drops every frozen-image reference so the RGBA buffers become
// GC-eligible. Caller MUST hold s.mu (write lock).
func (s *OverlayService) dropImagesLocked() { s.frozenImg = map[int]*image.RGBA{} }

// trackCropTemp records a served crop PNG temp path so HideOverlay/Teardown can
// remove it when the session ends (it is dead once the editor closes).
func (s *OverlayService) trackCropTemp(path string) {
	if path == "" {
		return
	}
	s.mu.Lock()
	s.cropTemps = append(s.cropTemps, path)
	s.mu.Unlock()
}

// takeCropTempsLocked returns and clears the tracked crop temp paths. Caller MUST
// hold s.mu (write lock); the os.Remove is done OUTSIDE the lock by the caller.
func (s *OverlayService) takeCropTempsLocked() []string {
	temps := s.cropTemps
	s.cropTemps = nil
	return temps
}

// Teardown CLOSES every overlay window and clears all session state. Wired only on
// app shutdown (process exit frees everything regardless, so this is best-effort
// cosmetic). Go-only — never over the wire.
//
//wails:ignore
func (s *OverlayService) Teardown() {
	s.armEscape(false) // app shutting down — never leave the Escape hook armed

	s.mu.Lock()
	wins := s.windows
	s.windows = map[int]*application.WebviewWindow{}
	s.dropImagesLocked()
	s.jpegCache = map[int][]byte{}
	s.pending = map[int]MonitorSession{}
	s.pendingEditShow = map[int]bool{}
	temps := s.takeCropTempsLocked()
	s.screens = nil
	s.mu.Unlock()

	removeFiles(temps)
	for _, w := range wins {
		if w != nil {
			w.Close()
		}
	}
}

// removeFiles best-effort deletes each path (the served crop temps). Errors are
// ignored: the OS reclaims %TEMP% regardless and a missing file is already gone.
func removeFiles(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// EnterEdit crops the in-memory FROZEN pixels for monitorID to a LOSSLESS PNG,
// dismisses the capture overlay, and opens the standalone annotation editor
// window (same destination as EnterEditMulti). cssLeft/Top/W/H are kept for
// binding compatibility with the React capture path but are unused — the editor
// is a separate centered window, not an in-overlay morph.
func (s *OverlayService) EnterEdit(monitorID int, sub capture.Rect, cssLeft, cssTop, cssW, cssH int) error {
	_, _, _, _ = cssLeft, cssTop, cssW, cssH
	// Leaving capture: disarm global Escape (overlay Cancel) before dismiss/open.
	s.armEscape(false)

	s.mu.RLock()
	img := s.frozenImg[monitorID]
	s.mu.RUnlock()
	if img == nil {
		return fmt.Errorf("overlay: no frozen image for monitor %d (session not active?)", monitorID)
	}

	_ = s.SaveCrop(monitorID, sub)

	// The frozen RGBA is immutable after the freeze, so cropping it lock-free after
	// grabbing the pointer is safe — nothing mutates an already-frozen image.
	cropPath, err := capture.CropImage(img, sub)
	if err != nil {
		return err
	}
	// Do NOT trackCropTemp: HideOverlay would delete the file the editor needs.
	// The editor window owns temp lifecycle (isToruTempPath on close).
	s.rememberScreenshot(cropPath)
	// Dismiss overlay BEFORE opening the editor so AOT capture windows can't
	// obscure the (not-AOT) editor — same as EnterEditMulti.
	s.HideOverlay()
	if s.editorOpener != nil {
		s.editorOpener(cropPath)
	} else {
		_ = os.Remove(cropPath)
	}
	return nil
}

// EnterEditLive is the FREEZE-OFF screenshot Capture: there is no pre-frozen
// still, so the live pixels must be grabbed NOW. It (1) HIDES the TARGET monitor's
// overlay window so the grab can't photograph dim panels / crop chrome, (2)
// settles one DWM frame, (3) captures the live monitor, (4) crops to a PNG,
// (5) HideOverlay + opens the standalone annotation editor.
//
// INVARIANT: if it hid the target window, error paths MUST re-show it (or the
// overlay is stranded hidden). Success uses HideOverlay (all monitors) then opens
// the editor — no in-overlay morph / EditReady path.
//
// cssLeft/Top/W/H are unused (binding-compat); the editor is a separate window.
func (s *OverlayService) EnterEditLive(monitorID int, sub capture.Rect, cssLeft, cssTop, cssW, cssH int) error {
	_, _, _, _ = cssLeft, cssTop, cssW, cssH
	s.armEscape(false)
	// Suspend focus-loss cancel while we Hide for a clean live grab.
	s.SetSuspendDismiss(true)
	defer s.SetSuspendDismiss(false)

	s.mu.RLock()
	var sc capture.ScreenInfo
	found := false
	for _, x := range s.screens {
		if x.ID == monitorID {
			sc = x
			found = true
			break
		}
	}
	gen := s.gen // supersede-guard: a fresh engage bumps gen
	win := s.windows[monitorID]
	s.mu.RUnlock()
	if !found {
		return fmt.Errorf("overlay: no screen %d in session (live capture)", monitorID)
	}

	targetWasVisible := win != nil && win.IsVisible()
	if targetWasVisible {
		win.Hide()
	}
	settleCompositor()

	reShow := func() {
		if targetWasVisible {
			if w := s.window(monitorID); w != nil {
				w.Show()
				w.Focus()
			}
		}
	}

	img, err := capture.FreezeMonitorImage(image.Rect(sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H))
	if err != nil {
		reShow()
		return err
	}

	cropPath, err := capture.CropImage(img, sub)
	if err != nil {
		reShow()
		return err
	}

	// Supersede: a concurrent BeginSession owns the session now — drop our crop.
	if s.superseded(gen) {
		_ = os.Remove(cropPath)
		return nil
	}

	_ = s.SaveCrop(monitorID, sub)
	// Untracked temp — editor window owns delete-on-close.
	s.rememberScreenshot(cropPath)
	s.HideOverlay()
	if s.editorOpener != nil {
		s.editorOpener(cropPath)
	} else {
		_ = os.Remove(cropPath)
	}
	return nil
}

// SetSharedCrop relays the cross-monitor selection (VIRTUAL-DESKTOP PHYSICAL px)
// to EVERY overlay window so each re-renders its slice of the one shared crop.
// The per-monitor windows can't message each other directly, so the window that
// owns an in-progress drag calls this (rAF-throttled) and Go broadcasts
// overlay:cropRect; the other windows apply it. It also stashes the value for
// diagnostics. High-frequency + fire-and-forget: no disk, no return.
func (s *OverlayService) SetSharedCrop(region capture.Rect) {
	s.mu.Lock()
	s.sharedCrop = region
	s.mu.Unlock()
	s.emit(EventOverlayCropRect, region)
}

// SaveSharedCrop persists the shared crop (VIRTUAL-DESKTOP PHYSICAL px) so the
// next session reopens where the user left it. Called debounced on drag/resize end
// (and by EnterEditMulti before it dismisses). Mirrors SaveCrop's file discipline.
func (s *OverlayService) SaveSharedCrop(region capture.Rect) error {
	cropFileMu.Lock()
	defer cropFileMu.Unlock()
	st := loadCrops()
	r := region
	st.Region = &r
	return saveCrops(st)
}

// EnterEditMulti is the STRADDLE screenshot Capture: the crop spans two or more
// monitors, so it can't morph in place (no overlay window spans the seam). It
// stitches the region out of the per-monitor pixels into ONE PNG, opens the
// standalone annotation editor window for it, and dismisses the overlay.
//
// It honours the freeze preference: freeze-ON crops the already-frozen images;
// freeze-OFF grabs each touched monitor LIVE right now (hiding those windows first
// so none photographs its own dim/crop chrome, exactly like EnterEditLive does for
// one monitor). region is VIRTUAL-DESKTOP PHYSICAL px.
func (s *OverlayService) EnterEditMulti(region capture.Rect) error {
	if region.W <= 0 || region.H <= 0 {
		return fmt.Errorf("overlay: empty straddle rect %+v", region)
	}

	// Leaving capture for the standalone editor: disarm the global Escape hook so a
	// stray Esc can't fire Cancel mid-stitch (HideOverlay also disarms on the success
	// path, but the early-return/grab-error paths return before reaching it).
	s.armEscape(false)
	// Suspend focus-loss cancel across the hide-for-grab / HideOverlay dance so we
	// don't treat the intermediate focus loss as an Alt-Tab cancel.
	s.SetSuspendDismiss(true)
	defer s.SetSuspendDismiss(false)

	s.mu.RLock()
	screens := append([]capture.ScreenInfo(nil), s.screens...)
	gen := s.gen
	s.mu.RUnlock()

	hit := intersectingScreens(screens, region)
	if len(hit) == 0 {
		return fmt.Errorf("overlay: straddle rect %+v intersects no monitor", region)
	}

	freeze := s.currentFreeze()

	// hidden holds the windows the freeze-OFF grab hides (empty for freeze-ON). It is
	// function-scoped so EVERY error return below can reshowWindows it — never strand
	// the overlay hidden with the capture lost (the EnterEditLive invariant). On a
	// SUPERSEDE we deliberately DON'T reshow: a fresh BeginSession bumped gen and will
	// re-show its own windows (ack-gated), so reshowing here would only flash stale DOM.
	hidden := map[int]*application.WebviewWindow{}

	var frozens map[int]*image.RGBA
	if freeze {
		// Crop the already-frozen pixels. Copy the map under the lock; the images
		// themselves are immutable after the freeze, so stitching lock-free is safe.
		s.mu.RLock()
		frozens = make(map[int]*image.RGBA, len(s.frozenImg))
		for k, v := range s.frozenImg {
			frozens[k] = v
		}
		s.mu.RUnlock()
		if len(frozens) == 0 {
			return fmt.Errorf("overlay: no frozen images for straddle capture (session not active?)")
		}
	} else {
		// FREEZE-OFF: grab each touched monitor live. Hide every hit window first so
		// none bakes its own see-through chrome into the grab, then settle one DWM
		// frame (mirrors EnterEditLive's single-monitor dance).
		s.mu.RLock()
		hitWins := make(map[int]*application.WebviewWindow, len(hit))
		for _, sc := range hit {
			hitWins[sc.ID] = s.windows[sc.ID]
		}
		s.mu.RUnlock()

		for id, w := range hitWins {
			if w != nil && w.IsVisible() {
				w.Hide()
				hidden[id] = w
			}
		}
		settleCompositor()

		// Supersede guard: a concurrent BeginSession (hotkey/tray, not gated by the
		// React busy flag) bumped gen and now owns the windows. Abandon ours; the new
		// session re-shows its windows itself (no reshow here — see `hidden` doc).
		if s.superseded(gen) {
			return nil
		}

		frozens = make(map[int]*image.RGBA, len(hit))
		for _, sc := range hit {
			img, err := capture.FreezeMonitorImage(image.Rect(sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H))
			if err != nil {
				reshowWindows(hidden) // genuine error: restore the overlay so the user can retry
				return err
			}
			frozens[sc.ID] = img
		}
	}

	path, err := capture.CropImageMulti(frozens, hit, region)
	if err != nil {
		reshowWindows(hidden) // genuine error: restore the overlay (no-op for freeze-ON)
		return err
	}

	// Final supersede re-check before the DESTRUCTIVE tail: stitching + opening the
	// editor takes long enough for a fresh BeginSession to land. If it did, drop our
	// stitched PNG and bail WITHOUT HideOverlay — otherwise we'd tear down the brand-new
	// session's windows + frozen pixels. (freeze-ON had NO guard before this fix.)
	if s.superseded(gen) {
		_ = os.Remove(path)
		return nil
	}

	_ = s.SaveSharedCrop(region)
	s.rememberScreenshot(path)
	// Dismiss the overlay BEFORE opening the editor so the always-on-top overlay
	// windows can't briefly obscure the (not-always-on-top) editor window. HideOverlay
	// frees the frozen pixels and hides every still-visible overlay window; `path` is
	// untracked, so it is NOT deleted here (the editor window owns its lifecycle).
	s.HideOverlay()
	if s.editorOpener != nil {
		s.editorOpener(path)
	} else {
		_ = os.Remove(path) // no editor to hand it to: don't leak the temp
	}
	return nil
}

// superseded reports whether a fresh BeginSession has bumped the engage generation
// past gen (i.e. this in-flight capture no longer owns the session).
func (s *OverlayService) superseded(gen int) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.gen != gen
}

// reshowWindows re-Shows + Focuses each window — the error/supersede recovery for
// the freeze-OFF straddle grab, which hides windows before grabbing.
func reshowWindows(wins map[int]*application.WebviewWindow) {
	for _, w := range wins {
		if w != nil {
			w.Show()
			w.Focus()
		}
	}
}

// intersectingScreens returns the monitors whose virtual rect overlaps region
// (positive area), in the screens slice's order. These are the monitors a straddle
// capture must stitch / grab.
func intersectingScreens(screens []capture.ScreenInfo, region capture.Rect) []capture.ScreenInfo {
	out := make([]capture.ScreenInfo, 0, len(screens))
	for _, sc := range screens {
		if intersectArea(region, sc) > 0 {
			out = append(out, sc)
		}
	}
	return out
}

// Finish is the explicit edit-mode "Done" (hide to tray) with NO cancel
// semantics: it hides the overlay (keeping windows alive) WITHOUT firing
// capture:cancelled (which other code may treat as a real cancel).
func (s *OverlayService) Finish() error {
	s.HideOverlay()
	return nil
}

// Commit is a thin compatibility shim kept so existing bindings/tests don't break.
// For screenshots it crops the in-memory frozen pixels (NEVER a live re-capture)
// using req.Rect as the contract Rect and deriving the monitor-local sub-rect from
// the owning screen's origin, returning the cropped PNG path WITHOUT dismissing or
// opening anything. The single-surface React path calls EnterEdit, not this. Video
// is delegated to StartRecording (the overlay records the live region).
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

// CommitScreenshot crops the in-memory FROZEN pixels for monitorID (NEVER a live
// re-capture) to a LOSSLESS PNG, emits capture:done, and returns the result. In
// overlay-v2 it does NOT dismiss the overlay and does NOT open any editor window —
// the React screenshot path uses EnterEdit (single-surface morph) instead. This is
// retained for the Commit() shim / tests / dev. rect is the contract Rect
// (virtual-desktop physical px); sub is the monitor-local physical crop region.
func (s *OverlayService) CommitScreenshot(monitorID int, rect capture.Rect, sub capture.Rect, copyOnCommit bool) (capture.CaptureResult, error) {
	s.mu.RLock()
	img := s.frozenImg[monitorID]
	s.mu.RUnlock()
	if img == nil {
		return capture.CaptureResult{}, fmt.Errorf("overlay: no frozen image for monitor %d (session not active?)", monitorID)
	}

	// Persist the crop (monitor-local physical px).
	_ = s.SaveCrop(monitorID, sub)

	out, err := capture.CropImage(img, sub)
	if err != nil {
		return capture.CaptureResult{}, err
	}
	s.trackCropTemp(out)

	res := capture.CaptureResult{
		Mode:      capture.ModeScreenshot,
		ImagePath: out,
		Rect:      rect,
		MonitorID: monitorID,
	}
	s.emit(EventCaptureDone, res)
	return res, nil
}

// Cancel hides the overlay (keeping windows alive) and returns the user to idle
// (the tray), emitting capture:cancelled. Toru lives in the tray; no window is
// opened on cancel.
func (s *OverlayService) Cancel() error {
	s.HideOverlay()
	s.emit(EventCaptureCancelled, nil)
	return nil
}

// StartRecording hides the overlay FIRST (so ffmpeg records the live region, not
// the dim overlays) while KEEPING the windows alive, THEN begins recording, THEN
// opens the recording pill (timer + Stop). req.Rect is the virtual-desktop
// physical Rect the front end emits.
//
// The pill is opened HERE, not by the calling frontend: the overlay surface is
// hidden the moment recording starts, so no live JS context owns the recording —
// a frontend follow-up call after this await would be dead code, leaving the
// recording unstoppable.
//
// On SUCCESS it also opens the click-through "glowing border" window that outlines
// the recorded region (recordingFrameOpener), torn down by StopRecording. On
// FAILURE it opens a dismissible error pill (recordingErrorOpener) — the overlay
// is already hidden, so without it a failed start leaves a blank screen with no
// explanation.
func (s *OverlayService) StartRecording(req capture.CaptureRequest) (string, error) {
	s.HideOverlay()
	handle, err := s.cap.StartRecording(req)
	if err != nil {
		if s.recordingErrorOpener != nil {
			s.recordingErrorOpener(recordingErrorMessage(err), req.MonitorID)
		}
		return "", err
	}
	fullscreen := req.Sub == capture.SubFullscreen
	rx, ry, rw, rh, regionOK := s.regionDIP(req.Rect, req.MonitorID)
	// Border outline around the recorded region (passed as the EXACT region DIP
	// bounds; the opener expands by the border margin so the visible outline sits
	// OUTSIDE the captured rect and never bakes into the video). SKIPPED for a
	// fullscreen recording: a border there would either fall off the screen edge
	// (its margin band is off-monitor) or, if drawn inward, land inside the captured
	// pixels — there is no out-of-frame band on a whole-monitor grab. The pill is
	// the indicator for fullscreen.
	if s.recordingFrameOpener != nil && !fullscreen && regionOK {
		s.recordingFrameOpener(rx, ry, rw, rh, req.MonitorID)
	}
	if s.recordingControlsOpener != nil {
		// Anchor the pill at the crop for region recordings; a region we couldn't
		// resolve to DIP bounds is treated like fullscreen so the opener uses its
		// safe top-centre fallback instead of bogus coordinates.
		s.recordingControlsOpener(handle, req.MonitorID, rx, ry, rw, rh, fullscreen || !regionOK)
	}
	return handle, nil
}

// recordingErrorMessage condenses a StartRecording failure (often a multi-line
// ffmpeg stderr tail) into a single short line that fits the error pill's window
// URL. The full error still propagates as the method's return value.
func recordingErrorMessage(err error) string {
	msg := strings.Join(strings.Fields(err.Error()), " ")
	const max = 280
	if len(msg) > max {
		msg = msg[:max] + "…"
	}
	return msg
}

// regionDIP converts a recorded region (virtual-desktop PHYSICAL px) to DIP bounds
// for the indicator window: it finds the owning screen in the session snapshot,
// maps the monitor-local physical offset through the monitor's scale, and anchors
// it to the Wails screen's DIP origin. ok=false when the monitor isn't in the
// current session.
func (s *OverlayService) regionDIP(rect capture.Rect, monitorID int) (x, y, w, h int, ok bool) {
	s.mu.RLock()
	var sc capture.ScreenInfo
	found := false
	for _, scr := range s.screens {
		if scr.ID == monitorID {
			sc = scr
			found = true
			break
		}
	}
	s.mu.RUnlock()
	if !found {
		return 0, 0, 0, 0, false
	}
	scale := sc.ScaleFactor
	if scale <= 0 {
		scale = 1
	}
	dipOriginX, dipOriginY := rndDiv(sc.X, scale), rndDiv(sc.Y, scale)
	if scr := s.matchWailsScreen(sc.X, sc.Y, sc.W, sc.H); scr != nil {
		dipOriginX, dipOriginY = scr.Bounds.X, scr.Bounds.Y
	}
	x = dipOriginX + rndDiv(rect.X-sc.X, scale)
	y = dipOriginY + rndDiv(rect.Y-sc.Y, scale)
	w = rndDiv(rect.W, scale)
	h = rndDiv(rect.H, scale)
	return x, y, w, h, true
}

// servedFileURL turns an absolute temp-file path (under %TEMP%/toru) into the
// /__file/<basename> URL ShotMiddleware serves. Duplicated from package main
// (windows.go) because the overlay package cannot import package main; keep the
// two trivial copies in sync.
func servedFileURL(absPath string) string {
	return "/__file/" + url.PathEscape(filepath.Base(absPath))
}

// StopRecording finalizes a recording and broadcasts capture:done. It first tears
// down the recorded-region border window (regardless of how the finalize goes, so
// the outline never outlives the recording).
func (s *OverlayService) StopRecording(handleID string) (capture.CaptureResult, error) {
	if s.recordingFrameCloser != nil {
		s.recordingFrameCloser()
	}
	res, err := s.cap.StopRecording(handleID)
	if err != nil {
		return capture.CaptureResult{}, err
	}
	s.rememberRecording(res.VideoPath)
	s.emit(EventCaptureDone, res)
	return res, nil
}

// SaveCrop persists the monitor-local PHYSICAL-px crop for monitorID. Called by
// the front end on crop drag-end (debounced) and again inside CommitScreenshot
// before dismiss.
func (s *OverlayService) SaveCrop(monitorID int, sub capture.Rect) error {
	cropFileMu.Lock()
	defer cropFileMu.Unlock()
	st := loadCrops()
	st.Crops[strconv.Itoa(monitorID)] = sub
	return saveCrops(st)
}

func (s *OverlayService) emit(name string, data any) {
	if s.app != nil {
		s.app.Event.Emit(name, data)
	}
}

// Event names broadcast Go->JS. The "capture:done" payload's Mode field is what
// routes the result to Developer 1's editor (screenshot) vs Developer 2's (video).
//
// overlay:engage (MonitorSession) resets a REUSED overlay window to capture mode
// with the fresh backdrop; overlay:edit (OverlayEditPayload) morphs the SAME
// window into edit mode with the served crop URL + CSS geometry. Both broadcast to
// every overlay window; React filters by its URL ?mon=.
const (
	EventCaptureDone      = "capture:done"
	EventCaptureCancelled = "capture:cancelled"
	EventRecordProgress   = "record:progress"
	EventOverlayDismiss   = "overlay:dismiss"
	EventCaptureThumbnail = "capture:thumbnail"
	EventOverlayEngage    = "overlay:engage"
	EventOverlayEdit      = "overlay:edit"

	// EventOverlayCropRect (capture.Rect, VIRTUAL-DESKTOP PHYSICAL px) is the
	// overlay-INTERNAL relay of the ONE shared cross-monitor crop. The window that
	// owns an in-progress drag calls SetSharedCrop; every window (including the
	// caller) receives this and re-renders its slice, so a straddle crop moves as a
	// single rectangle across the seam. Replaces the old single-active-monitor
	// selection model.
	EventOverlayCropRect = "overlay:cropRect"
)
