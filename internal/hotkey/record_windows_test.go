//go:build windows

package hotkey

import "testing"

// The hook proc can't be driven from a test, so feed recordKey directly.
func TestRecordKeyCapturesChord(t *testing.T) {
	ch := make(chan recordResult, 1)
	actMu.Lock()
	recCh, recMods = ch, Binding{}
	actMu.Unlock()

	steps := []struct {
		vk   uint32
		down bool
	}{
		{0xA2, true},  // LCtrl down
		{0x09, true},  // Tab: unsupported, ignored, still recording
		{0x09, false}, // Tab up
		{0xA4, true},  // LAlt down
		{0x79, true},  // F10 down -> done
		{0x79, false}, // F10 up
		{0xA4, false},
		{0xA2, false},
	}
	for _, s := range steps {
		if !recordKey(s.vk, s.down) {
			t.Fatalf("vk %#x down=%v should be swallowed during/after recording", s.vk, s.down)
		}
	}
	r := <-ch
	if !r.ok || r.b != (Binding{Ctrl: true, Alt: true, Key: FKey(10)}) {
		t.Fatalf("recorded %+v", r)
	}
	if recordKey(0x79, true) {
		t.Error("keys after the recording ended must pass through")
	}
	if recordKey('A', false) {
		t.Error("a key-up that wasn't swallowed on its way down must pass through")
	}
}

func TestRecordKeyEscapeCancels(t *testing.T) {
	ch := make(chan recordResult, 1)
	actMu.Lock()
	recCh, recMods = ch, Binding{}
	actMu.Unlock()
	recordKey(vkEscape, true)
	recordKey(vkEscape, false)
	if r := <-ch; r.ok {
		t.Fatalf("Esc should cancel, got %+v", r)
	}
}
