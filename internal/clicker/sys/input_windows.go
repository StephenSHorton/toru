//go:build windows

package sys

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/inputtag"
)

// INPUT on 64-bit Windows: a 4-byte type, 4 bytes of padding, then a 32-byte
// union (MOUSEINPUT is the largest member and holds a ULONG_PTR, so the union
// is 8-aligned). The union is [4]uint64 rather than [32]byte so input itself is
// 8-aligned and the dwExtraInfo inside it is never misaligned in an array.
type input struct {
	typ   uint32
	_     uint32
	union [4]uint64
}

// mouseInput mirrors MOUSEINPUT (32 bytes; dwExtraInfo at 24).
type mouseInput struct {
	dx, dy      int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	_           uint32
	dwExtraInfo uintptr
}

// keyInput mirrors KEYBDINPUT (24 bytes; dwExtraInfo at 16). It is written into
// the front of the 32-byte union; the rest stays zero.
type keyInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	_           uint32
	dwExtraInfo uintptr
}

const (
	inputMouse    = 0 // INPUT_MOUSE
	inputKeyboard = 1 // INPUT_KEYBOARD

	mouseeventfMove        = 0x0001
	mouseeventfLeftDown    = 0x0002
	mouseeventfLeftUp      = 0x0004
	mouseeventfRightDown   = 0x0008
	mouseeventfRightUp     = 0x0010
	mouseeventfMiddleDown  = 0x0020
	mouseeventfMiddleUp    = 0x0040
	mouseeventfXDown       = 0x0080
	mouseeventfXUp         = 0x0100
	mouseeventfVirtualDesk = 0x4000
	mouseeventfAbsolute    = 0x8000

	xbutton1 = 0x0001
	xbutton2 = 0x0002

	keyeventfExtendedKey = 0x0001
	keyeventfKeyUp       = 0x0002
	keyeventfScanCode    = 0x0008

	// scanLShift is the left Shift make code; Shift wrapping always uses the
	// left key (never extended).
	scanLShift = 0x2A

	// bufLen is the per-injector INPUT buffer. A Send longer than this goes out
	// in several SendInput calls of bufLen events each.
	bufLen = 16
)

var errBadOp = errors.New("clicker/sys: unknown input op")

// injector is the real engine.Injector. One per run; the buffer is filled in
// place so Send does no heap allocation.
type injector struct {
	mu  sync.Mutex
	buf [bufLen]input
	pt  [2]int32 // GetCursorPos out-parameter (kept here so it never escapes)
}

// NewInjector returns the SendInput-backed injector. Every event it sends
// (clicks, keys and moves) carries dwExtraInfo = inputtag.Toru.
func NewInjector() engine.Injector { return &injector{} }

// putMouse writes a tagged mouse event into slot i.
func (inj *injector) putMouse(i int, mi mouseInput) {
	mi.dwExtraInfo = inputtag.Toru
	in := &inj.buf[i]
	in.typ = inputMouse
	in.union = [4]uint64{}
	*(*mouseInput)(unsafe.Pointer(&in.union)) = mi
}

// putKey writes a tagged scan-code key event into slot i. scan may carry the
// 0xE0/0xE1 prefix in its high byte (MapVirtualKeyW's VSC_EX form); that also
// sets KEYEVENTF_EXTENDEDKEY.
func (inj *injector) putKey(i int, scan uint16, extended, up bool) {
	flags := uint32(keyeventfScanCode)
	if extended || scan>>8 == 0xE0 || scan>>8 == 0xE1 {
		flags |= keyeventfExtendedKey
	}
	if up {
		flags |= keyeventfKeyUp
	}
	in := &inj.buf[i]
	in.typ = inputKeyboard
	in.union = [4]uint64{}
	*(*keyInput)(unsafe.Pointer(&in.union)) = keyInput{
		wScan:       scan & 0xFF,
		dwFlags:     flags,
		dwExtraInfo: inputtag.Toru,
	}
}

// buttonFlags returns the MOUSEEVENTF_* flag and mouseData for a press or
// release of b (ok=false for an unknown button).
func buttonFlags(b engine.Button, down bool) (flags, data uint32, ok bool) {
	pick := func(d, u uint32) uint32 {
		if down {
			return d
		}
		return u
	}
	switch b {
	case engine.ButtonLeft:
		return pick(mouseeventfLeftDown, mouseeventfLeftUp), 0, true
	case engine.ButtonRight:
		return pick(mouseeventfRightDown, mouseeventfRightUp), 0, true
	case engine.ButtonMiddle:
		return pick(mouseeventfMiddleDown, mouseeventfMiddleUp), 0, true
	case engine.ButtonX1:
		return pick(mouseeventfXDown, mouseeventfXUp), xbutton1, true
	case engine.ButtonX2:
		return pick(mouseeventfXDown, mouseeventfXUp), xbutton2, true
	}
	return 0, 0, false
}

