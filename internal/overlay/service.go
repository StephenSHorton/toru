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

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// OverlayService is the Wails-bound overlay/coordination API (JS: OverlayService.*).
type OverlayService struct {
	app *application.App
	cap capture.Capturer

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
}

// NewService wires the overlay to the shared capture seam.
func NewService(cap capture.Capturer) *OverlayService {
	return &OverlayService{
		cap:       cap,
		windows:   map[int]*application.WebviewWindow{},
		frozenImg: map[int]*image.RGBA{},
		jpegCache: map[int][]byte{},
		pending:   map[int]MonitorSession{},
	}
}

// SetApp injects the running app (called from main after application.New).
//
//wails:ignore
func (s *OverlayService) SetApp(app *application.App) { s.app = app }

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
	frozen, jpegs, err := s.freezeAll(screens)
	if err != nil {
		return nil, err
	}

	// (3) Swap in the fresh maps; drop the previous frozen refs so at most one set
	// is ever resident (never accumulating per engage).
	s.mu.Lock()
	s.dropImagesLocked()
	s.frozenImg = frozen
	s.jpegCache = jpegs
	s.mu.Unlock()

	// (4) Build payloads (StillURL carries ?g=<gen> to cache-bust the reused webview).
	sessions := s.buildSessionPayloads(screens, gen)

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
//   - /__file/<basename> : a committed temp artifact (the cropped screenshot PNG
//     from CropImage, or a recording) living in the toru temp dir. Served by
//     basename only — filepath.Base strips any path traversal and the lookup is
//     confined to %TEMP%/toru — so this cannot read arbitrary disk files.
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
				// joining it onto the fixed toru temp dir confines the read there.
				base := filepath.Base(strings.TrimPrefix(r.URL.Path, filePrefix))
				full := filepath.Join(toruTmp, base)
				if fi, err := os.Stat(full); err == nil && !fi.IsDir() {
					w.Header().Set("Cache-Control", "no-store")
					http.ServeFile(w, r, full) // content-type inferred from extension
					return
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
	s.mu.Lock()
	wins := s.windows
	s.windows = map[int]*application.WebviewWindow{}
	s.dropImagesLocked()
	s.jpegCache = map[int][]byte{}
	s.pending = map[int]MonitorSession{}
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

// EnterEdit is THE single-surface screenshot morph. It crops the in-memory FROZEN
// pixels for monitorID to a small LOSSLESS PNG (served at /__file/<base>) and
// emits overlay:edit to the SAME overlay window — NO separate editor window is
// opened. React loads the crop as the editor base image, sizes the Konva stage to
// the crop's CSS rect, positions it where the bright region was, and morphs the
// dock into the annotation toolbar.
//
// sub is the monitor-local PHYSICAL crop (front end via CropToPhysical); cssLeft/
// cssTop/cssW/cssH are that region in CSS px within this window (echoed back so
// React positions/sizes the embedded stage). The window stays SHOWN — this is the
// same surface, not a re-engage.
func (s *OverlayService) EnterEdit(monitorID int, sub capture.Rect, cssLeft, cssTop, cssW, cssH int) error {
	s.mu.RLock()
	img := s.frozenImg[monitorID]
	s.mu.RUnlock()
	if img == nil {
		return fmt.Errorf("overlay: no frozen image for monitor %d (session not active?)", monitorID)
	}

	// Persist the crop (monitor-local physical px) before morphing to edit.
	_ = s.SaveCrop(monitorID, sub)

	// The frozen RGBA is immutable after the freeze, so cropping it lock-free after
	// grabbing the pointer is safe — nothing mutates an already-frozen image.
	cropPath, err := capture.CropImage(img, sub)
	if err != nil {
		return err
	}
	s.trackCropTemp(cropPath)

	s.emit(EventOverlayEdit, OverlayEditPayload{
		MonitorID: monitorID,
		CropURL:   servedFileURL(cropPath),
		CSSLeft:   cssLeft,
		CSSTop:    cssTop,
		CSSW:      cssW,
		CSSH:      cssH,
		Sub:       sub,
	})
	return nil
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
// the dim overlays) while KEEPING the windows alive, THEN begins recording.
// req.Rect is the virtual-desktop physical Rect the front end emits.
func (s *OverlayService) StartRecording(req capture.CaptureRequest) (string, error) {
	s.HideOverlay()
	return s.cap.StartRecording(req)
}

// servedFileURL turns an absolute temp-file path (under %TEMP%/toru) into the
// /__file/<basename> URL ShotMiddleware serves. Duplicated from package main
// (windows.go) because the overlay package cannot import package main; keep the
// two trivial copies in sync.
func servedFileURL(absPath string) string {
	return "/__file/" + url.PathEscape(filepath.Base(absPath))
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
)
