// Package hotkey owns Toru's global hotkeys. Unlike the original RegisterHotKey
// spike, the engine is a LOW-LEVEL KEYBOARD HOOK (WH_KEYBOARD_LL): it is the only
// reliable way to capture AND swallow arbitrary Win-key combos — notably
// Win+Shift+S, which the Windows shell (Snipping Tool) owns and which
// RegisterHotKey cannot claim. On a matching key-down the hook fires our action
// and RETURNS 1 to swallow the key so the OS snip never fires.
//
// This file holds the CROSS-PLATFORM half: the Binding type, the Default* combos,
// the single Manager struct (all fields, shared by both platforms), the bindings
// map + mutex, and the dispatch goroutine. It contains NO syscalls. The actual
// SetWindowsHookEx engine lives in hook_windows.go (//go:build windows); a no-op
// stub lives in hook_other.go (//go:build !windows). Splitting only
// installHook/stopHook behind build tags keeps ONE Manager definition that
// compiles everywhere.
//
// Trigger keys are A-Z, 0-9, F1-F24 and the named keys in keys.go
// (navigation, numpad, punctuation, Middle/Mouse4/Mouse5, sided modifiers).
// Modifier matching is EXACT by default: a binding fires only when its
// modifiers, and no others, are down, so e.g. Alt+F10 and Ctrl+Alt+F10 can be
// different actions. A binding may also name modifier sides, hold up to five
// keys as a chord, or match loosely (match.go); those are matched by the hooks
// only. The hooks skip input Toru injected itself (internal/inputtag) and keep
// a physical key-state table for the auto clicker (keystate.go).
package hotkey

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
)

// escapeAction is the reserved internal action id fired when Escape is pressed
// while the capture overlay is armed (see ArmEscape). It is NOT a user-facing
// shortcut: it carries no Binding (so the LL-hook key-match loop never matches a
// real keystroke to it) and is never added to `order` (so GetShortcuts hides it).
const escapeAction = "__overlay_escape"

// Binding is a parsed hotkey combo, e.g. {Win:true, Shift:true, Key:'S'}. Key is
// the ASCII-uppercase rune of the trigger key (A-Z, 0-9), FKey(n) for F1-F24,
// or namedKeyBase+vk for a named key (keys.go).
type Binding struct {
	Ctrl, Shift, Alt, Win bool
	Key                   rune
	// Sides narrows required modifiers to one side (LCtrl, RShift, ...). A set
	// bit implies its generic flag above is also set. Zero = either side.
	Sides SideMask
	// Extra are more keys held together with Key (a chord, up to 5 keys in
	// all), sorted ascending, 0 = unused. Kept as an array so Binding stays
	// comparable with ==.
	Extra [4]rune
	// Loose lets the combo fire while other, unlisted modifiers are also held
	// (default: the modifiers must match exactly).
	Loose bool
}

// SideMask bits for Binding.Sides.
type SideMask uint8

const (
	SideLCtrl SideMask = 1 << iota
	SideRCtrl
	SideLShift
	SideRShift
	SideLAlt
	SideRAlt
	SideLWin
	SideRWin
)

// fKeyBase maps F1-F24 into the Unicode private-use area so a function key
// fits the same rune-typed Binding.Key as letters and digits: FKey(n) ==
// fKeyBase+n. It never collides with a real A-Z/0-9 key.
const fKeyBase rune = 0xE000

// FKey returns the Binding.Key value for function key Fn (1..24).
func FKey(n int) rune { return fKeyBase + rune(n) }

// fKeyNumber returns n for an FKey(n) rune, or 0 if r isn't a function key.
func fKeyNumber(r rune) int {
	if n := int(r - fKeyBase); n >= 1 && n <= 24 {
		return n
	}
	return 0
}

