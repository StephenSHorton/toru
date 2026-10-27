//go:build windows

package overlay

import (
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// All overlay windows belong to one locked OS thread with its own message
// pump, started on first use. Callers never touch a window: they queue a
// closure and wake the thread by posting wmRun to its message-only host
// window, which also owns the ~60 Hz animation timer. The state below the
// "UI thread only" line is read and written only from those closures and the
// host's window proc, so it needs no lock.

const (
	hostClass  = "ToruClickerOverlayHost"
	wmRun      = w32.WM_APP + 0x31
	timerTick  = 1
	tickMs     = 16
	pillTextSz = 12.5 // dip
	hintTextSz = 13   // dip
)

var ui struct {
	mu    sync.Mutex
	hwnd  w32.HWND
	queue []func()
	done  chan struct{}
}

var (
	hostClassOnce sync.Once
	hostProcCB    uintptr
)

// post runs fn on the UI thread, starting it if needed. Never blocks on the
// UI thread's work.
func post(fn func()) {
	ui.mu.Lock()
	if ui.hwnd == 0 {
		startUI()
	}
	hwnd := ui.hwnd
	if hwnd != 0 {
		ui.queue = append(ui.queue, fn)
	}
	ui.mu.Unlock()
	if hwnd != 0 {
		w32.PostMessage(hwnd, wmRun, 0, 0)
	}
}

// postIfRunning is post without starting the thread: hiding something that
// was never shown has nothing to do.
func postIfRunning(fn func()) {
	ui.mu.Lock()
	running := ui.hwnd != 0
	ui.mu.Unlock()
	if running {
		post(fn)
	}
}

// call runs fn on the UI thread and waits up to timeout for it (tests).
func call(fn func(), timeout time.Duration) bool {
	done := make(chan struct{})
	post(func() { fn(); close(done) })
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// startUI spawns the UI thread and waits for its host window. ui.mu is held.
func startUI() {
	ready := make(chan w32.HWND, 1)
	done := make(chan struct{})
	go runUI(ready, done)
	select {
	case h := <-ready:
		if h != 0 {
			ui.hwnd, ui.done, ui.queue = h, done, nil
		}
	case <-time.After(2 * time.Second):
	}
}

func runUI(ready chan<- w32.HWND, done chan struct{}) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(done)

	inst := w32.GetModuleHandle("")
	hostClassOnce.Do(func() {
		hostProcCB = syscall.NewCallback(hostWndProc)
		wc := w32.WNDCLASSEX{
			WndProc:   hostProcCB,
			Instance:  inst,
			ClassName: windows.StringToUTF16Ptr(hostClass),
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		w32.RegisterClassEx(&wc)
	})
	// A message-only window: no surface, no z-order, just a mailbox + timers.
	hwnd := w32.CreateWindowEx(0, windows.StringToUTF16Ptr(hostClass), nil, 0,
		0, 0, 0, 0, w32.HWND_MESSAGE, 0, inst, nil)
	ready <- hwnd
	if hwnd == 0 {
		return
	}
	var m w32.MSG
	for w32.GetMessage(&m, 0, 0, 0) > 0 {
		w32.TranslateMessage(&m)
		w32.DispatchMessage(&m)
	}
	ui.mu.Lock()
	if ui.hwnd == hwnd {
		ui.hwnd, ui.queue = 0, nil
	}
	ui.mu.Unlock()
}

func hostWndProc(hwnd w32.HWND, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case wmRun:
		ui.mu.Lock()
		q := ui.queue
		ui.queue = nil
		ui.mu.Unlock()
		for _, fn := range q {
			fn()
		}
		return 0
	case w32.WM_TIMER:
		if wParam == timerTick {
			tick(hwnd, time.Now())
		}
		return 0
	case w32.WM_DESTROY:
		w32.PostQuitMessage(0)
		return 0
	}
	return w32.DefWindowProc(hwnd, m, wParam, lParam)
}

// ---- UI thread only below ----

// layer is a set of per-monitor sprites showing one scene, with an optional
// fade timeline.
type layer struct {
	sprites    []*sprite
	rects      []Monitor
	persistent bool
	f          fade
	start      time.Time
}

func (l *layer) active() bool { return len(l.sprites) > 0 }

func (l *layer) clear() {
	for _, s := range l.sprites {
		s.destroy()
	}
	l.sprites, l.rects = nil, nil
}

// show paints one sprite per rect. Sprites whose rect is unchanged keep their
// window (no flicker on an update). A layer already on screen skips the
// fade-in and restarts its hold.
func (l *layer) show(rects []Monitor, paint func(c *Canvas, r Monitor, scale float64), persistent bool, f fade) {
	keep := make([]*sprite, len(rects))
	for i, r := range rects {
		if i < len(l.rects) && l.rects[i] == r {
			keep[i], l.sprites[i] = l.sprites[i], nil
		}
	}
	wasUp := l.active()
	for _, s := range l.sprites {
		if s != nil {
			s.destroy()
		}
	}
	now := time.Now()
	l.persistent, l.f, l.start = persistent, f, now
	if wasUp {
		l.start = now.Add(-f.In)
	}
	alpha := uint8(255)
	if !persistent {
		alpha, _ = f.at(now.Sub(l.start))
	}
	for i, r := range rects {
		s := keep[i]
		if s == nil {
			s = &sprite{}
			keep[i] = s
		}
		c := s.canvas(int(r.R-r.L), int(r.B-r.T))
		c.Clear(Color{})
		paint(c, r, scaleFor(r))
		s.present(r.L, r.T, alpha)
	}
	l.sprites, l.rects = keep, rects
	if !persistent && len(rects) > 0 {
		startTimer()
	}
}

// step advances a fading layer; it reports whether it still needs ticks.
func (l *layer) step(now time.Time) bool {
	if !l.active() || l.persistent {
		return false
	}
	a, done := l.f.at(now.Sub(l.start))
	if done {
		l.clear()
		return false
	}
	for _, s := range l.sprites {
		s.setAlpha(a)
	}
	return true
}

var st struct {
	safety, points layer
	pick           *pickView
	ind            indicatorView
	timerOn        bool
}

func startTimer() {
	if st.timerOn {
		return
	}
	ui.mu.Lock()
	hwnd := ui.hwnd
	ui.mu.Unlock()
	w32.SetTimer(hwnd, timerTick, tickMs, 0)
	st.timerOn = true
}

func tick(hwnd w32.HWND, now time.Time) {
	busy := st.safety.step(now)
	busy = st.points.step(now) || busy
	if st.pick != nil {
		st.pick.apply(false)
		busy = true
	}
	if !busy {
		w32.KillTimer(hwnd, timerTick)
		st.timerOn = false
	}
}

func showSafetyLayer(s SafetyScene, persistent bool) {
	if len(s.Monitors) == 0 {
		s.Monitors = monitors()
	}
	st.safety.show(s.Monitors, func(c *Canvas, r Monitor, scale float64) {
		renderSafety(c, r.L, r.T, s, scale)
	}, persistent, fadeSafety)
}

// pointReach is how far a point's drawing reaches from its centre (ring,
// dot and index badge), used to pick the monitors that need a layer.
func pointReach(p ScenePoint) int32 { return max(p.Radius, 0) + 64 }

func showPointsLayer(pts []ScenePoint, persistent bool) {
	if len(pts) == 0 {
		st.points.clear()
		return
	}
	var rects []Monitor
	for _, m := range monitors() {
		for _, p := range pts {
			k := pointReach(p)
			if intersects(m, Monitor{p.X - k, p.Y - k, p.X + k + 1, p.Y + k + 1}) {
				rects = append(rects, m)
				break
			}
		}
	}
	pts = append([]ScenePoint(nil), pts...)
	st.points.show(rects, func(c *Canvas, r Monitor, scale float64) {
		renderPoints(c, r.L, r.T, pts, scale)
	}, persistent, fadePoints)
}

// ShowSafety draws the failsafe preview; persistent=false fades it in over
// 250 ms, holds and is gone by 3 s. During a pick it updates the scene under
// the pick and stays up.
func ShowSafety(s SafetyScene, persistent bool) {
	s.Monitors = append([]Monitor(nil), s.Monitors...)
	s.Zones = append([]SceneZone(nil), s.Zones...)
	post(func() { showSafetyLayer(s, persistent || st.pick != nil) })
}

// ShowPoints draws click points; persistent=false holds them and fades them
// out by 5 s. An empty list hides the points layer.
func ShowPoints(pts []ScenePoint, persistent bool) {
	pts = append([]ScenePoint(nil), pts...)
	post(func() { showPointsLayer(pts, persistent || st.pick != nil) })
}

// Hide removes the preview layers (not the indicator). An active pick keeps
// its scene; ending the pick removes it.
func Hide() {
	postIfRunning(func() {
		if st.pick != nil {
			return
		}
		st.safety.clear()
		st.points.clear()
	})
}

// indicatorView is the running pill.
type indicatorView struct {
	s     sprite
	text  string
	scale float64
}

func showIndicator(text string) {
	mon := w32.MonitorFromWindow(w32.GetForegroundWindow(), w32.MONITOR_DEFAULTTONEAREST)
	mi := w32.MONITORINFO{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if !w32.GetMonitorInfo(mon, &mi) {
		return
	}
	work := Monitor{mi.RcWork.Left, mi.RcWork.Top, mi.RcWork.Right, mi.RcWork.Bottom}
	scale := scaleOf(mon)
	iv := &st.ind
	if !iv.s.visible() || text != iv.text || scale != iv.scale {
		mask := textMask(text, pillTextSz, w32.FW_SEMIBOLD, scale)
		w, h := pillSize(mask, scale)
		renderPill(iv.s.canvas(w, h), mask, scale)
		iv.text, iv.scale = text, scale
	}
	// One UpdateLayeredWindow swaps size, position and pixels together, so a
	// text change never flickers.
	x, y := pillPos(work, iv.s.w, scale)
	iv.s.present(x, y, 255)
	iv.s.raise() // stay above topmost windows opened since the last update
}

// IndicatorShow shows or updates the running pill with text, centred near the
// top of the monitor the foreground window is on.
func IndicatorShow(text string) { post(func() { showIndicator(text) }) }

// IndicatorHide hides the running pill.
func IndicatorHide() {
	postIfRunning(func() {
		st.ind.s.destroy()
		st.ind.text = ""
	})
}

// destroyAll closes every overlay window. UI thread.
func destroyAll() {
	if st.pick != nil {
		st.pick.close()
		st.pick = nil
	}
	st.safety.clear()
	st.points.clear()
	st.ind.s.destroy()
	st.ind.text = ""
}

// Shutdown destroys every overlay window, unhooks an active pick (OnEnd(true))
// and stops the UI thread. A later call to any function starts it again.
func Shutdown() {
	endPickForShutdown()
	ui.mu.Lock()
	hwnd, done := ui.hwnd, ui.done
	ui.mu.Unlock()
	if hwnd == 0 {
		return
	}
	post(func() {
		destroyAll()
		w32.DestroyWindow(hwnd)
	})
	select {
	case <-done:
	case <-time.After(time.Second):
	}
}

// counts reports what is on screen (tests).
type counts struct{ safety, points, pick, indicator int }

func visibleCounts() (c counts, ok bool) {
	ok = call(func() {
		c.safety = len(st.safety.sprites)
		c.points = len(st.points.sprites)
		if st.pick != nil {
			c.pick = 1
		}
		if st.ind.s.visible() {
			c.indicator = 1
		}
	}, 2*time.Second)
	return c, ok
}
