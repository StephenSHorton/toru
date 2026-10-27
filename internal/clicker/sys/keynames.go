package sys

import "strings"

// Key name to virtual-key code, in hotkey.KeyName's spellings. internal/hotkey
// exports ParseKeyName but not its rune-to-VK mapping, so sys keeps its own
// table; keynames_test.go checks it stays in step with hotkey.ParseKeyName.
// Pure Go so the table is testable on every platform.

// namedVK is every non-alphanumeric key name the clicker can press. The sided
// modifiers are the S5 additions (usable as a pressed key); mouse button names
// (MouseMiddle, Mouse4, Mouse5) are deliberately absent: they are not keys.
var namedVK = map[string]uint16{
	"PAUSE":       0x13,
	"SPACE":       0x20,
	"PAGEUP":      0x21,
	"PAGEDOWN":    0x22,
	"END":         0x23,
	"HOME":        0x24,
	"LEFT":        0x25,
	"UP":          0x26,
	"RIGHT":       0x27,
	"DOWN":        0x28,
	"PRINTSCREEN": 0x2C,
	"INSERT":      0x2D,
	"DELETE":      0x2E,
	"NUM0":        0x60,
	"NUM1":        0x61,
	"NUM2":        0x62,
	"NUM3":        0x63,
	"NUM4":        0x64,
	"NUM5":        0x65,
	"NUM6":        0x66,
	"NUM7":        0x67,
	"NUM8":        0x68,
	"NUM9":        0x69,
	"NUM*":        0x6A,
	"NUM+":        0x6B,
	"NUM-":        0x6D,
	"NUM.":        0x6E,
	"NUM/":        0x6F,
	"SCROLLLOCK":  0x91,
	";":           0xBA,
	"=":           0xBB,
	",":           0xBC,
	"-":           0xBD,
	".":           0xBE,
	"/":           0xBF,
	"`":           0xC0,
	"[":           0xDB,
	`\`:           0xDC,
	"]":           0xDD,
	"'":           0xDE,
	"LSHIFT":      0xA0,
	"RSHIFT":      0xA1,
	"LCTRL":       0xA2,
	"RCTRL":       0xA3,
	"LALT":        0xA4,
	"RALT":        0xA5,
}

// vkForName maps a key name (case-insensitive) to its virtual-key code.
// letter reports A-Z.
func vkForName(name string) (vk uint16, letter bool, ok bool) {
	s := strings.ToUpper(strings.TrimSpace(name))
	if vk, ok := namedVK[s]; ok {
		return vk, false, true
	}
	if len(s) == 1 {
		c := s[0]
		switch {
		case c >= 'A' && c <= 'Z':
			return uint16(c), true, true // VK_A.. equals ASCII
		case c >= '0' && c <= '9':
			return uint16(c), false, true
		}
		return 0, false, false
	}
	if len(s) >= 2 && s[0] == 'F' {
		n := 0
		for _, d := range s[1:] {
			if d < '0' || d > '9' {
				return 0, false, false
			}
			n = n*10 + int(d-'0')
			if n > 24 {
				return 0, false, false
			}
		}
		if n >= 1 && s[1] != '0' {
			return 0x70 + uint16(n-1), false, true // VK_F1..VK_F24
		}
	}
	return 0, false, false
}
