//go:build windows

package sys

import (
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"golang.org/x/sys/windows"
)

const (
	createWaitableTimerHighResolution = 0x2      // CREATE_WAITABLE_TIMER_HIGH_RESOLUTION (Windows 10 1803+)
	timerAllAccess                    = 0x1F0003 // TIMER_ALL_ACCESS

	// spinWindow: a wait shorter than this wakes spinMargin early and spins on
	// QueryPerformanceCounter to the deadline. Longer waits trust the timer.
	spinWindow = 20 * time.Millisecond
)

// waitClock is engine.Clock on a waitable timer. WaitUntil sleeps in
// WaitForMultipleObjects on [interrupt event, timer], so Interrupt wakes it at
// once and a minutes-long hold costs no wakeups. The interrupt event is first:
// when both are signalled the lowest index wins and the wait reports
// "interrupted", as the contract asks.
type waitClock struct {
	handles    [2]windows.Handle // [interrupt (manual reset), timer]
	stop       atomic.Bool       // mirrors the event for the spin loop
	stopMu     sync.Mutex        // makes stop and the event change together
	freq       int64             // QPC ticks per second
	spinMargin int64             // ns
	closeOnce  sync.Once
}

// NewClock returns the high-resolution clock. err means the high-resolution
// timer is unavailable (Windows before 10 1803); the caller then uses
// NewFallbackClock and shows a warning. The closer releases the handles.
func NewClock() (engine.Clock, func(), error) {
	c, err := newWaitClock(createWaitableTimerHighResolution, int64(500*time.Microsecond))
	if err != nil {
		return nil, func() {}, err
	}
	return c, c.close, nil
}

// NewFallbackClock returns a clock on a normal waitable timer with the system
// timer resolution raised to 1 ms (timeBeginPeriod, ref-counted so
// overlapping users are safe; the closer ends it). Its timer can overshoot by
// up to about a millisecond, so it spins a wider tail. If even a normal timer
// cannot be created it returns a pure Go clock.
func NewFallbackClock() (engine.Clock, func()) {
	beginTimerPeriod()
	c, err := newWaitClock(0, int64(1500*time.Microsecond))
	if err != nil {
		g := newGoClock()
		return g, endTimerPeriod
	}
	return c, func() { c.close(); endTimerPeriod() }
}

func newWaitClock(flags uintptr, spinMargin int64) (*waitClock, error) {
	freq, err := qpcFrequency()
	if err != nil {
		return nil, err
	}
	timer, _, e := procCreateWaitableTimerExW.Call(0, 0, flags, timerAllAccess)
	if timer == 0 {
		return nil, e
	}
	ev, err := windows.CreateEvent(nil, 1, 0, nil) // manual reset, unsignalled
	if err != nil {
		_ = windows.CloseHandle(windows.Handle(timer))
		return nil, err
	}
	return &waitClock{
		handles:    [2]windows.Handle{ev, windows.Handle(timer)},
		freq:       freq,
		spinMargin: spinMargin,
	}, nil
}

func (c *waitClock) close() {
	c.closeOnce.Do(func() {
		_, _, _ = syscall.SyscallN(procCancelWaitableTimer.Addr(), uintptr(c.handles[1]))
		_ = windows.CloseHandle(c.handles[1])
		_ = windows.CloseHandle(c.handles[0])
	})
}

// Now is QueryPerformanceCounter in nanoseconds.
func (c *waitClock) Now() int64 {
	var t int64
	_, _, _ = syscall.SyscallN(procQueryPerformanceCounter.Addr(), uintptr(unsafe.Pointer(&t)))
	return qpcToNs(t, c.freq)
}

// qpcToNs converts QPC ticks without overflowing int64 for any realistic
// uptime.
func qpcToNs(t, freq int64) int64 {
	return t/freq*1e9 + t%freq*1e9/freq
}

func qpcFrequency() (int64, error) {
	var f int64
	r, _, e := procQueryPerformanceFrequency.Call(uintptr(unsafe.Pointer(&f)))
	if r == 0 || f <= 0 {
		return 0, e
	}
	return f, nil
}

// WaitUntil blocks until Now() >= deadline (true) or Interrupt (false).
func (c *waitClock) WaitUntil(deadline int64) bool {
	for {
		if c.stop.Load() {
			return false
		}
		rem := deadline - c.Now()
		if rem <= 0 {
			return true
		}
		sleep := rem
		if rem < int64(spinWindow) {
			sleep = rem - c.spinMargin
		}
		if sleep <= 0 {
			return c.spin(deadline)
		}
		due := -(sleep / 100) // relative, 100 ns units
		if due == 0 {
			due = -1
		}
		r, _, _ := syscall.SyscallN(procSetWaitableTimer.Addr(),
			uintptr(c.handles[1]), uintptr(unsafe.Pointer(&due)), 0, 0, 0, 0)
		if r == 0 {
			// Should not happen; spinning the rest keeps time without the timer.
			return c.spin(deadline)
		}
		ev, err := windows.WaitForMultipleObjects(c.handles[:], false, windows.INFINITE)
		if err != nil {
			return c.spin(deadline)
		}
		if ev == windows.WAIT_OBJECT_0 {
			return false
		}
		// Timer fired: loop to re-check the deadline (and spin the tail).
	}
}

// spin busy-waits on QPC to deadline, watching the interrupt flag.
func (c *waitClock) spin(deadline int64) bool {
	for c.Now() < deadline {
		if c.stop.Load() {
			return false
		}
	}
	return true
}

// Interrupt wakes a WaitUntil in progress and makes later ones return false
// until ClearInterrupt. Safe from any goroutine.
func (c *waitClock) Interrupt() {
	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	c.stop.Store(true)
	_ = windows.SetEvent(c.handles[0])
}

// ClearInterrupt re-arms waiting.
func (c *waitClock) ClearInterrupt() {
	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	_ = windows.ResetEvent(c.handles[0])
	c.stop.Store(false)
}

// Timer resolution for the fallback clock: timeBeginPeriod(1) while at least
// one fallback clock is open.
var (
	periodMu   sync.Mutex
	periodRefs int
)

func beginTimerPeriod() {
	periodMu.Lock()
	defer periodMu.Unlock()
	if periodRefs == 0 {
		_, _, _ = procTimeBeginPeriod.Call(1)
	}
	periodRefs++
}

func endTimerPeriod() {
	periodMu.Lock()
	defer periodMu.Unlock()
	if periodRefs == 0 {
		return
	}
	periodRefs--
	if periodRefs == 0 {
		_, _, _ = procTimeEndPeriod.Call(1)
	}
}

// goClock is the last-resort clock: time.Timer plus a channel for Interrupt.
type goClock struct {
	start time.Time
	mu    sync.Mutex
	stop  chan struct{} // closed while interrupted
}

func newGoClock() *goClock {
	return &goClock{start: time.Now(), stop: make(chan struct{})}
}

func (g *goClock) Now() int64 { return int64(time.Since(g.start)) }

func (g *goClock) WaitUntil(deadline int64) bool {
	g.mu.Lock()
	stop := g.stop
	g.mu.Unlock()
	for {
		select {
		case <-stop:
			return false
		default:
		}
		rem := deadline - g.Now()
		if rem <= 0 {
			return true
		}
		t := time.NewTimer(time.Duration(rem))
		select {
		case <-stop:
			t.Stop()
			return false
		case <-t.C:
		}
	}
}

func (g *goClock) Interrupt() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.stop:
	default:
		close(g.stop)
	}
}

func (g *goClock) ClearInterrupt() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.stop:
		g.stop = make(chan struct{})
	default:
	}
}
