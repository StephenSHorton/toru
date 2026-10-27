package sys

import (
	"strconv"
	"testing"

	"github.com/StephenSHorton/toru/internal/hotkey"
)

// sidedNames are S5's additions; hotkey.ParseKeyName accepts them once S5
// lands, so they are skipped in the consistency check until then.
var sidedNames = map[string]bool{"LSHIFT": true, "RSHIFT": true, "LCTRL": true, "RCTRL": true, "LALT": true, "RALT": true}

// TestKeyTableMatchesHotkey: every name sys can press is a name hotkey can
// parse, and every letter/digit/function key hotkey parses resolves here.
func TestKeyTableMatchesHotkey(t *testing.T) {
	for name := range namedVK {
		if sidedNames[name] {
			if _, ok := hotkey.ParseKeyName(name); !ok {
				t.Logf("%s not yet in hotkey (S5)", name)
			}
			continue
		}
		r, ok := hotkey.ParseKeyName(name)
		if !ok {
			t.Errorf("hotkey.ParseKeyName(%q) failed", name)
			continue
		}
		if _, _, ok := vkForName(hotkey.KeyName(r)); !ok {
			t.Errorf("vkForName(KeyName(%q)) failed", name)
		}
	}
	var names []string
	for c := 'A'; c <= 'Z'; c++ {
		names = append(names, string(c))
	}
	for c := '0'; c <= '9'; c++ {
		names = append(names, string(c))
	}
	for n := 1; n <= 24; n++ {
		names = append(names, "F"+strconv.Itoa(n))
	}
	for _, n := range names {
		if _, ok := hotkey.ParseKeyName(n); !ok {
			t.Errorf("hotkey.ParseKeyName(%q) failed", n)
		}
		if _, _, ok := vkForName(n); !ok {
			t.Errorf("vkForName(%q) failed", n)
		}
	}
}

func TestVKForName(t *testing.T) {
	cases := []struct {
		name   string
		vk     uint16
		letter bool
		ok     bool
	}{
		{"a", 'A', true, true},
		{"Z", 'Z', true, true},
		{"7", '7', false, true},
		{"F1", 0x70, false, true},
		{"f24", 0x87, false, true},
		{"F25", 0, false, false},
		{"F05", 0, false, false},
		{"Space", 0x20, false, true},
		{" up ", 0x26, false, true},
		{"num5", 0x65, false, true},
		{"[", 0xDB, false, true},
		{"RAlt", 0xA5, false, true},
		{"MouseMiddle", 0, false, false},
		{"Mouse4", 0, false, false},
		{"", 0, false, false},
		{"!", 0, false, false},
	}
	for _, c := range cases {
		vk, letter, ok := vkForName(c.name)
		if vk != c.vk || letter != c.letter || ok != c.ok {
			t.Errorf("vkForName(%q) = %#x,%v,%v; want %#x,%v,%v", c.name, vk, letter, ok, c.vk, c.letter, c.ok)
		}
	}
}
