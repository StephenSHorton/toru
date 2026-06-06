package overlay

import (
	"fmt"
	"image"
	"net/url"
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
	// or when there is no restored/default crop.
	Crop capture.Rect `json:"crop"`
}

// buildSessions enumerates screens (from s.screens, already populated by
// ListScreens), freezes every monitor, restores the persisted primary crop, and
// returns one MonitorSession per screen. Stills are recorded into s.stills /
// s.frozen under the lock. Freezing happens BEFORE any window is shown (the
// caller opens windows only after this returns).
func (s *OverlayService) buildSessions(screens []capture.ScreenInfo) ([]MonitorSession, error) {
	if len(screens) == 0 {
		return nil, fmt.Errorf("overlay: no screens to build a session for")
	}

	st := loadCrops()

	// Freeze every monitor CONCURRENTLY. capture+PNG-encode is the dominant
	// "engage" cost and was previously serial across monitors, so a 2-3 monitor
	// setup stalled for seconds before any overlay window appeared. Each freeze
	// grabs an independent screen region into its own temp file, so they fan out
	// cleanly; total time is now ~the slowest single monitor, not the sum.
	paths := make([]string, len(screens))
	errs := make([]error, len(screens))
	var wg sync.WaitGroup
	wg.Add(len(screens))
	for i, sc := range screens {
		go func(i int, sc capture.ScreenInfo) {
			defer wg.Done()
			paths[i], errs[i] = capture.FreezeMonitor(image.Rect(sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H))
		}(i, sc)
	}
	wg.Wait()

	// If any freeze failed, clean up the ones that succeeded and bail.
	// removeFile tolerates the "" left by a failed freeze.
	for i, sc := range screens {
		if errs[i] != nil {
			for _, p := range paths {
				_ = removeFile(p)
			}
			return nil, fmt.Errorf("overlay: freeze monitor %d: %w", sc.ID, errs[i])
		}
	}

	sessions := make([]MonitorSession, 0, len(screens))
	frozen := make(map[int]string, len(screens))
	stills := make(map[string]string, len(screens))

	for i, sc := range screens {
		path := paths[i]
		frozen[sc.ID] = path
		stills[strconv.Itoa(sc.ID)] = path

		var crop capture.Rect
		if sc.IsPrimary {
			if r, ok := st.Crops[strconv.Itoa(sc.ID)]; ok && validCrop(r, sc.W, sc.H) {
				crop = r
			} else {
				crop = centeredDefault(sc.W, sc.H)
			}
		}

		sessions = append(sessions, MonitorSession{
			MonitorID: sc.ID,
			StillURL:  "/__shot/" + strconv.Itoa(sc.ID),
			X:         sc.X,
			Y:         sc.Y,
			W:         sc.W,
			H:         sc.H,
			Scale:     sc.ScaleFactor,
			IsPrimary: sc.IsPrimary,
			Crop:      crop,
		})
	}

	s.mu.Lock()
	s.frozen = frozen
	s.stills = stills
	s.mu.Unlock()

	return sessions, nil
}

// openOverlayWindows creates one frameless, always-on-top, opaque, non-resizable
// window per monitor, sized to that monitor's DIP Bounds (NEVER physical px,
// which would double-scale on HiDPI). It looks up the Wails Screen by matching
// PhysicalBounds origin to the session's physical X/Y. Window handles are kept in
// s.windows so DismissSession can Close() each one.
func (s *OverlayService) openOverlayWindows(sessions []MonitorSession) {
	if s.app == nil {
		return
	}

	wins := make([]*application.WebviewWindow, 0, len(sessions))
	for _, mon := range sessions {
		dip := s.dipBoundsFor(mon)

		win := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
			Name:             "toru-overlay-" + strconv.Itoa(mon.MonitorID),
			URL:              overlayURL(mon),
			X:                dip.X,
			Y:                dip.Y,
			Width:            dip.Width,
			Height:           dip.Height,
			Screen:           nil,
			InitialPosition:  application.WindowXY,
			Frameless:        true,
			AlwaysOnTop:      true,
			DisableResize:    true,
			BackgroundType:   application.BackgroundTypeSolid,
			BackgroundColour: application.NewRGB(0, 0, 0),
			Windows: application.WindowsWindow{
				DisableFramelessWindowDecorations: true,
				HiddenOnTaskbar:                   true,
			},
		})

		// Belt-and-suspenders for the primary (DIP origin (0,0)): Wails' creation
		// path already calls setPosition(0,0) when Screen==nil + InitialPosition
		// is WindowXY, so the primary is placed correctly without this. We also
		// open the overlay on ApplicationStarted, so the window impl is live and
		// SetBounds is NOT a no-op — it just re-asserts the (0,0)+size bounds in
		// case the OS treated the initial 0,0 as CW_USEDEFAULT. application.Rect
		// uses Width/Height (not W/H).
		if dip.X == 0 && dip.Y == 0 {
			win.SetBounds(application.Rect{X: 0, Y: 0, Width: dip.Width, Height: dip.Height})
		}

		wins = append(wins, win)
	}

	s.mu.Lock()
	s.windows = append(s.windows, wins...)
	s.mu.Unlock()
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

// overlayURL builds the per-window URL carrying the session numbers so the front
// end can render on first paint without a binding round-trip. Params:
// mon, primary, scale, bx, by, mw, mh (monitor physical W/H), still (urlenc),
// crop (urlenc "x,y,w,h"). mw/mh let the front end clamp the rounded crop EDGES
// to the true native monitor size so a ceil'd DIP width can never push the
// emitted Rect / badge 1px past the monitor (see cropToPhysical).
func overlayURL(mon MonitorSession) string {
	return fmt.Sprintf(
		"/?view=overlay&mon=%d&primary=%d&scale=%s&bx=%d&by=%d&mw=%d&mh=%d&still=%s&crop=%s",
		mon.MonitorID,
		b2i(mon.IsPrimary),
		formatFloat(mon.Scale),
		mon.X,
		mon.Y,
		mon.W,
		mon.H,
		url.QueryEscape(mon.StillURL),
		url.QueryEscape(cropCSV(mon.Crop)),
	)
}

func cropCSV(r capture.Rect) string {
	return fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.W, r.H)
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
