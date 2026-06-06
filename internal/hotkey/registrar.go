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
// Known v1 limitations (documented, not bugs):
//   - Trigger keys are A-Z and 0-9 only; F-keys, punctuation, etc. are future.
//   - Modifier matching is a SUPERSET test: a binding matches (and swallows) any
//     chord where every modifier it sets is down, even if EXTRA modifiers are also
//     down (e.g. {Ctrl,'S'} also fires on Ctrl+Shift+S). This is conventional,
//     forgiving global-hotkey behavior and is harmless for the single-action v1; if
//     future actions need to distinguish e.g. Ctrl+Shift+1 from Ctrl+Shift+Alt+1,
//     add an exact-match check (require unset modifiers to be UP) in the proc.
package hotkey

import "sync"

// Binding is a parsed hotkey combo, e.g. {Win:true, Shift:true, Key:'S'}. Key is
// the ASCII-uppercase rune of the trigger key; v1 supports A-Z and 0-9 only.
type Binding struct {
	Ctrl, Shift, Alt, Win bool
	Key                   rune
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
)

// actionMeta maps a stable action id to its human label + default binding. It
// lets Register populate labels/defaults from just the id (keeping the
// Register(id, Binding, func()) signature stable for main.go). Unknown ids fall
// back to label==id and default==the binding passed to Register.
var actionMeta = map[string]struct {
	Label   string
	Default Binding
}{
	"overlay": {"Open capture overlay", DefaultOverlay},
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
	order     []string           // action ids in registration order (stable GetShortcuts)

	hook     uintptr // HHOOK (0 off-Windows / not installed)
	threadID uint32  // hook OS-thread id (for WM_QUIT)

	sig     chan string   // hook proc -> dispatch goroutine (buffered)
	stop    chan struct{} // closed by Close to end the dispatch goroutine
	started bool          // dispatch goroutine + hook installed
	closed  bool          // guards double Close
	closing bool          // set by stopHook; the install goroutine self-unhooks if
	//                       a stop landed before SetWindowsHookExW published the hook
}

// New returns an idle Manager. The engine starts lazily on the first Register.
func New() *Manager {
	return &Manager{
		bindings:  map[string]Binding{},
		callbacks: map[string]func(){},
		labels:    map[string]string{},
		defaults:  map[string]Binding{},
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
