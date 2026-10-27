package hotkey

// Auto clicker support (internal/clicker). S0 contract: the auto clicker work
// (S5) fills these in.

// PhysicallyDown reports whether vk is held down by the user right now. It
// ignores input Toru injected itself (tagged with inputtag.Toru), so the auto
// clicker's own presses never count.
func PhysicallyDown(vk uint16) bool { return false }

// TriggerVK returns the virtual-key code of b's main Key (0 when unbound).
func TriggerVK(b Binding) uint16 { return 0 }

// SetInjecting tells the hooks a clicker run is injecting input, so modifier
// checks use only the physical-key table (not GetAsyncKeyState, which sees
// injected Shift).
func SetInjecting(on bool) {}

// Conflicts reports the first other registered, active action whose binding
// would fire on b (exact combo match), e.g. the clicker's auto-press key against
// Toru's hotkeys. action is the human label.
func (m *Manager) Conflicts(b Binding) (action string, conflict bool) { return "", false }

// ClearShortcut unbinds action (persisted as an empty key) so its combo is
// no longer intercepted.
func (s *HotkeyService) ClearShortcut(action string) error { return nil }

// SetOnRecordStart registers a callback run when RecordShortcut begins, so the
// auto clicker can stop before the user presses keys.
//
//wails:ignore
func (s *HotkeyService) SetOnRecordStart(fn func()) {}
