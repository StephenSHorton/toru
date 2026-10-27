//go:build windows

package overlay

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/inputtag"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// Picking. The overlay stays click-through; input comes from WH_MOUSE_LL and
// WH_KEYBOARD_LL hooks installed on their own locked thread for the length of
// one pick. Right-button events and the Esc that ends a pick are swallowed;
// everything else, and every event Toru injected itself (inputtag.Toru),
// passes straight through, so the desktop stays usable while picking.
//
// Hook procs only update state and queue work: the PickCallbacks run from the
// same thread's message loop after the proc has returned, so a slow callback
// can never stall system-wide input or trip Windows' hook timeout.

const (
	whKeyboardLL = 13
	whMouseLL    = 14
	hcAction     = 0

	wmMouseMove   = 0x0200
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmKeyDown     = 0x0100
	wmKeyUp       = 0x0101
	wmSysKeyDown  = 0x0104
	wmSysKeyUp    = 0x0105

	vkShift   = 0x10
	vkControl = 0x11
	vkEscape  = 0x1B

	wmPickFlush = w32.WM_APP + 0x41 // run queued callbacks
	wmPickQuit  = w32.WM_APP + 0x42 // end the pick (outcome in picker.cancelled)

	pointHint = "Right-click to add a point · Shift keeps picking · Ctrl deletes · Esc to finish"
	zoneHint  = "Right-click and drag to draw a zone · Release to save · Esc to cancel"
)

var (
	errPickActive = errors.New("overlay: a pick is already active")
	errPickHooks  = errors.New("overlay: could not install the pick hooks")
)

type msllhookstruct struct {
	Pt          struct{ X, Y int32 }
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// picker is one pick session. Fields under "hook thread" are touched only by
// the hook procs and the pump, which run on the same goroutine.
type picker struct {
	kind   PickKind
	cb     PickCallbacks
	tid    atomic.Uint32
	ending atomic.Bool // cancel or finish requested
	cancel atomic.Bool // the end is a cancel
	done   chan struct{}

	// hook thread
	queue    []func()
	endOnUp  bool // point pick: a plain right-down ends the pick on its up
	anchored bool // zone pick: right button is down
	ax, ay   int32
	escDown  bool
}

var (
	pickMu  sync.Mutex // serialises StartPick
	curPick atomic.Pointer[picker]

	hookOnce        sync.Once
	mouseCB, keysCB uintptr

	// pickPos is the live cursor and drag anchor, written by the hook and read
	// by the UI thread's ~60 Hz tick.
	pickPos struct {
		sync.Mutex
		x, y     int32
		anchored bool
		ax, ay   int32
	}
)

// StartPick starts a point or zone pick with its own low-level hooks; the scene
// and points are drawn underneath. cancel ends it (OnEnd(true)); it does not
// wait, so callbacks may still arrive after it returns. Only one pick runs at a
// time: a second StartPick fails unless the previous one is already ending, in
// which case it waits up to 1 s for it.
func StartPick(kind PickKind, s SafetyScene, pts []ScenePoint, cb PickCallbacks) (cancel func(), err error) {
	if kind != PickPoint && kind != PickZone {
		return func() {}, errors.New("overlay: unknown pick kind")
	}
	pickMu.Lock()
	defer pickMu.Unlock()
	if old := curPick.Load(); old != nil {
		if !old.ending.Load() {
			return func() {}, errPickActive
		}
		select {
		case <-old.done:
		case <-time.After(time.Second):
			return func() {}, errPickActive
		}
	}
	hookOnce.Do(func() {
		mouseCB = syscall.NewCallback(pickMouseProc)
		keysCB = syscall.NewCallback(pickKeyProc)
	})

	p := &picker{kind: kind, cb: cb, done: make(chan struct{})}
	if x, y, ok := w32.GetCursorPos(); ok {
		pickPos.Lock()
		pickPos.x, pickPos.y, pickPos.anchored = int32(x), int32(y), false
		pickPos.Unlock()
	}
	curPick.Store(p)
	ready := make(chan bool, 1)
	go p.run(ready)
	ok := false
	select {
	case ok = <-ready:
	case <-time.After(time.Second):
		p.requestEnd(true) // the thread may still come up; make it leave at once
	}
	if !ok {
		return func() {}, errPickHooks
	}

	s.Monitors = append([]Monitor(nil), s.Monitors...)
	s.Zones = append([]SceneZone(nil), s.Zones...)
	pts = append([]ScenePoint(nil), pts...)
	post(func() { showPick(kind, s, pts) })
	return func() { p.requestEnd(true) }, nil
}

// requestEnd asks the hook thread to end the pick. The first request wins.
func (p *picker) requestEnd(cancelled bool) {
	if p.ending.CompareAndSwap(false, true) {
		p.cancel.Store(cancelled)
		if tid := p.tid.Load(); tid != 0 {
			postThread(tid, wmPickQuit)
		}
	}
}

func (p *picker) run(ready chan<- bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	// finish frees the pick slot. It runs before OnEnd so OnEnd may start a
	// new pick or call Shutdown without waiting on itself.
	finish := sync.OnceFunc(func() {
		curPick.CompareAndSwap(p, nil)
		close(p.done)
	})
	defer finish()

	p.tid.Store(windows.GetCurrentThreadId())
	mh, _, _ := procSetWindowsHookExW.Call(whMouseLL, mouseCB, 0, 0)
	kh, _, _ := procSetWindowsHookExW.Call(whKeyboardLL, keysCB, 0, 0)
	if mh == 0 || kh == 0 || p.ending.Load() {
		// Failed, or StartPick already gave up waiting: it returned an error,
		// so no OnEnd is owed.
		unhook(mh, kh)
		ready <- false
		return
	}
	ready <- true

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if m.Message == wmPickFlush {
			p.flush()
		}
		if m.Message == wmPickQuit {
			break
		}
	}
	unhook(mh, kh)
	cancelled := p.cancel.Load()
	if cancelled {
		p.queue = nil // nothing from a cancelled pick lands after the cancel
	}
	p.flush()
	post(func() { hidePick(p.kind == PickPoint && !cancelled) })
	finish()
	if p.cb.OnEnd != nil {
		p.cb.OnEnd(cancelled)
	}
}

