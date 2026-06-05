// Package hotkey owns global hotkey registration. The real implementation is a
// thin in-house RegisterHotKey/WM_HOTKEY wrapper (the popular Go lib is
// unmaintained, and owning it lets us control the message loop / thread
// affinity so it cooperates with Wails' message pump). This file is the stub
// the Phase 0 spike replaces.
package hotkey

import "sync"

// Binding is a parsed hotkey combo, e.g. {Ctrl:true, Shift:true, Key:'2'}.
type Binding struct {
	Ctrl, Shift, Alt, Win bool
	Key                   rune
}

// Default Toru hotkeys. All avoid OS-reserved combos (Win+Shift+S etc.).
var (
	DefaultOverlay   = Binding{Ctrl: true, Shift: true, Key: '2'} // control-bar overlay
	DefaultShot      = Binding{Ctrl: true, Shift: true, Key: '1'} // instant region screenshot
	DefaultRecord    = Binding{Ctrl: true, Shift: true, Key: '3'} // instant region recording
	DefaultStopVideo = Binding{Ctrl: true, Shift: true, Key: '0'} // stop active recording
)

// Registrar registers global hotkeys and invokes callbacks when pressed.
type Registrar struct {
	mu        sync.Mutex
	callbacks map[string]func()
}

// New returns an empty Registrar.
func New() *Registrar { return &Registrar{callbacks: map[string]func(){}} }

// Register binds a combo to a callback.
//
// TODO(spike): implement via RegisterHotKey on a dedicated OS thread with its
// own GetMessage loop; surface RegisterHotKey conflict errors to a rebind UI.
func (r *Registrar) Register(id string, _ Binding, cb func()) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbacks[id] = cb
	return nil
}

// Trigger invokes a registered callback by id (used by tests / the dev hub
// until real OS hotkeys are wired).
func (r *Registrar) Trigger(id string) {
	r.mu.Lock()
	cb := r.callbacks[id]
	r.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// Close unregisters everything.
func (r *Registrar) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.callbacks = map[string]func(){}
}
