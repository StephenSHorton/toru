//go:build windows

package sys

import (
	"testing"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/inputtag"
)

// TestInputLayout pins the INPUT / MOUSEINPUT / KEYBDINPUT layout SendInput
// expects (cbSize 40 on 64-bit Windows).
func TestInputLayout(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("layout pinned for 64-bit Windows")
	}
	checks := []struct {
		name      string
		got, want uintptr
	}{
		{"Sizeof(input)", unsafe.Sizeof(input{}), 40},
		{"Alignof(input)", unsafe.Alignof(input{}), 8},
		{"Offsetof(input.union)", unsafe.Offsetof(input{}.union), 8},
		{"Sizeof(mouseInput)", unsafe.Sizeof(mouseInput{}), 32},
		{"Offsetof(mouseInput.mouseData)", unsafe.Offsetof(mouseInput{}.mouseData), 8},
		{"Offsetof(mouseInput.dwFlags)", unsafe.Offsetof(mouseInput{}.dwFlags), 12},
		{"Offsetof(mouseInput.time)", unsafe.Offsetof(mouseInput{}.time), 16},
		{"Offsetof(mouseInput.dwExtraInfo)", unsafe.Offsetof(mouseInput{}.dwExtraInfo), 24},
		{"Sizeof(keyInput)", unsafe.Sizeof(keyInput{}), 24},
		{"Offsetof(keyInput.wScan)", unsafe.Offsetof(keyInput{}.wScan), 2},
		{"Offsetof(keyInput.dwFlags)", unsafe.Offsetof(keyInput{}.dwFlags), 4},
		{"Offsetof(keyInput.time)", unsafe.Offsetof(keyInput{}.time), 8},
		{"Offsetof(keyInput.dwExtraInfo)", unsafe.Offsetof(keyInput{}.dwExtraInfo), 16},
		{"array stride", unsafe.Sizeof([2]input{}), 80},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
}

// fakeSend swaps the SendInput call for one that captures the buffer and
// accepts up to limit events per call (limit < 0 = all). Nothing is injected.
func fakeSend(t *testing.T, limit int) (calls *int, seen *[]input) {
	t.Helper()
	orig := sendInput
	calls, seen = new(int), new([]input)
	sendInput = func(inj *injector, n int) int {
		*calls++
		*seen = append(*seen, inj.buf[:n]...)
		if limit >= 0 && n > limit {
			return limit
		}
		return n
	}
	t.Cleanup(func() { sendInput = orig })
	return calls, seen
}

// TestSendTagsAndFlags fills the buffer from mixed ops and checks every event's
// type, flags and dwExtraInfo == inputtag.Toru, without calling SendInput.
func TestSendTagsAndFlags(t *testing.T) {
	_, seen := fakeSend(t, -1)
	ops := []engine.Op{
		{Kind: engine.OpMouseDown, Button: engine.ButtonLeft},
		{Kind: engine.OpMouseUp, Button: engine.ButtonLeft},
		{Kind: engine.OpMouseDown, Button: engine.ButtonX2},
		{Kind: engine.OpShiftDown},
		{Kind: engine.OpKeyDown, Scan: 0x48, Extended: true},
		{Kind: engine.OpKeyUp, Scan: 0x1E},
		{Kind: engine.OpShiftUp},
	}
	inj := NewInjector().(*injector)
	n, err := inj.Send(ops)
	if err != nil || n != len(ops) {
		t.Fatalf("Send = %d, %v", n, err)
	}
	mouse := func(in input) mouseInput { return *(*mouseInput)(unsafe.Pointer(&in.union)) }
	key := func(in input) keyInput { return *(*keyInput)(unsafe.Pointer(&in.union)) }
	ev := *seen
	for i, in := range ev {
		var tag uintptr
		if in.typ == inputMouse {
			tag = mouse(in).dwExtraInfo
		} else {
			tag = key(in).dwExtraInfo
		}
		if tag != inputtag.Toru {
			t.Errorf("event %d dwExtraInfo = %#x, want inputtag.Toru", i, tag)
		}
	}
	if m := mouse(ev[0]); ev[0].typ != inputMouse || m.dwFlags != mouseeventfLeftDown || m.dx != 0 || m.dy != 0 {
		t.Errorf("left down = %+v", m)
	}
	if m := mouse(ev[2]); m.dwFlags != mouseeventfXDown || m.mouseData != xbutton2 {
		t.Errorf("X2 down = %+v", m)
	}
	if k := key(ev[3]); ev[3].typ != inputKeyboard || k.wScan != scanLShift || k.wVk != 0 || k.dwFlags != keyeventfScanCode {
		t.Errorf("shift down = %+v", k)
	}
	if k := key(ev[4]); k.wScan != 0x48 || k.dwFlags != keyeventfScanCode|keyeventfExtendedKey {
		t.Errorf("Up down = %+v", k)
	}
	if k := key(ev[5]); k.wScan != 0x1E || k.dwFlags != keyeventfScanCode|keyeventfKeyUp {
		t.Errorf("A up = %+v", k)
	}
	if k := key(ev[6]); k.dwFlags != keyeventfScanCode|keyeventfKeyUp {
		t.Errorf("shift up = %+v", k)
	}
}