// KeyName is the wire/persisted spelling of a trigger key: "S", "7", "F10",
// "PageUp", "Num5", "[".
func KeyName(r rune) string {
	if n := fKeyNumber(r); n > 0 {
		return "F" + strconv.Itoa(n)
	}
	if k, ok := namedKeyFor(r); ok {
		return k.name
	}
	if r == 0 {
		return ""
	}
	return string(unicode.ToUpper(r))
}

// ParseKeyName is the inverse of KeyName (case-insensitive). ok=false for
// anything outside A-Z, 0-9, F1-F24 and the named keys.
func ParseKeyName(s string) (rune, bool) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if k, ok := namedByName[s]; ok {
		return namedKeyBase + rune(k.vk), true
	}
	if len(s) >= 2 && s[0] == 'F' {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 1 && n <= 24 {
			return FKey(n), true
		}
		return 0, false
	}
	rs := []rune(s)
	if len(rs) != 1 || !validKeyRune(rs[0]) {
		return 0, false
	}
	return rs[0], true
}

// Default Toru hotkeys.
//
// DefaultOverlay is Win+Shift+S — the combo the user asked for. It overrides the
// Windows Snipping Tool shortcut while Toru runs (the LL hook swallows it). The
// other defaults document future actions and are intentionally kept (unused by
// the v1 wiring) so nothing that might reference them needs touching.
var (
	DefaultOverlay   = Binding{Win: true, Shift: true, Key: 'S'}  // open capture overlay
	DefaultShot      = Binding{Ctrl: true, Shift: true, Key: '1'} // instant region screenshot
	DefaultRecord    = Binding{Ctrl: true, Shift: true, Key: '3'} // instant region recording
	DefaultStopVideo = Binding{Ctrl: true, Shift: true, Key: '0'} // stop active recording

	// DefaultReplaySave matches NVIDIA ShadowPlay's "save instant replay" so
	// gamers' muscle memory carries over.
	DefaultReplaySave = Binding{Alt: true, Key: FKey(10)}
	// Steam Game Recording's defaults: start/stop a manual recording, and
	// add a timeline marker.
	DefaultRecordToggle   = Binding{Ctrl: true, Key: FKey(11)}
	DefaultTimelineMarker = Binding{Ctrl: true, Key: FKey(12)}

	// Auto clicker: start/stop (Ctrl+Shift+Y, close to Blur AutoClicker's
	// Ctrl+Y without eating Redo) and arm/disarm (unbound by default).
	DefaultClickerToggle = Binding{Ctrl: true, Shift: true, Key: 'Y'}
	DefaultClickerArm    = Binding{}
)

// actionMeta maps a stable action id to its human label + default binding. It
// lets Register populate labels/defaults from just the id (keeping the
// Register(id, Binding, func()) signature stable for main.go). Unknown ids fall
// back to label==id and default==the binding passed to Register.
var actionMeta = map[string]struct {
	Label   string
	Default Binding
}{
	"overlay":        {"Open capture overlay", DefaultOverlay},
	"replaySave":     {"Save instant replay", DefaultReplaySave},
	"recordToggle":   {"Start / stop recording", DefaultRecordToggle},
	"timelineMarker": {"Add timeline marker", DefaultTimelineMarker},
	"clickerToggle":  {"Start / stop auto clicker", DefaultClickerToggle},
	"clickerArm":     {"Arm / disarm auto clicker", DefaultClickerArm},
}

// shortcutRow is the internal snapshot row used by service.go's GetShortcuts. It
// carries the current binding alongside the label + default so the service can
// build a Shortcut without re-reading the manager.
type shortcutRow struct {
	Action string
	Label  string
	B      Binding
	Def    Binding
}

