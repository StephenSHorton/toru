// Package clicker is Toru's auto clicker: a Wails-bound service that clicks a
// mouse button or presses a key at a set rate, with click points, failsafes
// (screen corners/edges, stop/pause/start zones, a process allow/block list)
// and presets.
//
// Layout:
//   - clicker         : the bound ClickerService, settings + presets + stats
//     persistence, the run controller (this package).
//   - clicker/engine  : pure-Go scheduling and the run loop (no syscalls).
//   - clicker/sys     : Win32 input injection, timers, key/scan lookups.
//   - clicker/safety  : failsafe geometry, foreground-process checks.
//   - clicker/overlay : native click-through overlays (safety preview,
//     point/zone pickers, the running indicator).
//
// All screen coordinates are physical virtual-desktop pixels: Toru is
// Per-Monitor-V2 DPI aware (internal/dpi), so GetCursorPos/SetCursorPos and
// monitor rects need no conversion.
package clicker

// RateUnit is the period a Cadence.Rate counts over.
type RateUnit string

const (
	UnitSecond RateUnit = "s"
	UnitMinute RateUnit = "m"
	UnitHour   RateUnit = "h"
	UnitDay    RateUnit = "d"
)

// CadenceMode says which form of Cadence the user edits; Normalize derives the
// other one so both always agree.
type CadenceMode string

const (
	CadenceRate     CadenceMode = "rate"
	CadenceInterval CadenceMode = "interval"
)

// InputKind is what a run presses.
type InputKind string

const (
	InputMouse    InputKind = "mouse"
	InputKeyboard InputKind = "keyboard"
)

// MouseButton is the button a mouse run clicks.
type MouseButton string

const (
	ButtonLeft   MouseButton = "left"
	ButtonRight  MouseButton = "right"
	ButtonMiddle MouseButton = "middle"
	ButtonX1     MouseButton = "x1"
	ButtonX2     MouseButton = "x2"
)

// PressMode: "click" presses and releases once per cycle (the hold inside the
// cycle is DutyPercent). "hold" means, for the mouse, hold for the interval,
// release, wait HoldGapMs; for the keyboard, hold the key down with Windows'
// own key-repeat (typematic) rate.
type PressMode string

const (
	PressClick PressMode = "click"
	PressHold  PressMode = "hold"
)

// HotkeyMode is how the clickerToggle hotkey drives a run.
type HotkeyMode string

const (
	HotkeyToggle HotkeyMode = "toggle" // press starts, next press stops
	HotkeyHold   HotkeyMode = "hold"   // runs while the hotkey is held down
)

// ZoneAction is what entering a StopZone does. When zones overlap, stop beats
// start and start beats pause. A start zone carves a hole in pause zones, and
// while the clicker is armed and idle, moving the cursor into one starts a run
// that stops again when the cursor leaves it.
type ZoneAction string

const (
	ZoneStop  ZoneAction = "stop"
	ZonePause ZoneAction = "pause"
	ZoneStart ZoneAction = "start"
)

// ListMode is how ProcessList.Entries are read against the foreground app.
type ListMode string

const (
	ListAllow ListMode = "allow" // run only while one of the entries is in front
	ListBlock ListMode = "block" // stop when one of the entries is in front
)

// LimitKind picks at most one automatic stop condition.
type LimitKind string

const (
	LimitNone   LimitKind = "none"
	LimitClicks LimitKind = "clicks"
	LimitTime   LimitKind = "time"
)

// Cadence is how fast a run clicks.
type Cadence struct {
	Mode       CadenceMode `json:"mode"`
	Rate       float64     `json:"rate"` // clicks per Unit, > 0
	Unit       RateUnit    `json:"unit"`
	IntervalMs float64     `json:"intervalMs"` // >= 1
}

// Jitter randomizes each interval with a Gaussian spread.
type Jitter struct {
	Enabled bool    `json:"enabled"`
	Percent float64 `json:"percent"` // 0..100: standard deviation as a % of the interval
}

// Limit stops a run after a number of clicks or an amount of time.
type Limit struct {
	Kind    LimitKind `json:"kind"`
	Clicks  int64     `json:"clicks"`  // 1..100_000_000
	Seconds float64   `json:"seconds"` // >= 1
}

