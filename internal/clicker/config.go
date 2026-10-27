package clicker

import (
	"math"
	"strings"
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/clicker/overlay"
	"github.com/StephenSHorton/toru/internal/clicker/safety"
)

// dutyCap is the most of an interval a press may be held at a rate, so press
// and release always fit in the cycle with room for the next one.
func dutyCap(cps float64) float64 {
	switch {
	case cps > 500:
		return 0.01
	case cps >= 200:
		return 0.30
	case cps >= 100:
		return 0.70
	case cps >= 50:
		return 0.98
	}
	return 1
}

var engineButton = map[MouseButton]engine.Button{
	ButtonLeft:   engine.ButtonLeft,
	ButtonRight:  engine.ButtonRight,
	ButtonMiddle: engine.ButtonMiddle,
	ButtonX1:     engine.ButtonX1,
	ButtonX2:     engine.ButtonX2,
}

// buildEngineConfig freezes a normalized Profile into one run's engine.Config.
// It fails with ZERO_RATE, NO_KEY or UNKNOWN_KEY.
func buildEngineConfig(p Profile, d *deps, seed uint64) (engine.Config, error) {
	ivMs := intervalMs(p.Cadence)
	if !(ivMs > 0) || !finite(ivMs) {
		return engine.Config{}, clickerErr(ErrZeroRate, "The click rate must be above zero")
	}
	ivMs = math.Max(ivMs, 1)
	cps := 1000 / ivMs
	cfg := engine.Config{
		BaseIntervalNs: int64(math.Round(ivMs * 1e6)),
		DutyFraction:   math.Min(p.DutyPercent/100, dutyCap(cps)),
		HoldGapNs:      int64(p.HoldGapMs) * int64(time.Millisecond),
		Seed:           seed,
	}
	if p.Jitter.Enabled {
		cfg.JitterSigma = p.Jitter.Percent / 100
	}

	switch {
	case p.Press == PressHold && p.Input == InputKeyboard:
		cfg.Mode = engine.ModeKeyHold
	case p.Press == PressHold:
		cfg.Mode = engine.ModeMouseHoldInterval
	case p.DoubleClick:
		cfg.Mode = engine.ModeDouble
	default:
		cfg.Mode = engine.ModeClick
	}
	if cfg.Mode == engine.ModeDouble {
		// The two clicks sit inside the system double-click window and the cycle.
		gap := time.Duration(p.DoubleGapMs) * time.Millisecond
		if d.doubleClickTime != nil {
			if sysGap := d.doubleClickTime() * 9 / 10; sysGap > 0 && sysGap < gap {
				gap = sysGap
			}
		}
		if lim := time.Duration(cfg.BaseIntervalNs) - time.Millisecond; lim > 0 && gap > lim {
			gap = lim
		}
		cfg.DoubleGapNs = int64(gap)
	}

	if p.Input == InputKeyboard {
		if strings.TrimSpace(p.Key) == "" {
			return engine.Config{}, clickerErr(ErrNoKey, "Choose a key to press first")
		}
		scan, ext, letter, err := d.keyScan(p.Key)
		if err != nil || scan == 0 {
			return engine.Config{}, clickerErrf(ErrUnknownKey, "%q is not a key the clicker can press", p.Key)
		}
		cfg.Keyboard = true
		cfg.Scan, cfg.Extended = scan, ext
		if letter {
			cfg.WantUpper = p.Uppercase
			cfg.WantLower = !p.Uppercase
			cfg.CapsLock = d.capsLock
		}
		if cfg.Mode == engine.ModeKeyHold && d.keyRepeat != nil {
			cfg.KeyRepeat = d.keyRepeat()
		}
	} else {
		cfg.Button = engineButton[p.Button]
	}

	if p.ClickPoints.Enabled && len(p.ClickPoints.Points) > 0 {
		cfg.Points = make([]engine.Point, len(p.ClickPoints.Points))
		for i, pt := range p.ClickPoints.Points {
			cfg.Points[i] = engine.Point{X: pt.X, Y: pt.Y, Clicks: pt.Clicks, Radius: pt.Radius}
		}
		cfg.StopAfterPass = p.ClickPoints.StopAfterPass
		cfg.Glide = p.ClickPoints.Glide
	}
	switch p.Limit.Kind {
	case LimitClicks:
		cfg.ClickLimit = p.Limit.Clicks
	case LimitTime:
		cfg.TimeLimitNs = int64(p.Limit.Seconds * 1e9)
	}
	return cfg, nil
}

var safetyZoneAction = map[ZoneAction]uint8{
	ZoneStop:  safety.ZoneStop,
	ZonePause: safety.ZonePause,
	ZoneStart: safety.ZoneStart,
}