// fill writes op into slot i.
func (inj *injector) fill(i int, op engine.Op) error {
	switch op.Kind {
	case engine.OpMouseDown, engine.OpMouseUp:
		flags, data, ok := buttonFlags(op.Button, op.Kind == engine.OpMouseDown)
		if !ok {
			return errBadOp
		}
		// No MOVE/ABSOLUTE: the click lands wherever the cursor is.
		inj.putMouse(i, mouseInput{mouseData: data, dwFlags: flags})
	case engine.OpKeyDown, engine.OpKeyUp:
		if op.Scan == 0 {
			return errBadOp
		}
		inj.putKey(i, op.Scan, op.Extended, op.Kind == engine.OpKeyUp)
	case engine.OpShiftDown, engine.OpShiftUp:
		inj.putKey(i, scanLShift, false, op.Kind == engine.OpShiftUp)
	default:
		return errBadOp
	}
	return nil
}

// sendInput is SendInput over the first n buffer slots. A var so tests can
// count calls without injecting anything.
var sendInput = func(inj *injector, n int) int {
	r, _, _ := syscall.SyscallN(procSendInput.Addr(),
		uintptr(n), uintptr(unsafe.Pointer(&inj.buf[0])), unsafe.Sizeof(input{}))
	return int(r)
}

// Send injects ops in order. ops longer than the buffer go out in several
// SendInput calls; a short count from any call stops there and returns how
// many went through in total. A short count is not an error: Windows does not
// say why (UIPI, a secure desktop), and GetLastError is not reliable for it.
func (inj *injector) Send(ops []engine.Op) (int, error) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	sent := 0
	for sent < len(ops) {
		n := min(len(ops)-sent, bufLen)
		for i := 0; i < n; i++ {
			if err := inj.fill(i, ops[sent+i]); err != nil {
				// Send the valid prefix of this chunk, then report the bad op.
				bad := ops[sent+i]
				idx := sent + i
				if i > 0 {
					sent += sendInput(inj, i)
				}
				return sent, fmt.Errorf("%w (op %d kind %d)", err, idx, bad.Kind)
			}
		}
		got := sendInput(inj, n)
		sent += got
		if got < n {
			return sent, nil
		}
	}
	return sent, nil
}

// Teleport puts the cursor exactly on (x, y) with SetCursorPos, which is
// pixel-exact in virtual-desktop physical px under per-monitor-v2 DPI
// awareness. The move is not an input event, so it carries no tag.
func (inj *injector) Teleport(x, y int32) error {
	r, _, _ := syscall.SyscallN(procSetCursorPos.Addr(), uintptr(x), uintptr(y))
	if r == 0 {
		return errors.New("clicker/sys: SetCursorPos failed")
	}
	return nil
}

// GlideStep moves the cursor to (x, y) as one tagged absolute mouse move,
// normalized over the whole virtual desktop (MOUSEEVENTF_VIRTUALDESK), so
// monitors left of or above the primary are reachable.
func (inj *injector) GlideStep(x, y int32) error {
	vx, vy, vw, vh := VirtualScreen()
	if vw <= 1 || vh <= 1 {
		return errors.New("clicker/sys: no virtual screen")
	}
	mi := mouseInput{
		dx:      normalizeAbs(x, vx, vw),
		dy:      normalizeAbs(y, vy, vh),
		dwFlags: mouseeventfMove | mouseeventfAbsolute | mouseeventfVirtualDesk,
	}
	if n := inj.sendMouse(mi); n != 1 {
		return errors.New("clicker/sys: move blocked")
	}
	return nil
}

// sendMouse sends one tagged mouse event and returns how many went through.
func (inj *injector) sendMouse(mi mouseInput) int {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	inj.putMouse(0, mi)
	return sendInput(inj, 1)
}

// normalizeAbs maps physical px p on an axis starting at origin with the given
// extent to SendInput's 0..65535 absolute range: round((p-origin)·65535/(extent-1)),
// clamped.
func normalizeAbs(p, origin, extent int32) int32 {
	if extent <= 1 {
		return 0
	}
	off := int64(p) - int64(origin)
	if off <= 0 {
		return 0
	}
	den := int64(extent) - 1
	v := (off*65535*2 + den) / (2 * den)
	if v > 65535 {
		v = 65535
	}
	return int32(v)
}

// Cursor returns the cursor position in virtual-desktop physical px.
func (inj *injector) Cursor() (int32, int32, bool) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	r, _, _ := syscall.SyscallN(procGetCursorPos.Addr(), uintptr(unsafe.Pointer(&inj.pt)))
	if r == 0 {
		return 0, 0, false
	}
	return inj.pt[0], inj.pt[1], true
}
