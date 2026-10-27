package safety

import (
	"strconv"
	"strings"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
)

// guard is one run's failsafe checker. Its config is fixed at NewGuard, so it
// holds no mutable state of its own: Check (the run goroutine) and Warning (the
// status loop) may run at the same time, and System guards its own caches.
type guard struct {
	cfg  Config
	sys  System
	exes map[string]struct{} // normalized Process.Exes
	// startZone is the slice index of the start zone that started the run, or
	// -1 when the run was not started by a zone.
	startZone int
}

// NewGuard builds the failsafe guard for one run. A nil sys gets a System that
// reports nothing, so the guard always answers Continue.
func NewGuard(cfg Config, sys System) Guard {
	if sys == nil {
		sys = nopSystem{}
	}
	g := &guard{cfg: cfg, sys: sys, startZone: -1}
	if cfg.Process.Enabled {
		g.exes = make(map[string]struct{}, len(cfg.Process.Exes))
		for _, e := range cfg.Process.Exes {
			if n := NormalizeExe(e); n != "" {
				g.exes[n] = struct{}{}
			}
		}
	}
	// StartZoneIndex is a settings-list position (Zone.Index). It only counts
	// when it names a start zone: a zero-value Config (StartZoneIndex 0) must
	// not turn zone 0 into "the zone that started the run".
	if cfg.StartZoneIndex >= 0 {
		for i, z := range cfg.Zones {
			if z.Index == cfg.StartZoneIndex && z.Action == ZoneStart {
				g.startZone = i
				break
			}
		}
	}
	return g
}

// Check runs the failsafes in priority order: stop zone, corner, edge, task
// switcher, process list, hold release, start zone left, pause zone. A pause
// zone under a start zone does not pause (ZoneAt lets start win).
func (g *guard) Check(int64, int64) engine.Verdict {
	x, y, haveCursor := g.sys.Cursor()

	zoneAction, zoneIdx := uint8(0), -1
	if haveCursor && len(g.cfg.Zones) > 0 {
		zoneAction, zoneIdx = ZoneAt(g.cfg.Zones, x, y)
		if zoneAction == ZoneStop {
			return stop(codeInZone, "Stop zone "+zoneLabel(g.cfg.Zones[zoneIdx]))
		}
	}

	if haveCursor && (g.cfg.Corners.Enabled || g.cfg.Edges.Enabled) {
		mons := g.sys.Monitors()
		if r := cornerHit(g.cfg.Corners, mons, x, y); r != "" {
			return stop(codeCorner, r)
		}
		if r := edgeHit(g.cfg.Edges, mons, x, y); r != "" {
			return stop(codeEdge, r)
		}
	}

	if g.cfg.TaskSwitcher && g.sys.TaskSwitcherActive() {
		return stop(codeTaskSwitcher, "Alt+Tab")
	}

	if g.cfg.Process.Enabled {
		if r := processBlocked(g.cfg.Process.Allow, g.exes, g.sys.Foreground()); r != "" {
			return stop(codeProcessList, r)
		}
	}

	if g.cfg.HoldTriggerVK != 0 && !g.sys.PhysicallyDown(g.cfg.HoldTriggerVK) {
		return stop(codeHoldRelease, "Hotkey released")
	}

	if g.startZone >= 0 && haveCursor {
		z := g.cfg.Zones[g.startZone]
		if !z.Rect.contains(x, y) {
			return stop(codeZoneLeft, "Left start zone "+zoneLabel(z))
		}
	}

	if zoneAction == ZonePause {
		// Pause has no StopCode: the caller maps a Pause verdict to PauseZone.
		return engine.Verdict{Kind: engine.Pause, Reason: "Pause zone " + zoneLabel(g.cfg.Zones[zoneIdx])}
	}
	return engine.Verdict{}
}

// Warning notes an elevated foreground app: Windows drops Toru's input into it
// unless Toru runs as administrator too.
func (g *guard) Warning() string {
	fg := g.sys.Foreground()
	if !fg.OK || !fg.Elevated || g.sys.SelfElevated() {
		return ""
	}
	name := fg.Exe
	if name == "" {
		name = "The app in front"
	}
	return name + " runs as administrator; Windows blocks clicks into it"
}

func stop(code, reason string) engine.Verdict {
	return engine.Verdict{Kind: engine.Stop, Code: code, Reason: reason}
}

// zoneLabel is a zone's 1-based position in the settings list, as the UI
// numbers them.
func zoneLabel(z Zone) string { return strconv.Itoa(z.Index + 1) }

// processBlocked returns why fg stops the run under the allow/block list, or ""
// when it may run. An unknown foreground (none, or its exe could not be read)
// never stops a run. An allow list with no entries blocks every app.
func processBlocked(allow bool, exes map[string]struct{}, fg Foreground) string {
	if !fg.OK || fg.Exe == "" {
		return ""
	}
	_, listed := exes[strings.ToLower(fg.Exe)]
	switch {
	case allow && !listed:
		return fg.Exe + " is not in the allow list"
	case !allow && listed:
		return fg.Exe + " is in the block list"
	}
	return ""
}

// NormalizeExe turns a list entry into the form the foreground check compares:
// trimmed, lowercase, with ".exe". A path keeps only its file name.
func NormalizeExe(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndexAny(s, `\/`); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return ""
	}
	if !strings.HasSuffix(s, ".exe") {
		s += ".exe"
	}
	return s
}

// nopSystem reports no cursor, no monitors and no foreground app, and treats a
// hold key as still held, so a guard over it never stops a run.
type nopSystem struct{}

func (nopSystem) Cursor() (int32, int32, bool) { return 0, 0, false }
func (nopSystem) Monitors() []Rect             { return nil }
func (nopSystem) Foreground() Foreground       { return Foreground{} }
func (nopSystem) TaskSwitcherActive() bool     { return false }
func (nopSystem) PhysicallyDown(uint16) bool   { return true }
func (nopSystem) SelfElevated() bool           { return false }
