// Package tray owns the system tray icon and the recording Stop affordance.
package tray

// State reflects what the tray should show.
type State int

const (
	Idle      State = iota // normal tray icon
	Recording              // show a Stop square + elapsed time
)

// Controller manages the tray icon lifecycle.
type Controller struct {
	state State
}

// New returns a tray controller.
func New() *Controller { return &Controller{} }

// SetState updates the tray icon/menu for the given state.
//
// TODO(shared): build the tray via Wails' systray API; add Capture / Record /
// Settings / Quit menu items and a Stop square while Recording.
func (c *Controller) SetState(s State) { c.state = s }

// State returns the current tray state.
func (c *Controller) State() State { return c.state }
