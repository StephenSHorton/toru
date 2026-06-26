package overlay

import (
	"fmt"
	"image"
	"strconv"
	"sync"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// MonitorSession is the per-overlay-window payload. One is produced per monitor
// by BeginSession; each is also embedded in its window's URL so the front end
// can render WITHOUT a binding round-trip on first paint.
type MonitorSession struct {
	MonitorID int `json:"monitorId"` // == kbinani idx == ScreenInfo.ID == ddagrab output_idx
	// StillURL is served by ShotMiddleware, e.g. "/__shot/0".
	StillURL string `json:"stillUrl"`
	// Monitor geometry in VIRTUAL-DESKTOP PHYSICAL px (origin = primary top-left;
	// monitors left/above carry NEGATIVE X/Y).
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
	// Scale is the monitor DPI scale (CSS px * scale = physical px).
	Scale float64 `json:"scale"`
	// IsPrimary marks the single interactive (crop + pill) window.
	IsPrimary bool `json:"isPrimary"`
	// Crop is MONITOR-LOCAL PHYSICAL px; zero-value {0,0,0,0} when not primary
	// or when there is no restored/default crop. LEGACY: the shared-crop overlay
	// seeds from Region instead; Crop is retained for older callers/tests.
	Crop capture.Rect `json:"crop"`
	// Region is the SHARED crop in VIRTUAL-DESKTOP PHYSICAL px (origin = primary
	// top-left; may be negative; MAY straddle monitors). EVERY window receives the
	// SAME Region and renders its own slice of it — this is the single source of
	// truth for the cross-monitor selection. Seeded from persisted state / a
	// centered default by buildSessionPayloads.
	Region capture.Rect `json:"region"`
	// Freeze tells React how this engage was rendered: true => paint the frozen
	// StillURL backdrop (classic); false => no backdrop, the transparent window
	// shows the LIVE desktop and a screenshot grabs live pixels at Capture.
	Freeze bool `json:"freeze"`
}

// OverlayEditPayload is the overlay:edit event payload (Go->JS). It is emitted by
// EnterEdit to the SAME overlay window (no separate editor window) so React can
// load the served crop PNG as the editor base image, size the Konva stage to the
// crop's CSS rect, position it exactly where the bright region was on screen, and
// morph the dock into the annotation toolbar. React filters by MonitorID == its
// URL ?mon=, so a non-target window ignores it and stays dimmed.
type OverlayEditPayload struct {
	MonitorID int          `json:"monitorId"`
	CropURL   string       `json:"cropUrl"` // served crop PNG, e.g. "/__file/<base>"
	CSSLeft   int          `json:"cssLeft"` // region left in CSS px within the window
	CSSTop    int          `json:"cssTop"`  // region top  in CSS px
	CSSW      int          `json:"cssW"`    // region width  in CSS px = stage width
	CSSH      int          `json:"cssH"`    // region height in CSS px = stage height
	Sub       capture.Rect `json:"sub"`     // monitor-local physical crop (Save provenance)
}

// freezeAll freezes every monitor's pixels IN MEMORY (image.RGBA) and pre-encodes
// each monitor's fast dim-backdrop JPEG, fanning out CONCURRENTLY. It writes ONLY
// per-index local slices from the goroutines and assembles the two maps after
// WaitGroup.Wait, so the shared maps are never touched off the engage goroutine —
// the caller installs them under one Lock. Freezing happens BEFORE any window is
// shown (the windows are hidden when this runs), so no still photographs an
// overlay. No temp files are produced, so there is nothing to clean on error.
func (s *OverlayService) freezeAll(screens []capture.ScreenInfo) (map[int]*image.RGBA, map[int][]byte, error) {
	if len(screens) == 0 {
		return nil, nil, fmt.Errorf("overlay: no screens to freeze")
	}

	imgs := make([]*image.RGBA, len(screens))
	jpgs := make([][]byte, len(screens))
	errs := make([]error, len(screens))
	var wg sync.WaitGroup
	wg.Add(len(screens))
	for i, sc := range screens {
		go func(i int, sc capture.ScreenInfo) {
			defer wg.Done()
			img, err := capture.FreezeMonitorImage(image.Rect(sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H))
			if err != nil {
				errs[i] = err
				return
			}
			imgs[i] = img
			jpgs[i], errs[i] = capture.EncodeJPEG(img, 85)
		}(i, sc)
	}
	wg.Wait()

	for i, sc := range screens {
		if errs[i] != nil {
			return nil, nil, fmt.Errorf("overlay: freeze monitor %d: %w", sc.ID, errs[i])
		}
	}

	frozen := make(map[int]*image.RGBA, len(screens))
	jpegs := make(map[int][]byte, len(screens))
	for i, sc := range screens {
		frozen[sc.ID] = imgs[i]
		jpegs[sc.ID] = jpgs[i]
	}
	return frozen, jpegs, nil
}

// buildSessionPayloads builds one MonitorSession per screen WITHOUT freezing
// (freezeAll already did that, when freeze is on). The primary's crop is restored
// from persisted state (or a centered default). When freeze is on, StillURL
// carries the engage generation as ?g= so a REUSED webview's backdrop <img> is
// cache-busted to the fresh JPEG; when freeze is OFF there is no still, so
// StillURL is empty and React renders the see-through live overlay.
func (s *OverlayService) buildSessionPayloads(screens []capture.ScreenInfo, gen int, freeze bool) []MonitorSession {
	st := loadCrops()
	// One SHARED region (virtual-desktop physical px) seeds every window — the
	// cross-monitor selection is a single rect all windows render a slice of.
	region := seedRegion(screens, st)
	sessions := make([]MonitorSession, 0, len(screens))
	for _, sc := range screens {
		var crop capture.Rect
		if sc.IsPrimary {
			if r, ok := st.Crops[strconv.Itoa(sc.ID)]; ok && validCrop(r, sc.W, sc.H) {
				crop = r
			} else {
				crop = centeredDefault(sc.W, sc.H)
			}
		}
		stillURL := ""
		if freeze {
			stillURL = "/__shot/" + strconv.Itoa(sc.ID) + "?g=" + strconv.Itoa(gen)
		}
		sessions = append(sessions, MonitorSession{
			MonitorID: sc.ID,
			StillURL:  stillURL,
			X:         sc.X,
			Y:         sc.Y,
			W:         sc.W,
			H:         sc.H,
			Scale:     sc.ScaleFactor,
			IsPrimary: sc.IsPrimary,
			Crop:      crop,
			Region:    region,
			Freeze:    freeze,
		})
	}
	return sessions
}

// ensureWindows creates each per-monitor overlay window ONCE (Hidden:true) and
// keeps it alive across captures, keyed by monitor ID in s.windows. Any window
// that is somehow still VISIBLE is Hidden FIRST so the subsequent freeze runs
// while every overlay is hidden (otherwise the frozen still would bake in the dim
// overlay). A vanished monitor's window simply stays hidden — multi-monitor
// topology change between engages is out of scope for v2.
//
// It returns wasVisible == true iff at least one window was actually VISIBLE when
// hidden here (the New-from-edit path). The caller uses that to settle one DWM
// frame before freezing so the just-hidden overlay can't be baked into the still;
// on the cold/idle paths (nothing visible) it returns false so re-engage stays
// instant.
//
// IsVisible() on a never-shown window is SAFE (Wails guards impl==nil -> false).
// Windows are created with their DIP bounds baked into the creation options; the
// first Show() reveals at those bounds, and BeginSession re-asserts bounds via
// SetBounds on every engage (a no-op until the window has been shown once).
func (s *OverlayService) ensureWindows(screens []capture.ScreenInfo) bool {
	if s.app == nil {
		return false
	}
	wasVisible := false
	s.mu.Lock()
	if s.windows == nil {
		s.windows = map[int]*application.WebviewWindow{}
	}
	for _, sc := range screens {
		if w := s.windows[sc.ID]; w != nil {
			if w.IsVisible() {
				wasVisible = true
				w.Hide()
			}
			continue
		}
		dip := s.dipBoundsFor(MonitorSession{MonitorID: sc.ID, X: sc.X, Y: sc.Y, W: sc.W, H: sc.H, Scale: sc.ScaleFactor})
		w := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "toru-overlay-" + strconv.Itoa(sc.ID),
			URL:              overlayURL(sc.ID, sc.IsPrimary, sc.ScaleFactor, sc.X, sc.Y, sc.W, sc.H),
			X:                dip.X,
			Y:                dip.Y,
			Width:            dip.Width,
			Height:           dip.Height,
			Screen:           nil,
			InitialPosition: application.WindowXY,
			Hidden:          true,
			Frameless:       true,
			AlwaysOnTop:     true,
			DisableResize:   true,
			// TRANSPARENT (not solid): the freeze-ON path paints an OPAQUE full-screen
			// frozen-still <img> over it (so it looks exactly like the old solid
			// window once the backdrop decodes — and Show is gated on that decode via
			// OverlayReady, so the transparent window never flashes through). The
			// freeze-OFF path paints NO backdrop, so the same window shows the LIVE
			// desktop through it with only the dim panels + crop chrome on top. Hit
			// testing stays in the DOM (no IgnoreMouseEvents), so the crop is fully
			// interactive even over the see-through region (Wails composites via
			// DirectComposition; clicks land on the DOM, not the desktop behind).
			BackgroundType:   application.BackgroundTypeTransparent,
			BackgroundColour: application.NewRGBA(0, 0, 0, 0),
			Windows: application.WindowsWindow{
				DisableFramelessWindowDecorations: true,
				HiddenOnTaskbar:                   true,
			},
		})
		s.windows[sc.ID] = w
	}
	s.mu.Unlock()
	return wasVisible
}