func unhook(hooks ...uintptr) {
	for _, h := range hooks {
		if h != 0 {
			_, _, _ = procUnhookWindowsHookEx.Call(h)
		}
	}
}

// later queues fn to run on the pump once the hook proc has returned.
func (p *picker) later(fn func()) {
	p.queue = append(p.queue, fn)
	postThread(p.tid.Load(), wmPickFlush)
}

func (p *picker) flush() {
	q := p.queue
	p.queue = nil
	for _, fn := range q {
		fn()
	}
}

func callNext(nCode int, wParam, lParam uintptr) uintptr {
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}

func pickMouseProc(nCode int, wParam, lParam uintptr) uintptr {
	p := curPick.Load()
	if nCode != hcAction || p == nil {
		return callNext(nCode, wParam, lParam)
	}
	var ms msllhookstruct
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&ms)), lParam, unsafe.Sizeof(ms))
	if ms.DwExtraInfo == inputtag.Toru {
		return callNext(nCode, wParam, lParam)
	}
	x, y := ms.Pt.X, ms.Pt.Y
	switch wParam {
	case wmMouseMove:
		pickPos.Lock()
		pickPos.x, pickPos.y = x, y
		pickPos.Unlock()
	case wmRButtonDown:
		if !p.ending.Load() {
			p.rightDown(x, y)
		}
		return 1
	case wmRButtonUp:
		if !p.ending.Load() {
			p.rightUp(x, y)
		}
		return 1
	}
	return callNext(nCode, wParam, lParam)
}

func (p *picker) rightDown(x, y int32) {
	if p.kind == PickZone {
		p.anchored, p.ax, p.ay = true, x, y
		pickPos.Lock()
		pickPos.x, pickPos.y = x, y
		pickPos.anchored, pickPos.ax, pickPos.ay = true, x, y
		pickPos.Unlock()
		return
	}
	// Modifiers as they are at the moment of the press. Ctrl beats Shift.
	ctrl, shift := keyDown(vkControl), keyDown(vkShift)
	cb := p.cb
	switch {
	case ctrl:
		if cb.OnDelete != nil {
			p.later(func() { cb.OnDelete(x, y) })
		}
	default:
		if cb.OnPoint != nil {
			p.later(func() { cb.OnPoint(x, y, shift) })
		}
		// End on the matching up, so that up is swallowed too and no stray
		// right-click reaches the app under the cursor.
		p.endOnUp = !shift
	}
}

func (p *picker) rightUp(x, y int32) {
	if p.kind == PickZone {
		if !p.anchored {
			return
		}
		p.anchored = false
		l, t, r, b := rectFromDrag(p.ax, p.ay, x, y)
		if cb := p.cb.OnZone; cb != nil {
			p.later(func() { cb(l, t, r, b) })
		}
		p.requestEnd(false)
		return
	}
	if p.endOnUp {
		p.requestEnd(false)
	}
}

func pickKeyProc(nCode int, wParam, lParam uintptr) uintptr {
	p := curPick.Load()
	if nCode != hcAction || p == nil {
		return callNext(nCode, wParam, lParam)
	}
	var ks kbdllhookstruct
	_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&ks)), lParam, unsafe.Sizeof(ks))
	if ks.DwExtraInfo == inputtag.Toru || ks.VkCode != vkEscape {
		return callNext(nCode, wParam, lParam)
	}
	switch wParam {
	case wmKeyDown, wmSysKeyDown:
		p.escDown = true
		p.requestEnd(true)
		return 1
	case wmKeyUp, wmSysKeyUp:
		if p.escDown { // the release of the Esc that ended the pick
			p.escDown = false
			return 1
		}
	}
	return callNext(nCode, wParam, lParam)
}

