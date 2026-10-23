package hotkey

import "testing"

func TestKeyNameRoundTrip(t *testing.T) {
	for _, name := range []string{"S", "7", "F1", "F10", "F24"} {
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
	for _, bad := range []string{"", "F0", "F25", "FF", "AB", "-"} {
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
