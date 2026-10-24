//go:build windows

package hotkey

import (
	"os"
	"testing"
	"time"
)

var procKeybdEvent = user32.NewProc("keybd_event")

func tap(vks ...uint32) {
	const keyUp = 0x2
	for _, vk := range vks {
		_, _, _ = procKeybdEvent.Call(uintptr(vk), 0, 0, 0)
	}
	for i := len(vks) - 1; i >= 0; i-- {
		_, _, _ = procKeybdEvent.Call(uintptr(vks[i]), 0, keyUp, 0)
	}
}

// TestSystemHotkeySmoke registers a real system hotkey, injects the chord and
// checks it fires exactly once through WM_HOTKEY (the hook must step aside),
// then that deactivating it unregisters it. Opt-in: it injects keystrokes.
func TestSystemHotkeySmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	m := New()
	defer m.Close()
	fired := make(chan struct{}, 4)
	// F13 with every modifier: nothing else on the machine uses it.
	b := Binding{Ctrl: true, Alt: true, Shift: true, Key: FKey(13)}
	if err := m.Register("replaySave", b, func() { fired <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		m.mu.Lock()
		owned := m.sysOwned["replaySave"]
		m.mu.Unlock()
		if owned {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replaySave was not registered with RegisterHotKey")
		}
		time.Sleep(50 * time.Millisecond)
	}

	tap(0xA2, 0xA4, 0xA0, 0x7C) // LCtrl, LAlt, LShift, F13
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("system hotkey did not fire")
	}
	select {
	case <-fired:
		t.Fatal("fired twice (hook and WM_HOTKEY both dispatched)")
	case <-time.After(300 * time.Millisecond):
	}

	m.SetActive("replaySave", false)
	time.Sleep(200 * time.Millisecond)
	m.mu.Lock()
	owned := m.sysOwned["replaySave"]
	m.mu.Unlock()
	if owned {
		t.Error("deactivated action should be unregistered")
	}
	tap(0xA2, 0xA4, 0xA0, 0x7C)
	select {
	case <-fired:
		t.Error("inactive action fired")
	case <-time.After(500 * time.Millisecond):
	}
}