// safetyZones resolves the zone list for checking (empty when zones are off).
func safetyZones(s Settings) []safety.Zone {
	if !s.Profile.ZonesEnabled {
		return nil
	}
	out := make([]safety.Zone, 0, len(s.StopZones))
	for i, z := range s.StopZones {
		out = append(out, safety.Zone{
			Rect:   safety.Rect{L: z.X, T: z.Y, R: z.X + z.W, B: z.Y + z.H},
			Action: safetyZoneAction[z.Action],
			Index:  i,
		})
	}
	return out
}

// buildSafetyConfig is one run's failsafe setup. holdVKs are the hold-mode
// trigger keys (empty in toggle mode); startZone >= 0 when a start zone
// started it.
func buildSafetyConfig(s Settings, holdVKs []uint16, startZone int) safety.Config {
	p := s.Profile
	cfg := safety.Config{
		Corners: safety.Corners{
			Enabled: p.Corners.Enabled,
			TL:      int32(p.Corners.TL), TR: int32(p.Corners.TR),
			BL: int32(p.Corners.BL), BR: int32(p.Corners.BR),
		},
		Edges: safety.Edges{
			Enabled: p.Edges.Enabled,
			Top:     int32(p.Edges.Top), Right: int32(p.Edges.Right),
			Bottom: int32(p.Edges.Bottom), Left: int32(p.Edges.Left),
			OuterOnly: p.Edges.OuterOnly,
		},
		Zones:          safetyZones(s),
		TaskSwitcher:   s.StopOnTaskSwitcher,
		HoldTriggerVKs: holdVKs,
		StartZoneIndex: startZone,
	}
	if p.ProcessList.Enabled {
		cfg.Process = safety.ProcessRule{Enabled: true, Allow: p.ProcessList.Mode == ListAllow}
		for _, e := range p.ProcessList.Entries {
			if e.Enabled {
				cfg.Process.Exes = append(cfg.Process.Exes, e.Exe)
			}
		}
	}
	return cfg
}

// startWarning is the status-line note known before a run starts.
func startWarning(s Settings) string {
	pl := s.Profile.ProcessList
	if pl.Enabled && pl.Mode == ListAllow {
		for _, e := range pl.Entries {
			if e.Enabled {
				return ""
			}
		}
		return "The allow list is empty, so the clicker will not click in any app"
	}
	return ""
}

// safetyScene is the failsafe preview for the overlay.
func safetyScene(s Settings, mons []safety.Rect) overlay.SafetyScene {
	p := s.Profile
	sc := overlay.SafetyScene{
		Corners:   [4]int32{int32(p.Corners.TL), int32(p.Corners.TR), int32(p.Corners.BL), int32(p.Corners.BR)},
		CornersOn: p.Corners.Enabled,
		Edges:     [4]int32{int32(p.Edges.Top), int32(p.Edges.Right), int32(p.Edges.Bottom), int32(p.Edges.Left)},
		EdgesOn:   p.Edges.Enabled,
		OuterOnly: p.Edges.OuterOnly,
		ZonesOn:   p.ZonesEnabled,
	}
	for _, m := range mons {
		sc.Monitors = append(sc.Monitors, overlay.Monitor{L: m.L, T: m.T, R: m.R, B: m.B})
	}
	for i, z := range s.StopZones {
		sc.Zones = append(sc.Zones, overlay.SceneZone{
			L: z.X, T: z.Y, R: z.X + z.W, B: z.Y + z.H,
			Action: safetyZoneAction[z.Action],
			Index:  i,
		})
	}
	return sc
}

// scenePoints is the click-point list for the overlay.
func scenePoints(pts []ClickPoint) []overlay.ScenePoint {
	out := make([]overlay.ScenePoint, len(pts))
	for i, pt := range pts {
		out[i] = overlay.ScenePoint{ID: pt.ID, X: pt.X, Y: pt.Y, Radius: int32(pt.Radius), Index: i}
	}
	return out
}

// safetyGeometryChanged reports whether anything the failsafe preview draws
// differs between a and b.
func safetyGeometryChanged(a, b Settings) bool {
	pa, pb := a.Profile, b.Profile
	if pa.Corners != pb.Corners || pa.Edges != pb.Edges || pa.ZonesEnabled != pb.ZonesEnabled {
		return true
	}
	if len(a.StopZones) != len(b.StopZones) {
		return true
	}
	for i := range a.StopZones {
		if a.StopZones[i] != b.StopZones[i] {
			return true
		}
	}
	return false
}

// pointsChanged reports whether the click points (or their switch) differ.
func pointsChanged(a, b ClickPoints) bool {
	if a.Enabled != b.Enabled || len(a.Points) != len(b.Points) {
		return true
	}
	for i := range a.Points {
		if a.Points[i] != b.Points[i] {
			return true
		}
	}
	return false
}
