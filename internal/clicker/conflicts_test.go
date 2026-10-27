package clicker

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/hotkey"
)

func keyProfile(key string, upper bool) Profile {
	p := DefaultProfile()
	p.Input, p.Key, p.Uppercase = InputKeyboard, key, upper
	return normalizeProfile(p, false)
}

func TestConflictsAutoPressKey(t *testing.T) {
	k := newFakeKeys()
	k.conflict[hotkey.Binding{Key: hotkey.FKey(8)}] = "Start / stop auto clicker"
	k.conflict[hotkey.Binding{Shift: true, Key: 'S'}] = "Open capture overlay"

	if err := checkConflicts(keyProfile("F8", false), k, false); err == nil ||
		!strings.Contains(err.Error(), "clicker: "+ErrHotkeyConflict+": ") || !strings.Contains(err.Error(), "Start / stop auto clicker") {
		t.Fatalf("bare key conflict: %v", err)
	}
	if err := checkConflicts(keyProfile("S", false), k, false); err != nil {
		t.Fatalf("lowercase s with Caps off injects no Shift: %v", err)
	}
	if err := checkConflicts(keyProfile("S", true), k, false); err == nil || !strings.Contains(err.Error(), "Shift + S") {
		t.Fatalf("uppercase S: %v", err)
	}
	if err := checkConflicts(keyProfile("S", false), k, true); err == nil {
		t.Fatal("lowercase s with Caps on wraps Shift and should conflict")
	}
	if err := checkConflicts(keyProfile("Q", true), k, false); err != nil {
		t.Fatalf("no conflict expected: %v", err)
	}
	if err := checkConflicts(keyProfile("F8", false), nil, false); err != nil {
		t.Fatal("no hotkey manager must mean no conflicts")
	}
}

func TestConflictsMouseButtonVsMouseHotkey(t *testing.T) {
	k := newFakeKeys()
	mouse4, ok := hotkey.ParseKeyName("Mouse4")
	if !ok {
		t.Skip("hotkey has no Mouse4 key yet (S5)")
	}
	k.bindings[actionToggle] = hotkey.Binding{Key: mouse4}
	p := DefaultProfile()
	p.Button = ButtonX1
	if err := checkConflicts(p, k, false); err == nil || !strings.Contains(err.Error(), ErrHotkeyConflict) {
		t.Fatalf("x1 vs Mouse4 toggle: %v", err)
	}
	p.Button = ButtonLeft
	if err := checkConflicts(p, k, false); err != nil {
		t.Fatalf("left vs Mouse4: %v", err)
	}
}

func TestBindingLabel(t *testing.T) {
	cases := []struct {
		b    hotkey.Binding
		want string
	}{
		{hotkey.Binding{}, ""},
		{hotkey.DefaultClickerToggle, "Ctrl + Shift + Y"},
		{hotkey.Binding{Ctrl: true, Shift: true, Sides: hotkey.SideLCtrl, Key: 'A', Extra: [4]rune{'S'}}, "LCtrl + Shift + A + S"},
		{hotkey.Binding{Alt: true, Sides: hotkey.SideRAlt, Key: hotkey.FKey(10)}, "RAlt + F10"},
		{hotkey.Binding{Win: true, Sides: hotkey.SideLWin | hotkey.SideRWin, Key: 'K'}, "Win + K"},
	}
	for _, c := range cases {
		if got := bindingLabel(c.b); got != c.want {
			t.Errorf("bindingLabel(%+v) = %q, want %q", c.b, got, c.want)
		}
	}
}

func TestIndicatorText(t *testing.T) {
	cases := []struct {
		st   Status
		want string
	}{
		{Status{State: StateIdle}, ""},
		{Status{State: StateCountdown, CountdownMs: 2100}, "Starting in 3"},
		{Status{State: StateRunning, Clicks: 1234, Hotkey: "Ctrl + Shift + Y"}, "Clicking · 1,234 · Ctrl+Shift+Y to stop"},
		{Status{State: StateRunning, Clicks: 5}, "Clicking · 5"},
		{Status{State: StatePaused, Clicks: 1234567}, "Paused · 1,234,567"},
	}
	for _, c := range cases {
		if got := indicatorText(c.st); got != c.want {
			t.Errorf("indicatorText(%+v) = %q, want %q", c.st, got, c.want)
		}
	}
}
