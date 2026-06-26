package hotkey

import "testing"

// TestEscapeArmedDefaultOff is the safety-critical invariant: a global Escape must
// NEVER be intercepted unless the overlay explicitly armed it. ArmEscape toggles
// the flag the LL hook reads on every Escape key-down.
func TestEscapeArmedDefaultOff(t *testing.T) {
	m := New()
	if m.escapeArmed() {
		t.Fatal("escape must NOT be armed on a fresh Manager")
	}
	m.ArmEscape(true)
	if !m.escapeArmed() {
		t.Fatal("ArmEscape(true) should arm")
	}
	m.ArmEscape(false)
	if m.escapeArmed() {
		t.Fatal("ArmEscape(false) should disarm")
	}
}

// TestEscapeActionDispatch verifies the escape callback is stored under the
// reserved action id so the dispatch path (which the LL hook feeds via m.sig)
// runs it. Trigger exercises that same callbacks lookup synchronously.
func TestEscapeActionDispatch(t *testing.T) {
	m := New()
	fired := false
	m.SetEscapeAction(func() { fired = true })
	m.Trigger(escapeAction)
	if !fired {
		t.Fatal("escape action was not registered under escapeAction / not dispatched")
	}
}

// TestEscapeActionNotAUserShortcut guards that the reserved escape action never
// leaks into GetShortcuts (it has no binding and is not in `order`), so it can't
// be rebound or matched against a real keystroke.
func TestEscapeActionNotAUserShortcut(t *testing.T) {
	m := New()
	m.SetEscapeAction(func() {})
	if _, ok := m.Binding(escapeAction); ok {
		t.Fatal("escapeAction must not have a key Binding")
	}
	for _, row := range m.snapshot() {
		if row.Action == escapeAction {
			t.Fatal("escapeAction must not appear in the shortcut snapshot")
		}
	}
}