// TestSendShortCount: a short count from SendInput is returned as is, with no
// error and no further calls.
func TestSendShortCount(t *testing.T) {
	calls, _ := fakeSend(t, 1)
	ops := make([]engine.Op, bufLen+4)
	for i := range ops {
		ops[i] = engine.Op{Kind: engine.OpMouseDown, Button: engine.ButtonLeft}
	}
	n, err := NewInjector().Send(ops)
	if err != nil || n != 1 || *calls != 1 {
		t.Fatalf("Send = %d, %v after %d calls; want 1, nil, 1 call", n, err, *calls)
	}
}

// TestSendChunks: more ops than the buffer go out in several calls.
func TestSendChunks(t *testing.T) {
	calls, seen := fakeSend(t, -1)
	ops := make([]engine.Op, bufLen*2+3)
	for i := range ops {
		ops[i] = engine.Op{Kind: engine.OpMouseUp, Button: engine.ButtonRight}
	}
	n, err := NewInjector().Send(ops)
	if err != nil || n != len(ops) || *calls != 3 || len(*seen) != len(ops) {
		t.Fatalf("Send = %d, %v, %d calls, %d events", n, err, *calls, len(*seen))
	}
}

func TestSendBadOp(t *testing.T) {
	_, _ = fakeSend(t, -1)
	n, err := NewInjector().Send([]engine.Op{
		{Kind: engine.OpMouseDown, Button: engine.ButtonLeft},
		{Kind: engine.OpMouseDown, Button: 99},
	})
	if err == nil || n != 1 {
		t.Fatalf("Send = %d, %v; want 1 and an error", n, err)
	}
}

// TestSendNoAllocs: the Send hot path allocates nothing.
func TestSendNoAllocs(t *testing.T) {
	orig := sendInput
	sendInput = func(_ *injector, n int) int { return n }
	defer func() { sendInput = orig }()
	inj := NewInjector()
	ops := []engine.Op{
		{Kind: engine.OpShiftDown},
		{Kind: engine.OpKeyDown, Scan: 0x1E},
		{Kind: engine.OpKeyUp, Scan: 0x1E},
		{Kind: engine.OpShiftUp},
	}
	if a := testing.AllocsPerRun(1000, func() { _, _ = inj.Send(ops) }); a != 0 {
		t.Errorf("Send allocs/op = %v, want 0", a)
	}
}

// TestSendInputSyscallNoAllocs: the real SendInput call path allocates
// nothing. Count 0 makes Windows inject nothing.
func TestSendInputSyscallNoAllocs(t *testing.T) {
	inj := NewInjector().(*injector)
	if a := testing.AllocsPerRun(1000, func() { _ = sendInput(inj, 0) }); a != 0 {
		t.Errorf("SendInput syscall allocs/op = %v, want 0", a)
	}
}

func BenchmarkSend(b *testing.B) {
	orig := sendInput
	sendInput = func(_ *injector, n int) int { return n }
	defer func() { sendInput = orig }()
	inj := NewInjector()
	ops := []engine.Op{
		{Kind: engine.OpMouseDown, Button: engine.ButtonLeft},
		{Kind: engine.OpMouseUp, Button: engine.ButtonLeft},
	}
	b.ReportAllocs()
	for b.Loop() {
		_, _ = inj.Send(ops)
	}
}

func TestNormalizeAbs(t *testing.T) {
	cases := []struct{ p, o, ext, want int32 }{
		{0, 0, 1920, 0},
		{1919, 0, 1920, 65535},
		{-1920, -1920, 3840, 0},
		{3839 - 1920, -1920, 3840, 65535},
		{-1, 0, 1920, 0},
		{5000, 0, 1920, 65535},
		{1, 0, 2, 65535},
		{10, 0, 1, 0},
	}
	for _, c := range cases {
		if got := normalizeAbs(c.p, c.o, c.ext); got != c.want {
			t.Errorf("normalizeAbs(%d,%d,%d) = %d, want %d", c.p, c.o, c.ext, got, c.want)
		}
	}
}
