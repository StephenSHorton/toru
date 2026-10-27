// Package overlay draws the auto clicker's native on-screen layers: the failsafe
// preview, the click-point and zone pickers, and the running indicator pill.
// Every window is a per-monitor WS_EX_LAYERED|TRANSPARENT|TOPMOST|NOACTIVATE
// tool window painted from a Go RGBA buffer, so it never takes focus or clicks.
// Coordinates are physical virtual-desktop px (the process is PMv2).
package overlay

// Monitor is a display rect; R and B are exclusive.
type Monitor struct{ L, T, R, B int32 }

// Zone actions (same values as safety).
const (
	ZoneStop  uint8 = 1
	ZonePause uint8 = 2
	ZoneStart uint8 = 3
)

// SceneZone is one zone to draw; Index is shown as its label (1-based on screen).
type SceneZone struct {
	L, T, R, B int32
	Action     uint8
	Index      int
}

// SafetyScene is the failsafe preview.
type SafetyScene struct {
	Monitors  []Monitor
	Corners   [4]int32 // TL, TR, BL, BR; 0 = off
	CornersOn bool
	Edges     [4]int32 // top, right, bottom, left; 0 = off
	EdgesOn   bool
	OuterOnly bool
	Zones     []SceneZone
	ZonesOn   bool
}

// ScenePoint is a click point to draw.
type ScenePoint struct {
	ID     string
	X, Y   int32
	Radius int32
	Index  int
}

// PickKind selects a picker.
type PickKind uint8

const (
	PickPoint PickKind = 1
	PickZone  PickKind = 2
)

// PickCallbacks are called from the picker's hook thread; keep them short.
type PickCallbacks struct {
	// OnPoint: right click at (x, y). keepPicking is true when Shift was held.
	OnPoint func(x, y int32, keepPicking bool)
	// OnDelete: Ctrl+right click near (x, y).
	OnDelete func(x, y int32)
	// OnZone: a right-drag finished; R and B are exclusive.
	OnZone func(l, t, r, b int32)
	// OnEnd: the pick is over (cancelled = Esc or CancelPick).
	OnEnd func(cancelled bool)
}