// ClickPoint is a screen spot a run moves to and clicks.
type ClickPoint struct {
	ID     string `json:"id"`
	X      int32  `json:"x"` // physical virtual-desktop px
	Y      int32  `json:"y"`
	Clicks int    `json:"clicks"` // 1..999_999 cycles before moving on
	Radius int    `json:"radius"` // 0..9_999 px of random scatter around the spot
}

// ClickPoints makes a run visit Points in order (round robin) instead of
// clicking wherever the cursor is.
type ClickPoints struct {
	Enabled       bool         `json:"enabled"`
	StopAfterPass bool         `json:"stopAfterPass"` // stop after the last point instead of wrapping
	Glide         bool         `json:"glide"`         // smooth cursor move between points when the interval allows
	Points        []ClickPoint `json:"points"`
}

// Corners stops a run when the cursor enters a square of the given size in a
// monitor corner. 0 turns that corner off.
type Corners struct {
	Enabled bool `json:"enabled"`
	TL      int  `json:"tl"` // 0..10_000 px
	TR      int  `json:"tr"`
	BL      int  `json:"bl"`
	BR      int  `json:"br"`
}

// Edges stops a run when the cursor enters a band of the given thickness along
// a monitor edge. 0 turns that edge off.
type Edges struct {
	Enabled bool `json:"enabled"`
	Top     int  `json:"top"` // 0..10_000 px
	Right   int  `json:"right"`
	Bottom  int  `json:"bottom"`
	Left    int  `json:"left"`
	// OuterOnly ignores edges shared with a neighbouring monitor, so moving the
	// cursor from one screen to another does not stop the run.
	OuterOnly bool `json:"outerOnly"`
}

// ProcessEntry is one app in the allow/block list.
type ProcessEntry struct {
	Exe     string `json:"exe"` // lowercase file name with ".exe"
	Enabled bool   `json:"enabled"`
}

// ProcessList limits runs by the foreground app.
type ProcessList struct {
	Enabled bool           `json:"enabled"`
	Mode    ListMode       `json:"mode"`
	Entries []ProcessEntry `json:"entries"`
}

// Profile is everything a preset snapshots.
type Profile struct {
	Cadence      Cadence     `json:"cadence"`
	Input        InputKind   `json:"input"`
	Button       MouseButton `json:"button"`
	Key          string      `json:"key"` // hotkey.KeyName spelling: "A", "F5", "Space", "Num5"
	Uppercase    bool        `json:"uppercase"`
	Press        PressMode   `json:"press"`
	DutyPercent  float64     `json:"dutyPercent"` // 0..100 of the interval held down (capped at high rates)
	HoldGapMs    int         `json:"holdGapMs"`   // 0..600_000; mouse hold mode: wait after each release
	DoubleClick  bool        `json:"doubleClick"`
	DoubleGapMs  int         `json:"doubleGapMs"` // gap between the two clicks of a double click
	Jitter       Jitter      `json:"jitter"`
	Limit        Limit       `json:"limit"`
	ClickPoints  ClickPoints `json:"clickPoints"`
	Corners      Corners     `json:"corners"`
	Edges        Edges       `json:"edges"`
	ZonesEnabled bool        `json:"zonesEnabled"`
	ProcessList  ProcessList `json:"processList"`
}

// StopZone is a screen rectangle with an action (see ZoneAction).
type StopZone struct {
	ID     string     `json:"id"`
	X      int32      `json:"x"` // physical virtual-desktop px
	Y      int32      `json:"y"`
	W      int32      `json:"w"` // >= 1
	H      int32      `json:"h"` // >= 1
	Action ZoneAction `json:"action"`
}

