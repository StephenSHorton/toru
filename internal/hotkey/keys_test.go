package hotkey

import "testing"

func TestKeyNameRoundTrip(t *testing.T) {
	for _, name := range []string{"S", "7", "F1", "F10", "F24", "PageUp", "Num5", "Num+", "PrintScreen", "Space", "-", "[", `\`} {
		r, ok := ParseKeyName(name)
		if !ok {
			t.Fatalf("ParseKeyName(%q) rejected", name)
		}
		if got := KeyName(r); got != name {
			t.Errorf("KeyName(ParseKeyName(%q)) = %q", name, got)
		}
	}
	if r, ok := ParseKeyName("f10"); !ok || r != FKey(10) {
		t.Errorf("lowercase f10 should parse to FKey(10), got %v %v", r, ok)
	}
	for _, bad := range []string{"", "F0", "F25", "FF", "AB", "Tab", "Num10"} {
		if _, ok := ParseKeyName(bad); ok {
			t.Errorf("ParseKeyName(%q) should be rejected", bad)
		}
	}
}

func TestValidateShortcutFunctionKeys(t *testing.T) {
	if err := validateShortcut(Shortcut{Key: "F9"}); err != nil {
		t.Errorf("a bare F-key is a valid bind: %v", err)
	}
	if err := validateShortcut(Shortcut{Key: "R"}); err == nil {
		t.Error("a bare letter must still need a modifier")
	}
	b, err := shortcutToBinding(Shortcut{Alt: true, Key: "F10"})
	if err != nil || b != DefaultReplaySave {
		t.Errorf("Alt+F10 -> %+v, %v; want %+v", b, err, DefaultReplaySave)
	}
	if sc := bindingToShortcut("replaySave", "Save instant replay", DefaultReplaySave); sc.Key != "F10" || !sc.Alt {
		t.Errorf("wire shortcut = %+v", sc)
	}
}

func TestSetActiveTogglesInterception(t *testing.T) {
	m := New()
	m.SetActive("replaySave", false)
	if !m.inactive["replaySave"] {
		t.Fatal("SetActive(false) should mark the action inactive")
	}
	m.SetActive("replaySave", true)
	if m.inactive["replaySave"] {
		t.Fatal("SetActive(true) should re-arm the action")
	}
}

func TestNamedKeysParseCaseInsensitive(t *testing.T) {
	if r, ok := ParseKeyName("pageup"); !ok || KeyName(r) != "PageUp" {
		t.Errorf("pageup -> %v %v", r, ok)
	}
}

func TestBareKeyRules(t *testing.T) {
	for _, ok := range []string{"F9", "PrintScreen", "Num5", "PageUp", "Insert", "Pause"} {
		if err := validateShortcut(Shortcut{Key: ok}); err != nil {
			t.Errorf("bare %s should be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"R", "7", "Space", "-", "Delete", "Left"} {
		if err := validateShortcut(Shortcut{Key: bad}); err == nil {
			t.Errorf("bare %s types text / moves the caret and must need a modifier", bad)
		}
		if err := validateShortcut(Shortcut{Ctrl: true, Key: bad}); err != nil {
			t.Errorf("Ctrl+%s should be allowed: %v", bad, err)
		}
	}
}

func TestKeyForVK(t *testing.T) {
	cases := map[uint32]string{'A': "A", '5': "5", 0x70: "F1", 0x79: "F10", 0x87: "F24", 0x21: "PageUp", 0x6B: "Num+", 0xDB: "["}
	for vk, want := range cases {
		r, ok := keyForVK(vk)
		if !ok || KeyName(r) != want {
			t.Errorf("keyForVK(%#x) = %q %v, want %q", vk, KeyName(r), ok, want)
		}
		if triggered, _ := ParseKeyName(want); triggered != r {
			t.Errorf("%s: recorded rune %v != parsed rune %v", want, r, triggered)
		}
	}
	for _, vk := range []uint32{0x09, 0x0D, 0x14, 0xA0, 0x5B} { // Tab, Enter, CapsLock, LShift, LWin
		if _, ok := keyForVK(vk); ok {
			t.Errorf("keyForVK(%#x) should be unsupported", vk)
		}
	}
	var b Binding
	for _, vk := range []uint32{0xA2, 0xA5, 0xA1, 0x5C} { // LCtrl, RAlt, RShift, RWin
		applyModifierVK(&b, vk, true)
	}
	if !b.Ctrl || !b.Alt || !b.Shift || !b.Win {
		t.Errorf("modifiers = %+v", b)
	}
	applyModifierVK(&b, 0xA4, false)
	if b.Alt {
		t.Error("LAlt up should clear Alt")
	}
}

func TestComboLabel(t *testing.T) {
	if got := comboLabel(Binding{Ctrl: true, Alt: true, Key: FKey(10)}); got != "Ctrl + Alt + F10" {
		t.Errorf("comboLabel = %q", got)
	}
}
