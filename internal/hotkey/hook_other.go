//go:build !windows

package hotkey

import "errors"

// installHook / stopHook are no-ops off Windows so the package builds (and the
// HotkeyService binding generates) on any OS. The dispatch goroutine + Trigger()
// still work; only the OS-level WH_KEYBOARD_LL engine is Windows-only.
func (m *Manager) installHook() {}
func (m *Manager) stopHook()    {}

func (m *Manager) beginRecord() (<-chan recordResult, error) {
	return nil, errors.New("recording shortcuts is only supported on Windows")
}

func endRecord() {}
