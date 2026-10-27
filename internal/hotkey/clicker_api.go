package hotkey

import "fmt"

// Auto clicker support (internal/clicker).

// PhysicallyDown reports whether vk is held down by the user right now. It
// ignores input Toru injected itself (tagged with inputtag.Toru), so the auto
// clicker's own presses never count. The generic Shift/Ctrl/Alt codes mean
// either side.
//
// The exact rule (see keyTable in keystate.go):
//   - While the hook that sees vk isn't installed (the keyboard hook; the
//     mouse hook for Middle/Mouse4/Mouse5, which only runs while a binding
//     uses a mouse button), or for keys no hook tracks (left/right mouse
//     buttons): GetAsyncKeyState.
//   - Otherwise the hook's physical-key table for a key whose down Toru's hook
//     swallowed (a matched hotkey: GetAsyncKeyState never saw it), and, while
//     SetInjecting(true), for a key Toru has injected during the run.
//   - Otherwise GetAsyncKeyState. This keeps working while an elevated window
//     has focus and the hooks are not called (a release there is still seen).
//
// The clicker's hold-mode guard polls PhysicallyDown(TriggerVK(binding)); the
// conflict guard makes sure the clicker never injects that key.
func PhysicallyDown(vk uint16) bool { return phys.physicallyDown(uint32(vk)) }

// TriggerVK returns the virtual-key code of b's main Key (0 when unbound).
func TriggerVK(b Binding) uint16 { return uint16(triggerVK(b.Key)) }

// SetInjecting tells the hooks a clicker run is injecting input, so the keys
// it injects are judged by the physical-key table only (GetAsyncKeyState
// sees injected Shift). Turn it on before the run's first SendInput and off
// after its last key-up.
func SetInjecting(on bool) { phys.setInjecting(on) }

// Conflicts reports the first other registered, active action whose binding
// would fire on b (b pressed as a combo, matched exactly as the hooks match),
// e.g. the clicker's auto-press key against Toru's hotkeys. action is the human
// label.
func (m *Manager) Conflicts(b Binding) (action string, conflict bool) {
	return m.ConflictsExcept(b, "")
}

// ConflictsExcept is Conflicts that skips the action id except (the binding's
// own action, when checking one of Toru's bindings against the rest).
func (m *Manager) ConflictsExcept(b Binding, except string) (action string, conflict bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.order {
		x := m.bindings[id]
		if id == except || m.inactive[id] || x.Key == 0 {
			continue
		}
		if firesOn(x, b) {
			return m.labels[id], true
		}
	}
	return "", false
}

// ClearShortcut unbinds action (persisted as an explicit unbound entry, so it
// stays unbound after a restart instead of falling back to the default) so its
// combo is no longer intercepted.
func (s *HotkeyService) ClearShortcut(action string) error {
	if _, ok := s.mgr.defaultFor(action); !ok {
		return fmt.Errorf("unknown action %q", action)
	}
	if err := persistBinding(action, Binding{}); err != nil {
		return fmt.Errorf("save shortcut: %w", err)
	}
	s.mgr.SetBinding(action, Binding{})
	return nil
}

// SetOnRecordStart registers a callback run when RecordShortcut begins, so the
// auto clicker can stop before the user presses keys.
//
//wails:ignore
func (s *HotkeyService) SetOnRecordStart(fn func()) {
	s.mu.Lock()
	s.onRecordStart = fn
	s.mu.Unlock()
}
