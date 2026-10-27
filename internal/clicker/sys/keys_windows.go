//go:build windows

package sys

import (
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
)

const (
	mapvkVKToVSCEx = 4    // MAPVK_VK_TO_VSC_EX: high byte 0xE0/0xE1 for extended keys
	vkCapital      = 0x14 // VK_CAPITAL

	spiGetKeyboardSpeed = 0x000A // SPI_GETKEYBOARDSPEED: 0..31
	spiGetKeyboardDelay = 0x0016 // SPI_GETKEYBOARDDELAY: 0..3
)

// KeyScan resolves a hotkey.KeyName spelling ("A", "7", "F5", "Space", "Num5",
// "Up", "[", "LCtrl") to the scan code the clicker sends. scan is the low byte
// of MapVirtualKeyW(vk, MAPVK_VK_TO_VSC_EX); extended is set when that call
// reports an 0xE0/0xE1 prefix (Num/, RCtrl, RAlt, PrintScreen, ...) and for the
// navigation cluster (arrows, Ins/Del/Home/End/PgUp/PgDn; see navExtended).
// isLetter is true for A-Z, the keys Shift wrapping applies to. Mouse button
// names are not keys and return an error.
func KeyScan(keyName string) (scan uint16, extended bool, isLetter bool, err error) {
	vk, letter, ok := vkForName(keyName)
	if !ok {
		return 0, false, false, fmt.Errorf("clicker/sys: unknown key %q", keyName)
	}
	r, _, _ := syscall.SyscallN(procMapVirtualKeyW.Addr(), uintptr(vk), mapvkVKToVSCEx)
	sc := uint16(r)
	if sc&0xFF == 0 {
		return 0, false, false, fmt.Errorf("clicker/sys: key %q has no scan code on this layout", keyName)
	}
	prefix := sc >> 8
	return sc & 0xFF, prefix == 0xE0 || prefix == 0xE1 || navExtended(vk), letter, nil
}

// navExtended reports the dedicated navigation keys. They share their scan
// codes with the numpad (Up = Num8 = 0x48, Delete = Num. = 0x53, ...) and are
// told apart only by the E0 prefix, but MapVirtualKeyW(VSC_EX) returns them
// without it (seen on Windows 11 26200), so a press would come out as a numpad
// key. The VKs are unambiguous, so mark them here.
func navExtended(vk uint16) bool {
	switch vk {
	case 0x21, 0x22, 0x23, 0x24, // PageUp, PageDown, End, Home
		0x25, 0x26, 0x27, 0x28, // Left, Up, Right, Down
		0x2D, 0x2E: // Insert, Delete
		return true
	}
	return false
}

// CapsLock reports whether Caps Lock is toggled on (GetKeyState low bit).
func CapsLock() bool {
	r, _, _ := syscall.SyscallN(procGetKeyState.Addr(), vkCapital)
	return r&1 != 0
}

// KeyRepeat returns Windows' typematic timing: the delay before a held key
// repeats (SPI_GETKEYBOARDDELAY 0..3 = 250..1000 ms) and the repeat period
// (SPI_GETKEYBOARDSPEED 0..31 = 2.5..30 repeats/s, linear). Falls back to the
// Windows defaults (500 ms, 31 = 30/s) when a query fails.
func KeyRepeat() engine.KeyRepeat {
	delay, speed := uint32(1), uint32(31)
	var v uint32
	if r, _, _ := syscall.SyscallN(procSystemParametersInfoW.Addr(), spiGetKeyboardDelay, 0, uintptr(unsafe.Pointer(&v)), 0); r != 0 {
		delay = min(v, 3)
	}
	if r, _, _ := syscall.SyscallN(procSystemParametersInfoW.Addr(), spiGetKeyboardSpeed, 0, uintptr(unsafe.Pointer(&v)), 0); r != 0 {
		speed = min(v, 31)
	}
	return keyRepeatFrom(delay, speed)
}

// keyRepeatFrom converts the raw SPI values to engine.KeyRepeat.
func keyRepeatFrom(delay, speed uint32) engine.KeyRepeat {
	rate := 2.5 + float64(speed)*27.5/31 // repeats per second
	return engine.KeyRepeat{
		DelayNs:  int64(delay+1) * int64(250*time.Millisecond),
		PeriodNs: int64(float64(time.Second) / rate),
	}
}

// DoubleClickTime is the system double-click time.
func DoubleClickTime() time.Duration {
	r, _, _ := syscall.SyscallN(procGetDoubleClickTime.Addr())
	if r == 0 {
		return 500 * time.Millisecond
	}
	return time.Duration(r) * time.Millisecond
}
