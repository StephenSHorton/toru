//go:build windows

package hotkey

import (
	"os"
	"testing"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/inputtag"
)

// The hook structs are copied byte-for-byte out of lParam, so their layout
// must match Win32's (64-bit: MSLLHOOKSTRUCT pads 4 bytes before dwExtraInfo).
func TestHookStructLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("64-bit layout")
	}
	if n := unsafe.Sizeof(kbdllhookstruct{}); n != 24 {
		t.Errorf("KBDLLHOOKSTRUCT size %d, want 24", n)
	}
	if n := unsafe.Sizeof(msllhookstruct{}); n != 32 {
		t.Errorf("MSLLHOOKSTRUCT size %d, want 32", n)
	}
	if off := unsafe.Offsetof(msllhookstruct{}.DwExtraInfo); off != 24 {
		t.Errorf("MSLLHOOKSTRUCT.dwExtraInfo at %d, want 24", off)
	}
}

// TestHookIgnoresTaggedInputSmoke installs the real keyboard hook with a
// hook-only binding, injects the combo tagged with inputtag.Toru (must not
// fire) and then untagged (must fire once). Opt-in: it injects keystrokes.
// Ctrl+Alt+Shift+F24 is used because nothing on a normal desktop reacts to
// it, and every key is released even if the test fails part way.
func TestHookIgnoresTaggedInputSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	const keyUp = 0x2
	vks := []uint32{0xA2, 0xA4, 0xA0, 0x87} // LCtrl, LAlt, LShift, F24
	release := func(extra uintptr) {
		for i := len(vks) - 1; i >= 0; i-- {
			_, _, _ = procKeybdEvent.Call(uintptr(vks[i]), 0, keyUp, extra)
		}
	}
	tapTagged := func(extra uintptr) {
		defer release(extra)
		for _, vk := range vks {
			_, _, _ = procKeybdEvent.Call(uintptr(vk), 0, 0, extra)
		}
	}
	defer release(0)

	m := New()
	defer m.Close()
	fired := make(chan struct{}, 4)
	// Not a systemHotkeyActions id, so the hook (not RegisterHotKey) owns it.
	b := Binding{Ctrl: true, Alt: true, Shift: true, Key: FKey(24)}
	if err := m.Register("smokeFilter", b, func() { fired <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		up := m.hook != 0
		m.mu.Unlock()
		if up {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("keyboard hook did not install")
		}
		time.Sleep(50 * time.Millisecond)
	}

	tapTagged(inputtag.Toru)
	select {
	case <-fired:
		t.Fatal("a Toru-tagged combo fired a hotkey")
	case <-time.After(500 * time.Millisecond):
	}
	if phys.down[0x87].Load() {
		t.Error("a tagged key reached the physical table")
	}

	tapTagged(0)
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("the untagged combo did not fire")
	}
	select {
	case <-fired:
		t.Fatal("fired twice")
	case <-time.After(300 * time.Millisecond):
	}
}