// Preset is a named Profile.
type Preset struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`      // trimmed, 1..40 chars, unique ignoring case
	CreatedAt string  `json:"createdAt"` // RFC 3339
	UpdatedAt string  `json:"updatedAt"`
	Profile   Profile `json:"profile"`
}

// Settings is %AppData%\toru\clicker.json. The two clicker hotkeys
// (clickerToggle, clickerArm) live with the other shortcuts in settings.json.
type Settings struct {
	Version int `json:"version"` // 1
	// Armed gates the clicker hotkeys: disarmed, the toggle combo passes through
	// to other apps and a running clicker stops.
	Armed              bool       `json:"armed"`
	HotkeyMode         HotkeyMode `json:"hotkeyMode"`
	Profile            Profile    `json:"profile"`
	StopZones          []StopZone `json:"stopZones"`
	StopOnTaskSwitcher bool       `json:"stopOnTaskSwitcher"` // Alt+Tab / Win+Tab stops a run
	ShowSafetyOverlay  bool       `json:"showSafetyOverlay"`  // flash the failsafe areas when they change
	ShowIndicator      bool       `json:"showIndicator"`      // small "Clicking" pill while running
	ToastOnStop        bool       `json:"toastOnStop"`        // toast with the reason when a failsafe stops a run
	StartDelayMs       int        `json:"startDelayMs"`       // countdown for the page's Start button, 0..10_000
	HighRate           bool       `json:"highRate"`           // allow up to 1000 clicks/s instead of 500
	NewPointClicks     int        `json:"newPointClicks"`     // defaults for picked points
	NewPointRadius     int        `json:"newPointRadius"`
	Presets            []Preset   `json:"presets"`
	ActivePresetID     string     `json:"activePresetId"` // "" = none
	// Introduced is set the first time the Auto Clicker page opens. Until
	// then the clicker stays disarmed, so a fresh install doesn't claim its
	// start/stop combo from other apps for a feature nobody has used.
	Introduced bool `json:"introduced"`
}

// RunState is the controller's phase.
type RunState string

const (
	StateIdle      RunState = "idle"
	StateCountdown RunState = "countdown"
	StateRunning   RunState = "running"
	StatePaused    RunState = "paused"
)

// PauseCause says why a run is paused.
type PauseCause string

const (
	PauseNone PauseCause = ""
	PauseUser PauseCause = "user"
	PauseZone PauseCause = "zone"
)

// StopCode says why the last run ended.
type StopCode string

const (
	StopNone         StopCode = ""
	StopUser         StopCode = "user"
	StopHotkey       StopCode = "hotkey"
	StopHoldRelease  StopCode = "holdRelease"
	StopTray         StopCode = "tray"
	StopDisarmed     StopCode = "disarmed"
	StopOverlay      StopCode = "overlay"    // Toru's capture overlay opened
	StopKeyCapture   StopCode = "keyCapture" // a shortcut or key is being recorded
	StopPick         StopCode = "pick"       // a point/zone pick started
	StopCorner       StopCode = "corner"
	StopEdge         StopCode = "edge"
	StopInZone       StopCode = "zoneStop"
	StopZoneLeft     StopCode = "zoneLeft" // cursor left the start zone that started the run
	StopTaskSwitcher StopCode = "taskSwitcher"
	StopProcessList  StopCode = "processList"
	StopClickLimit   StopCode = "clickLimit"
	StopTimeLimit    StopCode = "timeLimit"
	StopPointsDone   StopCode = "pointsDone"
	StopShutdown     StopCode = "shutdown"
	StopError        StopCode = "error"
)

// Status is the live state the page and tray show.
type Status struct {
	State       RunState   `json:"state"`
	PauseCause  PauseCause `json:"pauseCause"`
	CountdownMs int        `json:"countdownMs"`
	Clicks      int64      `json:"clicks"`
	ElapsedMs   int64      `json:"elapsedMs"`
	ActivePoint int        `json:"activePoint"` // index into Points, -1 when not using points
	PointTick   int        `json:"pointTick"`   // clicks done at the active point
	StopCode    StopCode   `json:"stopCode"`
	StopReason  string     `json:"stopReason"` // display text, e.g. "Top-left corner failsafe"
	Warning     string     `json:"warning"`    // e.g. elevated target, low-precision timer
	Armed       bool       `json:"armed"`
	Hotkey      string     `json:"hotkey"` // display label of the clickerToggle binding, "" = unbound
}

// Point is a screen position in physical virtual-desktop px.
type Point struct {
	X int32 `json:"x"`
	Y int32 `json:"y"`
}

// ProcessInfo is one row of the process picker.
type ProcessInfo struct {
	Exe     string `json:"exe"`
	Title   string `json:"title"`   // window title, <= 35 chars
	IconURL string `json:"iconUrl"` // data:image/png;base64,... or ""
	Running bool   `json:"running"` // false for saved entries that are not running now
}

// Stats are lifetime totals in clicker-stats.json.
type Stats struct {
	Clicks  int64   `json:"clicks"`
	Seconds float64 `json:"seconds"`
	Runs    int64   `json:"runs"`
}

// PickEvent reports the point/zone picker's lifecycle on EventPick.
type PickEvent struct {
	Kind  string `json:"kind"`  // "point" | "zone"
	Phase string `json:"phase"` // "active" | "added" | "deleted" | "done" | "cancelled"
	Index int    `json:"index"` // affected point/zone, -1 when none
}

// Event names. EventStatus/EventSettings/EventPick are registered with typed
// payloads in main.go; EventOpen carries none and is deliberately unregistered.
const (
	EventStatus   = "clicker:status"
	EventSettings = "clicker:settings"
	EventPick     = "clicker:pick"
	EventOpen     = "clicker:open"
)

// Error codes. Service errors read "clicker: <CODE>: <message>" so the page can
// map them; the message is already user-facing.
const (
	ErrAlreadyRunning = "ALREADY_RUNNING"
	ErrZeroRate       = "ZERO_RATE"
	ErrNoKey          = "NO_KEY"
	ErrUnknownKey     = "UNKNOWN_KEY"
	ErrHotkeyConflict = "HOTKEY_CONFLICT"
	ErrDisarmed       = "DISARMED"
	ErrPickActive     = "PICK_ACTIVE"
	ErrHookFailed     = "HOOK_FAILED"
	ErrWinAPI         = "WIN_API"
	ErrPresetName     = "PRESET_NAME"
	ErrPresetLocked   = "PRESET_LOCKED"
	ErrPresetNotFound = "PRESET_NOT_FOUND"
	ErrImportFormat   = "IMPORT_FORMAT"
	ErrNotImplemented = "NOT_IMPLEMENTED"
)

// PresetFileMarker identifies an exported Toru preset file. Import also accepts
// BlurPresetMarker files (Blur AutoClicker exports) and maps their keys.
const (
	PresetFileMarker = "toru-clicker-preset"
	BlurPresetMarker = "blur-autoclicker-preset"
)

// DefaultSettings is a fresh install's configuration.
func DefaultSettings() Settings {
	return Settings{
		Version:            1,
		Armed:              false, // armed by PageOpened on first use
		HotkeyMode:         HotkeyToggle,
		Profile:            DefaultProfile(),
		StopZones:          []StopZone{},
		StopOnTaskSwitcher: true,
		ShowSafetyOverlay:  true,
		ShowIndicator:      true,
		ToastOnStop:        true,
		StartDelayMs:       3000,
		NewPointClicks:     1,
		NewPointRadius:     0,
		Presets:            []Preset{},
	}
}

// DefaultProfile is the out-of-the-box clicking setup: 25 left clicks a second.
func DefaultProfile() Profile {
	return Profile{
		Cadence:     Cadence{Mode: CadenceRate, Rate: 25, Unit: UnitSecond, IntervalMs: 40},
		Input:       InputMouse,
		Button:      ButtonLeft,
		Key:         "",
		Press:       PressClick,
		DutyPercent: 45,
		HoldGapMs:   100,
		DoubleGapMs: 60,
		Jitter:      Jitter{Enabled: false, Percent: 35},
		Limit:       Limit{Kind: LimitNone, Clicks: 1000, Seconds: 60},
		ClickPoints: ClickPoints{Glide: true, Points: []ClickPoint{}},
		Corners:     Corners{Enabled: true, TL: 50, TR: 50, BL: 50, BR: 50},
		Edges:       Edges{Enabled: true, Top: 40, Right: 40, Bottom: 40, Left: 40, OuterOnly: true},
		ProcessList: ProcessList{Mode: ListBlock, Entries: []ProcessEntry{}},
	}
}
