// Package safety decides when a run must stop or pause: screen-corner and edge
// failsafes per monitor, stop/pause/start zones, the task switcher, the
// foreground-app allow/block list and hold-mode hotkey release. Geometry is pure
// and tested; the Windows queries sit behind System.
package safety

import "github.com/StephenSHorton/toru/internal/clicker/engine"

// Rect is a screen rectangle in physical virtual-desktop px; R and B are exclusive.
type Rect struct{ L, T, R, B int32 }

// Zone actions.
const (
	ZoneStop  uint8 = 1
	ZonePause uint8 = 2
	ZoneStart uint8 = 3
)

// Zone is a StopZone resolved for checking.
type Zone struct {
	Rect   Rect
	Action uint8
	Index  int // position in the settings list, for display
}

// Corners/Edges sizes in px; 0 = off.
type Corners struct {
	Enabled        bool
	TL, TR, BL, BR int32
}

type Edges struct {
	Enabled                  bool
	Top, Right, Bottom, Left int32
	OuterOnly                bool // skip edges shared with a neighbouring monitor
}

// ProcessRule is the foreground-app list.
type ProcessRule struct {
	Enabled bool
	Allow   bool     // true: run only with one of Exes in front; false: stop when one is in front
	Exes    []string // lowercase "name.exe"
}

// Config is one run's failsafe setup.
type Config struct {
	Corners      Corners
	Edges        Edges
	Zones        []Zone // empty when zones are off
	TaskSwitcher bool
	Process      ProcessRule
	// HoldTriggerVKs are the clickerToggle binding's keys in hold mode (empty
	// = toggle mode): the run stops once any of them is no longer physically
	// down, so releasing one key of a chord ends it.
	HoldTriggerVKs []uint16
	// StartZoneIndex >= 0 means the run was started by entering that start
	// zone and stops (zoneLeft) when the cursor leaves it.
	StartZoneIndex int
}

// Foreground describes the window in front.
type Foreground struct {
	Exe      string // lowercase file name
	Class    string
	Elevated bool // its process runs as administrator (input from Toru is blocked)
	OK       bool
}

// System is what the guard reads from Windows; tests pass a fake.
type System interface {
	Cursor() (x, y int32, ok bool)
	Monitors() []Rect // rcMonitor of every display
	Foreground() Foreground
	TaskSwitcherActive() bool // Alt+Tab / Win+Tab UI in front or Alt+Tab physically held
	PhysicallyDown(vk uint16) bool
	SelfElevated() bool
}

// ProcessInfo mirrors clicker.ProcessInfo.
type ProcessInfo struct {
	Exe     string
	Title   string
	IconURL string
}

// Guard implements engine.Guard. Check order: stop zone, corner, edge, task
// switcher, process list, hold release, start-zone left, pause zone (unless a
// start zone covers the cursor).
type Guard interface {
	engine.Guard
	// Warning is a non-fatal note for the status line ("" = none), e.g. an
	// elevated foreground app.
	Warning() string
}