// endPickForShutdown cancels an active pick and waits briefly for it to unhook.
func endPickForShutdown() {
	p := curPick.Load()
	if p == nil {
		return
	}
	p.requestEnd(true)
	select {
	case <-p.done:
	case <-time.After(500 * time.Millisecond):
	}
}

// UpdatePickPoints redraws the points during an active pick.
func UpdatePickPoints(pts []ScenePoint) {
	pts = append([]ScenePoint(nil), pts...)
	postIfRunning(func() {
		if st.pick != nil {
			showPointsLayer(pts, true)
		}
	})
}

// ---- UI thread only below ----

// pickView is what a pick draws on top of the scene: crosshair lines (point
// pick), the live zone rect (zone pick) and the hint bubble.
type pickView struct {
	kind  PickKind
	hint  string
	mons  []Monitor
	h, v  sprite // crosshair lines
	draft sprite
	bub   sprite

	text      *Mask
	textScale float64
	mon       Monitor
	scale     float64
	x, y      int32
	drawn     bool
	draftR    Monitor
}

func showPick(kind PickKind, s SafetyScene, pts []ScenePoint) {
	if st.pick != nil {
		st.pick.close()
	}
	// The pick may already be over (a very quick Esc): then hidePick ran
	// first and there is nothing to show.
	if p := curPick.Load(); p == nil || p.ending.Load() {
		return
	}
	mons := monitors()
	if len(s.Monitors) == 0 {
		s.Monitors = mons
	}
	showSafetyLayer(s, true)
	showPointsLayer(pts, true)
	hint := pointHint
	if kind == PickZone {
		hint = zoneHint
	}
	st.pick = &pickView{kind: kind, hint: hint, mons: mons}
	st.pick.apply(true)
	startTimer()
}

// hidePick removes the pick's layers. After a completed point pick the points
// stay up and fade like a normal preview.
func hidePick(keepPoints bool) {
	if st.pick != nil {
		st.pick.close()
		st.pick = nil
	}
	st.safety.clear()
	if keepPoints && st.points.active() {
		st.points.persistent, st.points.f, st.points.start = false, fadePoints, time.Now()
		startTimer()
	} else {
		st.points.clear()
	}
}

func (v *pickView) close() {
	v.h.destroy()
	v.v.destroy()
	v.draft.destroy()
	v.bub.destroy()
}

// apply moves the pick decorations to the latest cursor position.
func (v *pickView) apply(force bool) {
	pickPos.Lock()
	x, y, anchored, ax, ay := pickPos.x, pickPos.y, pickPos.anchored, pickPos.ax, pickPos.ay
	pickPos.Unlock()
	if !force && v.drawn && x == v.x && y == v.y {
		return
	}
	v.x, v.y, v.drawn = x, y, true
	mon, ok := monitorAt(v.mons, x, y)
	if !ok {
		return
	}
	monChanged := mon != v.mon || force
	if monChanged {
		v.mon, v.scale = mon, scaleFor(mon)
	}

	if v.kind == PickPoint {
		t := int32(crosshairThick(v.scale))
		if monChanged {
			renderCrosshair(v.h.canvas(int(mon.R-mon.L), int(t)), v.scale)
			v.h.present(mon.L, y-t/2, 255)
			renderCrosshair(v.v.canvas(int(t), int(mon.B-mon.T)), v.scale)
			v.v.present(x-t/2, mon.T, 255)
		} else {
			v.h.move(mon.L, y-t/2)
			v.v.move(x-t/2, mon.T)
		}
	}

	if v.kind == PickZone {
		if anchored {
			l, t, r, b := rectFromDrag(ax, ay, x, y)
			rc := Monitor{l, t, r, b}
			if rc != v.draftR || !v.draft.visible() {
				first := !v.draft.visible()
				renderDraft(v.draft.canvas(int(r-l), int(b-t)), v.scale)
				v.draft.present(l, t, 255)
				v.draftR = rc
				if first {
					v.bub.raise() // keep the hint above the new rect
				}
			}
		} else if v.draft.visible() {
			v.draft.destroy()
		}
	}

	if v.text == nil || v.textScale != v.scale {
		v.text, v.textScale = textMask(v.hint, hintTextSz, w32.FW_SEMIBOLD, v.scale), v.scale
		w, h := bubbleSize(v.text, v.scale)
		renderBubble(v.bub.canvas(w, h), v.text, v.scale)
		bx, by := bubblePos(x, y, w, h, mon, v.scale)
		v.bub.present(bx, by, 255)
		v.bub.raise()
		return
	}
	bx, by := bubblePos(x, y, v.bub.w, v.bub.h, mon, v.scale)
	v.bub.move(bx, by)
}
