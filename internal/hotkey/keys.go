package hotkey

import "strings"

// namedKeyBase maps trigger keys beyond A-Z/0-9/F1-F24 (navigation, numpad,
// punctuation, …) into the Unicode private-use area by Windows virtual-key
// code: namedKeyBase+vk. Like fKeyBase it never collides with a real
// letter/digit rune or an FKey.
const namedKeyBase rune = 0xE100

// namedKey is one supported non-alphanumeric trigger key. Bare keys may be
// bound without a modifier: they don't produce text, so a global bind on them
// won't fire while typing.
type namedKey struct {
	vk   uint32
	name string // wire/persisted + display spelling
	bare bool
}

var namedKeys = []namedKey{
	{0x13, "Pause", true},
	{0x20, "Space", false},
	{0x21, "PageUp", true},
	{0x22, "PageDown", true},
	{0x23, "End", true},
	{0x24, "Home", true},
	{0x25, "Left", false},
	{0x26, "Up", false},
	{0x27, "Right", false},
	{0x28, "Down", false},
	{0x2C, "PrintScreen", true},
	{0x2D, "Insert", true},
	{0x2E, "Delete", false},
	{0x60, "Num0", true},
	{0x61, "Num1", true},
	{0x62, "Num2", true},
	{0x63, "Num3", true},
	{0x64, "Num4", true},
	{0x65, "Num5", true},
	{0x66, "Num6", true},
	{0x67, "Num7", true},
	{0x68, "Num8", true},
	{0x69, "Num9", true},
	{0x6A, "Num*", true},
	{0x6B, "Num+", true},
	{0x6D, "Num-", true},
	{0x6E, "Num.", true},
	{0x6F, "Num/", true},
	{0x91, "ScrollLock", true},
	{0xBA, ";", false},
	{0xBB, "=", false},
	{0xBC, ",", false},
	{0xBD, "-", false},
	{0xBE, ".", false},
	{0xBF, "/", false},
	{0xC0, "`", false},
	{0xDB, "[", false},
	{0xDC, `\`, false},
	{0xDD, "]", false},
	{0xDE, "'", false},
}

var (
	namedByVK   = map[uint32]namedKey{}
	namedByName = map[string]namedKey{} // upper-cased name
)

func init() {
	for _, k := range namedKeys {
		namedByVK[k.vk] = k
		namedByName[strings.ToUpper(k.name)] = k
	}
}

// namedKeyFor returns the named key a Binding.Key rune encodes, if any.
func namedKeyFor(r rune) (namedKey, bool) {
	if r <= namedKeyBase || r > namedKeyBase+0xFF {
		return namedKey{}, false
	}
	k, ok := namedByVK[uint32(r-namedKeyBase)]
	return k, ok
}

// bareAllowed reports whether r may be bound without a modifier.
func bareAllowed(r rune) bool {
	if fKeyNumber(r) > 0 {
		return true
	}
	k, ok := namedKeyFor(r)
	return ok && k.bare
}

// keyForVK maps a Windows virtual-key code to the Binding.Key rune that
// triggers on it (ok=false for modifiers and unsupported keys). Used when
// recording a combo from the keyboard hook.
func keyForVK(vk uint32) (rune, bool) {
	switch {
	case vk >= 'A' && vk <= 'Z', vk >= '0' && vk <= '9':
		return rune(vk), true
	case vk >= 0x70 && vk <= 0x87: // VK_F1..VK_F24
		return FKey(int(vk-0x70) + 1), true
	}
	if _, ok := namedByVK[vk]; ok {
		return namedKeyBase + rune(vk), true
	}
	return 0, false
}

// isModifierVK reports whether vk is Shift/Ctrl/Alt/Win (merged or L/R).
func isModifierVK(vk uint32) bool {
	switch vk {
	case 0x10, 0x11, 0x12, 0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, 0x5B, 0x5C:
		return true
	}
	return false
}

// applyModifierVK sets or clears the modifier vk represents on b.
func applyModifierVK(b *Binding, vk uint32, down bool) {
	switch vk {
	case 0x10, 0xA0, 0xA1:
		b.Shift = down
	case 0x11, 0xA2, 0xA3:
		b.Ctrl = down
	case 0x12, 0xA4, 0xA5:
		b.Alt = down
	case 0x5B, 0x5C:
		b.Win = down
	}
}