// Manager owns the global-hotkey engine. There is exactly ONE struct definition
// (here) so both platforms share it; the hook/threadID fields are simply unused
// off Windows. The bindings map is read by the LL-hook proc on every key-down and
// written by SetBinding, both under mu, so a rebind takes effect with no reinstall.
type Manager struct {
	mu        sync.Mutex
	bindings  map[string]Binding
	callbacks map[string]func()
	labels    map[string]string  // action -> human label (for GetShortcuts)
	defaults  map[string]Binding // action -> default binding (for ResetShortcut)
	inactive  map[string]bool    // actions whose combo is currently NOT intercepted
	// sysOwned are actions currently registered with RegisterHotKey (Windows);
	// the LL hook leaves their keys alone so the system hotkey fires instead.
	sysOwned map[string]bool
	order    []string // action ids in registration order (stable GetShortcuts)

	hook     uintptr // HHOOK (0 off-Windows / not installed)
	threadID uint32  // hook OS-thread id (for WM_QUIT)

	sig     chan string   // hook proc -> dispatch goroutine (buffered)
	stop    chan struct{} // closed by Close to end the dispatch goroutine
	started bool          // dispatch goroutine + hook installed
	closed  bool          // guards double Close
	closing bool          // set by stopHook; the install goroutine self-unhooks if
	//                       a stop landed before SetWindowsHookExW published the hook

	// escArmed is 1 while the capture overlay is up: the LL hook fires escapeAction
	// on an Escape key-down and (crucially) NEVER swallows Escape. Atomic so the
	// latency-sensitive hook proc reads it without taking m.mu. Toggled by ArmEscape
	// from the overlay lifecycle (armed on engage, disarmed on hide/edit/record).
	escArmed int32
}

// New returns an idle Manager. The engine starts lazily on the first Register.
func New() *Manager {
	return &Manager{
		bindings:  map[string]Binding{},
		callbacks: map[string]func(){},
		labels:    map[string]string{},
		defaults:  map[string]Binding{},
		inactive:  map[string]bool{},
		sig:       make(chan string, 16),
		stop:      make(chan struct{}),
	}
}

// Register records a binding + callback for id and, on the first call, starts the
// engine (installs the LL hook on Windows; no-op start elsewhere). The label and
// default for id come from actionMeta; an unknown id gets label==id and
// default==b. main.go calls Register("overlay", loadedBinding, OpenOverlay).
func (m *Manager) Register(id string, b Binding, cb func()) error {
	m.mu.Lock()
	if _, exists := m.callbacks[id]; !exists {
		m.order = append(m.order, id)
	}
	m.bindings[id] = b
	m.callbacks[id] = cb
	if meta, ok := actionMeta[id]; ok {
		m.labels[id] = meta.Label
		m.defaults[id] = meta.Default
	} else {
		m.labels[id] = id
		m.defaults[id] = b
	}
	needStart := !m.started
	m.mu.Unlock()

	if needStart {
		m.start()
	} else {
		m.syncSystemHotkeys() // a later action may need RegisterHotKey or the mouse hook
	}
	return nil
}

// SetBinding swaps a live binding under the mutex. The hook reads m.bindings under
// the same mutex on every key-down, so the change takes effect immediately with
// NO hook reinstall. Used by HotkeyService.SetShortcut/ResetShortcut after the
// new combo has been validated + persisted.
func (m *Manager) SetBinding(id string, b Binding) {
	m.mu.Lock()
	m.bindings[id] = b
	m.mu.Unlock()
	m.syncSystemHotkeys()
}

// SetActive turns interception of id's combo on or off without forgetting the
// binding. An inactive action's keys pass straight through to the focused app
// (e.g. Alt+F10 reaches the game while instant replay is disabled).
func (m *Manager) SetActive(id string, on bool) {
	m.mu.Lock()
	if on {
		delete(m.inactive, id)
	} else {
		m.inactive[id] = true
	}
	m.mu.Unlock()
	m.syncSystemHotkeys()
}

// Binding returns the current binding for id (ok=false if absent).
func (m *Manager) Binding(id string) (Binding, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.bindings[id]
	return b, ok
}

// snapshot returns a copy of every action's {binding, label, default} in
// registration order. Used by service.go's GetShortcuts; read once under mu.
func (m *Manager) snapshot() []shortcutRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := make([]shortcutRow, 0, len(m.order))
	for _, id := range m.order {
		rows = append(rows, shortcutRow{
			Action: id,
			Label:  m.labels[id],
			B:      m.bindings[id],
			Def:    m.defaults[id],
		})
	}
	return rows
}

