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
// Limitation: a hook-only binding's swallowed key (Mouse4, a chord's last key)
// released while an elevated window has focus stays down here until that key
// is pressed and released again, because neither the hook nor
// GetAsyncKeyState saw the release. A hold-mode run on such a binding keeps
// going until then.
//
// The clicker's hold-mode guard polls PhysicallyDown for all of the binding's
// keys (TriggerVKs); the conflict guard makes sure the clicker never
// injects them.
func PhysicallyDown(vk uint16) bool { return phys.physicallyDown(uint32(vk)) }

// TriggerVK returns the virtual-key code of b's main Key (0 when unbound).
func TriggerVK(b Binding) uint16 { return uint16(triggerVK(b.Key)) }

// TriggerVKs returns the virtual-key codes of all of b's chord keys: Key, then
// its Extra keys, including mouse buttons and sided modifiers used as keys
// (empty when unbound). Modifiers (Ctrl/Shift/Alt/Win flags) aren't keys and
// aren't included.
func TriggerVKs(b Binding) []uint16 {
	vks := chordVKs(b)
	out := make([]uint16, len(vks))
	for i, vk := range vks {
		out[i] = uint16(vk)
	}
	return out
}

// SetInjecting tells the hooks a clicker run is injecting input, so the keys
// it injects are judged by the physical-key table only (GetAsyncKeyState
// sees injected Shift). Turn it on before the run's first SendInput and off
// after its last key-up.
//
// While it is on, clickerToggle is taken off RegisterHotKey and matched by
// the hook (see systemHotkeyWant in hook_windows.go), so the stop hotkey
// keeps working while the run wraps presses in injected Shift, but doesn't
// fire over an elevated window until the run ends. Each change asks the hook
// thread to re-apply registrations; a press in the moment before it does is
// still seen by RegisterHotKey.
func SetInjecting(on bool) {
	if phys.setInjecting(on) {
		requestSync()
	}
}

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
	s.bindingChanged(action)
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

// SetOnBindingChange registers a callback run after SetShortcut,
// ResetShortcut or ClearShortcut changes an action's binding, so labels shown
// outside the Shortcuts UI (the clicker's status and pill, the tray) follow.
//
//wails:ignore
func (s *HotkeyService) SetOnBindingChange(fn func(action string)) {
	s.mu.Lock()
	s.onBindingChange = fn
	s.mu.Unlock()
}

// bindingChanged runs the SetOnBindingChange callback for action.
func (s *HotkeyService) bindingChanged(action string) {
	s.mu.Lock()
	fn := s.onBindingChange
	s.mu.Unlock()
	if fn != nil {
		fn(action)
	}
}
