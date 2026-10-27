// Package engine is the auto clicker's run loop and scheduling math. It is pure
// Go with no syscalls: input goes out through Injector, time comes from Clock and
// failsafes are asked through Guard, so the whole loop runs under test with fakes.
package engine

// Button is a mouse button to press.
type Button uint8

const (
	ButtonLeft Button = iota + 1
	ButtonRight
	ButtonMiddle
	ButtonX1
	ButtonX2
)

// OpKind is one input event.
type OpKind uint8

const (
	OpMouseDown OpKind = iota + 1
	OpMouseUp
	OpKeyDown // Op.Scan + Op.Extended
	OpKeyUp
	OpShiftDown
	OpShiftUp
)

// Op is one input event for Injector.Send.
type Op struct {
	Kind     OpKind
	Button   Button
	Scan     uint16
	Extended bool
}

// Injector sends input. The real one (clicker/sys) tags every event with
// inputtag.Toru; tests record the ops.
type Injector interface {
	// Send injects ops in order in one SendInput call and returns how many
	// went through (fewer than len(ops) means Windows blocked them, e.g. an
	// elevated window is in front).
	Send(ops []Op) (sent int, err error)
	// Teleport puts the cursor exactly on (x, y) (SetCursorPos).
	Teleport(x, y int32) error
	// GlideStep moves the cursor to (x, y) as a mouse move event.
	GlideStep(x, y int32) error
	// Cursor returns the cursor position.
	Cursor() (x, y int32, ok bool)
}

// Clock is monotonic time in nanoseconds with an interruptible wait.
type Clock interface {
	Now() int64
	// WaitUntil blocks until Now() >= deadline and returns true, or returns
	// false early when Interrupt is called.
	WaitUntil(deadline int64) bool
	// Interrupt wakes a WaitUntil in progress (and makes the next one return
	// false at once) until ClearInterrupt. Safe from any goroutine.
	Interrupt()
	ClearInterrupt()
}

// VerdictKind is what a Guard wants.
type VerdictKind uint8

const (
	Continue VerdictKind = iota
	Pause
	Stop
)

// Verdict is a Guard's answer. Code is a clicker.StopCode value (a string here
// to avoid an import cycle); Reason is display text.
type Verdict struct {
	Kind   VerdictKind
	Code   string
	Reason string
}

// Guard is asked before every click and at least every 10 ms while waiting.
// Pause holds the run (releasing anything held) until a later Check returns
// Continue; Stop ends it.
type Guard interface {
	Check(now int64, clicks int64) Verdict
}

// RunMode is the shape of one cycle.
type RunMode uint8

const (
	// ModeClick: press, hold DutyFraction of the interval, release.
	ModeClick RunMode = iota + 1
	// ModeDouble: two clicks DoubleGapNs apart in one cycle; counts as 2.
	ModeDouble
	// ModeMouseHoldInterval: press, hold for the drawn interval, release,
	// wait HoldGapNs; counts completed holds.
	ModeMouseHoldInterval
	// ModeKeyHold: hold the key down, repeating key-downs at KeyRepeat like a
	// physically held key; released on pause and stop.
	ModeKeyHold
)

// KeyRepeat is Windows' typematic timing (SPI_GETKEYBOARDDELAY/SPEED).
type KeyRepeat struct{ DelayNs, PeriodNs int64 }

// Point is a click target in physical virtual-desktop px.
type Point struct {
	X, Y   int32
	Clicks int // cycles before moving to the next point, >= 1
	Radius int // random scatter radius, 0 = exact
}

// Config is one run's resolved, immutable settings (built by the clicker
// service from a Profile).
type Config struct {
	BaseIntervalNs int64   // >= 1 ms
	JitterSigma    float64 // standard deviation as a fraction of the interval; 0 = off
	DutyFraction   float64 // 0..1, already capped for the rate
	Mode           RunMode
	DoubleGapNs    int64
	HoldGapNs      int64
	// Mouse: which button. Keyboard (Scan != 0): the key to press.
	Button   Button
	Keyboard bool
	Scan     uint16
	Extended bool
	// WantUpper/WantLower: wrap the key in Shift so a letter comes out upper or
	// lower case regardless of Caps Lock. CapsLock is read once per press.
	WantUpper bool
	WantLower bool
	CapsLock  func() bool
	KeyRepeat KeyRepeat
	// Points: empty = click wherever the cursor is.
	Points        []Point
	StopAfterPass bool
	Glide         bool
	ClickLimit    int64 // 0 = none
	TimeLimitNs   int64 // 0 = none
	Seed          uint64
}

// Progress is reported by Run after every counted click; the caller throttles.
type Progress struct {
	Clicks      int64
	ActivePoint int // -1 when not using points
	PointTick   int
}

// Stop codes the engine itself produces (clicker.StopCode values).
const (
	CodeClickLimit  = "clickLimit"
	CodeTimeLimit   = "timeLimit"
	CodePointsDone  = "pointsDone"
	CodeInterrupted = "user" // the caller interrupted the clock and Paused() is false
	CodeError       = "error"
)