// recordResult is what a shortcut recording ends with: the combo pressed, or
// ok=false when it was cancelled (Esc, CancelRecord, timeout).
type recordResult struct {
	b  Binding
	ok bool
}

// Record waits up to timeout for the next key combo pressed anywhere and
// returns it (see HotkeyService.RecordShortcut). While recording, the hook
// swallows every key and fires no actions. ok=false when cancelled or timed
// out; err when the hook isn't running.
func (m *Manager) Record(timeout time.Duration) (Binding, bool, error) {
	ch, err := m.beginRecord()
	if err != nil {
		return Binding{}, false, err
	}
	select {
	case r := <-ch:
		return r.b, r.ok, nil
	case <-time.After(timeout):
		endRecord()
		return Binding{}, false, nil
	}
}

// CancelRecord ends a pending Record (it returns ok=false).
func (m *Manager) CancelRecord() { endRecord() }

// defaultFor returns the registered default binding for id (ok=false if absent).
func (m *Manager) defaultFor(id string) (Binding, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.defaults[id]
	return d, ok
}

// Trigger synchronously runs the callback for id. Kept for tests and the dev hub.
func (m *Manager) Trigger(id string) {
	m.mu.Lock()
	cb := m.callbacks[id]
	m.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// SetEscapeAction registers the callback fired when Escape is pressed while the
// overlay is armed (ArmEscape(true)). It is stored under the reserved escapeAction
// id so the existing dispatch goroutine runs it off the hook thread, exactly like
// a normal hotkey action. Wired once at startup (main.go -> OverlayService.Cancel).
func (m *Manager) SetEscapeAction(cb func()) {
	m.mu.Lock()
	m.callbacks[escapeAction] = cb
	m.mu.Unlock()
}

// ArmEscape toggles whether a global Escape key-down fires the escape action. It
// is armed ONLY while the capture overlay is shown (disarmed on hide / enter-edit
// / record / teardown), so Escape never cancels during annotation or when no
// overlay is up. The LL hook NEVER swallows Escape regardless of this flag, so a
// stuck-true flag can at worst cause a harmless extra Cancel — Escape keeps
// working everywhere else. Safe to call before the engine starts (just sets a
// flag the not-yet-installed hook will read once running).
func (m *Manager) ArmEscape(on bool) {
	var v int32
	if on {
		v = 1
	}
	atomic.StoreInt32(&m.escArmed, v)
}

// escapeArmed reports whether a global Escape should fire the escape action. Read
// lock-free by the LL-hook proc on every Escape key-down.
func (m *Manager) escapeArmed() bool {
	return atomic.LoadInt32(&m.escArmed) == 1
}

// start launches the dispatch goroutine (which runs callbacks off the hook
// thread) and then installs the platform hook. Called once, from the first
// Register.
func (m *Manager) start() {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.mu.Unlock()

	// Dispatch goroutine: receives action ids the hook proc signalled and runs
	// the callback OFF the hook thread, so the proc never blocks and never
	// touches Wails. This is where windowsSvc.OpenOverlay actually runs.
	go func() {
		for {
			select {
			case <-m.stop:
				return
			case action := <-m.sig:
				m.mu.Lock()
				cb := m.callbacks[action]
				m.mu.Unlock()
				if cb != nil {
					cb()
				}
			}
		}
	}()

	m.installHook() // platform method (real on Windows, no-op elsewhere)
}

// Close tears the engine down: unhook + end the message pump (Windows), then end
// the dispatch goroutine. Idempotent.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	started := m.started
	m.mu.Unlock()

	if started {
		m.stopHook() // platform method: unhook + WM_QUIT (no-op elsewhere)
	}
	close(m.stop) // end the dispatch goroutine
}