// dipBoundsFor returns the DIP Bounds of the Wails Screen whose PhysicalBounds
// origin matches the session's virtual-desktop physical X/Y. Falls back to a
// scale-derived approximation if no Wails screen matches (e.g. pre-Run, or the
// enrichment used kbinani-only data).
func (s *OverlayService) dipBoundsFor(mon MonitorSession) application.Rect {
	// Match by rectangle overlap (not exact-origin equality) so a 1px origin
	// disagreement between kbinani and Wails on a secondary mixed-DPI monitor
	// does not drop us into the scale-1.0 fallback (which mis-sizes the window).
	if scr := s.matchWailsScreen(mon.X, mon.Y, mon.W, mon.H); scr != nil {
		return scr.Bounds
	}
	// Fallback: derive DIP from physical via the monitor's own scale.
	scale := mon.Scale
	if scale <= 0 {
		scale = 1.0
	}
	return application.Rect{
		X:      rndDiv(mon.X, scale),
		Y:      rndDiv(mon.Y, scale),
		Width:  rndDiv(mon.W, scale),
		Height: rndDiv(mon.H, scale),
	}
}

// overlayURL builds the per-window URL carrying ONLY the STABLE identity numbers
// (mon, primary, scale, bx, by, mw, mh = monitor physical W/H) the React side
// reads once at mount. In overlay-v2 the per-session data (backdrop URL + restored
// crop) is NO LONGER in the URL — it is pushed to the reused webview via the
// overlay:engage event so a window can re-engage without a navigation. mw/mh let
// the front end clamp the rounded crop EDGES to the true native monitor size so a
// ceil'd DIP width can never push the emitted Rect / badge 1px past the monitor
// (see cropToPhysical).
func overlayURL(monitorID int, isPrimary bool, scale float64, bx, by, mw, mh int) string {
	return fmt.Sprintf(
		"/?view=overlay&mon=%d&primary=%d&scale=%s&bx=%d&by=%d&mw=%d&mh=%d",
		monitorID,
		b2i(isPrimary),
		formatFloat(scale),
		bx,
		by,
		mw,
		mh,
	)
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// rndDiv divides an int by a scale and rounds to nearest int (DIP fallback math).
func rndDiv(v int, scale float64) int {
	return int(float64(v)/scale + 0.5)
}
