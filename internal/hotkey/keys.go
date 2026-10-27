package hotkey

import (
	"strings"
	"unicode"
)

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
	// Mouse buttons (matched by the WH_MOUSE_LL hook, never by RegisterHotKey).
	// Left and right are deliberately absent: binding them would eat every click.
	{vkMButton, "MouseMiddle", true},
	{vkXButton1, "Mouse4", true},
	{vkXButton2, "Mouse5", true},
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
	// Sided modifiers used as a key of their own, so modifier-only hotkeys
	// (RCtrl alone, LCtrl + LShift) work. Hook-only.
	{vkLShift, "LShift", true},
	{vkRShift, "RShift", true},
	{vkLControl, "LCtrl", true},
	{vkRControl, "RCtrl", true},
	{vkLMenu, "LAlt", true},
	{vkRMenu, "RAlt", true},
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
// recording a combo from the keyboard hook; the recorder turns modifiers into
// keys itself (modifier-only shortcuts) with namedKeyBase+vk.
func keyForVK(vk uint32) (rune, bool) {
	if isModifierVK(vk) {
		return 0, false
	}
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

// Virtual-key codes the engine needs by name.
const (
	vkMButton  = 0x04 // VK_MBUTTON
	vkXButton1 = 0x05 // VK_XBUTTON1 (Mouse4, "Back")
	vkXButton2 = 0x06 // VK_XBUTTON2 (Mouse5, "Forward")
	vkShift    = 0x10 // VK_SHIFT  (merged L+R)
	vkControl  = 0x11 // VK_CONTROL (merged L+R)
	vkMenu     = 0x12 // VK_MENU / Alt (merged L+R)
	vkEscape   = 0x1B // VK_ESCAPE (global overlay cancel)
	vkLWin     = 0x5B // VK_LWIN
	vkRWin     = 0x5C // VK_RWIN
	vkF1       = 0x70 // VK_F1; F2-F24 follow consecutively
	vkLShift   = 0xA0 // VK_LSHIFT
	vkRShift   = 0xA1 // VK_RSHIFT
	vkLControl = 0xA2 // VK_LCONTROL
	vkRControl = 0xA3 // VK_RCONTROL
	vkLMenu    = 0xA4 // VK_LMENU
	vkRMenu    = 0xA5 // VK_RMENU

	scanRShift   = 0x36 // set-1 scan code of the right Shift key
	llkhExtended = 0x01 // LLKHF_EXTENDED: right Ctrl / right Alt
)

// triggerVK maps a binding Key rune to its virtual-key code. For A-Z and 0-9 the
// VK equals the ASCII uppercase rune ('S'==0x53, '1'==0x31); F1-F24 are
// VK_F1 (0x70) onward; named keys carry their VK.
func triggerVK(key rune) uint32 {
	if key == 0 {
		return 0
	}
	if n := fKeyNumber(key); n > 0 {
		return vkF1 + uint32(n-1)
	}
	if k, ok := namedKeyFor(key); ok {
		return k.vk
	}
	return uint32(unicode.ToUpper(key))
}

// isMouseVK reports whether vk is one of the bindable mouse buttons.
func isMouseVK(vk uint32) bool {
	return vk == vkMButton || vk == vkXButton1 || vk == vkXButton2
}

// sidedVK turns the merged Shift/Ctrl/Alt codes into their left/right codes.
// Physical keys already arrive sided in a low-level hook; injected ones
// (keybd_event(VK_SHIFT), AutoHotkey) may not. Shift is told apart by its
// scan code, Ctrl and Alt by LLKHF_EXTENDED (set on the right-hand key).
func sidedVK(vk, scan, flags uint32) uint32 {
	switch vk {
	case vkShift:
		if scan == scanRShift {
			return vkRShift
		}
		return vkLShift
	case vkControl:
		if flags&llkhExtended != 0 {
			return vkRControl
		}
		return vkLControl
	case vkMenu:
		if flags&llkhExtended != 0 {
			return vkRMenu
		}
		return vkLMenu
	}
	return vk
}

// sideVKs are the eight sided modifier keys in SideMask bit order, so
// sideVKs[i] is the key for SideMask(1<<i).
var sideVKs = [8]uint32{vkLControl, vkRControl, vkLShift, vkRShift, vkLMenu, vkRMenu, vkLWin, vkRWin}

// sideNames are the wire spellings of the SideMask bits, in bit order.
var sideNames = [8]string{"lctrl", "rctrl", "lshift", "rshift", "lalt", "ralt", "lwin", "rwin"}

// sideLabels are the display spellings of the SideMask bits, in bit order.
var sideLabels = [8]string{"LCtrl", "RCtrl", "LShift", "RShift", "LAlt", "RAlt", "LWin", "RWin"}

// sideBit returns the SideMask bit for a sided modifier vk (0 if vk isn't one).
func sideBit(vk uint32) SideMask {
	for i, v := range sideVKs {
		if v == vk {
			return SideMask(1) << i
		}
	}
	return 0
}

// Modifier groups: both sides of one modifier.
const (
	groupCtrl  = SideLCtrl | SideRCtrl
	groupShift = SideLShift | SideRShift
	groupAlt   = SideLAlt | SideRAlt
	groupWin   = SideLWin | SideRWin
)
