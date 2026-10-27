//go:build windows

package sys

import (
	"math"
	"os"
	"runtime"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/inputtag"
)

// Opt-in (TORU_SMOKE=1) smoke tests against the real desktop. They inject
// nothing visible: one zero-delta relative mouse move, and timer waits.

var (
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	procRtlMoveMemory       = kernel32.NewProc("RtlMoveMemory")
)

const (
	whMouseLL       = 14 // WH_MOUSE_LL
	wmQuit          = 0x0012
	llmhfInjected   = 0x1
	smokeHookWaitMs = 2000
)

// msllhookstruct mirrors MSLLHOOKSTRUCT.
type msllhookstruct struct {
	X, Y        int32
	MouseData   uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

var (
	mouseHookOnce sync.Once
	mouseHookCB   uintptr
	mouseHookSeen = make(chan msllhookstruct, 1)
)

// smokeMouseProc records the first event carrying Toru's tag. Real mouse
// movement from the user also arrives here and is ignored.
func smokeMouseProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode == 0 {
		var ev msllhookstruct
		_, _, _ = procRtlMoveMemory.Call(uintptr(unsafe.Pointer(&ev)), lParam, unsafe.Sizeof(ev))
		if ev.DwExtraInfo == inputtag.Toru {
			select {
			case mouseHookSeen <- ev:
			default:
			}
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, uintptr(nCode), wParam, lParam)
	return r
}

func TestInjectionTagSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	// A zero-delta relative MOUSEEVENTF_MOVE (what PLAN §6.2 asks for) never
	// reaches WH_MOUSE_LL hooks: Windows drops it as no movement, and a zero
	// wheel event is dropped too (both tried on Windows 11 26200). So the test
	// sends the real GlideStep path to where the cursor already is, and puts the
	// cursor back with SetCursorPos if normalization rounded it a pixel.
	inj := NewInjector().(*injector)
	x, y, ok := CursorPos()
	if !ok {
		t.Fatal("GetCursorPos failed")
	}
	hookSend(t, func() int {
		if err := inj.GlideStep(x, y); err != nil {
			t.Logf("GlideStep: %v", err)
			return 0
		}
		return 1
	})
	// Undo a rounding pixel only; a bigger change is the user moving the mouse,
	// which the test must not yank back.
	if x2, y2, _ := CursorPos(); x2 != x || y2 != y {
		if abs32(x2-x) <= 1 && abs32(y2-y) <= 1 {
			_ = inj.Teleport(x, y)
		}
		t.Logf("cursor %d,%d -> %d,%d after the glide step", x, y, x2, y2)
	}
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// hookSend installs a WH_MOUSE_LL hook on a locked thread, calls send (which
// returns how many events SendInput accepted) and waits for the hook to see an
// event tagged inputtag.Toru with LLMHF_INJECTED.
func hookSend(t *testing.T, send func() int) {
	t.Helper()
	mouseHookOnce.Do(func() { mouseHookCB = syscall.NewCallback(smokeMouseProc) })

	ready := make(chan uint32, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		tid, _, _ := procGetCurrentThreadId.Call()
		hook, _, _ := procSetWindowsHookExW.Call(whMouseLL, mouseHookCB, 0, 0)
		if hook == 0 {
			ready <- 0
			return
		}
		defer func() { _, _, _ = procUnhookWindowsHookEx.Call(hook) }()
		ready <- uint32(tid)
		var m [48]byte // MSG
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
		}
	}()
	tid := <-ready
	if tid == 0 {
		t.Fatal("SetWindowsHookExW(WH_MOUSE_LL) failed")
	}
	defer func() {
		_, _, _ = procPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
		<-done
	}()

	if n := send(); n != 1 {
		t.Fatalf("SendInput sent %d of 1 (blocked?)", n)
	}
	select {
	case ev := <-mouseHookSeen:
		if ev.Flags&llmhfInjected == 0 {
			t.Errorf("hook flags = %#x, want LLMHF_INJECTED", ev.Flags)
		}
		t.Logf("hook saw tagged move: extra=%#x flags=%#x at (%d,%d)", ev.DwExtraInfo, ev.Flags, ev.X, ev.Y)
	case <-time.After(smokeHookWaitMs * time.Millisecond):
		t.Fatal("the mouse hook never saw an event with dwExtraInfo == inputtag.Toru")
	}
}

// countingInjector counts sends and stamps them with the clock.
type countingInjector struct {
	clk   engine.Clock
	times []int64
}

func (c *countingInjector) Send(ops []engine.Op) (int, error) {
	c.times = append(c.times, c.clk.Now())
	return len(ops), nil
}
func (c *countingInjector) Teleport(x, y int32) error    { return nil }
func (c *countingInjector) GlideStep(x, y int32) error   { return nil }
func (c *countingInjector) Cursor() (int32, int32, bool) { return 0, 0, false }

// TestTimerAccuracySmoke drives the real Clock on a BeginRun thread with a
// drift-free deadline schedule and a counting fake injector, and reports the
// interval error.
func TestTimerAccuracySmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	for _, tc := range []struct {
		name   string
		hz     int
		dur    time.Duration
		strict bool
	}{
		{"100Hz", 100, 2 * time.Second, true},
		{"1000Hz", 1000, 500 * time.Millisecond, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			end, warn := BeginRun()
			defer end()
			if warn != "" {
				t.Logf("BeginRun warning: %s", warn)
			}
			clk, closeFn, err := NewClock()
			if err != nil {
				t.Fatalf("NewClock: %v", err)
			}
			defer closeFn()

			inj := &countingInjector{clk: clk}
			interval := int64(time.Second) / int64(tc.hz)
			n := int(tc.dur / time.Duration(interval))
			ops := []engine.Op{{Kind: engine.OpMouseDown, Button: engine.ButtonLeft}, {Kind: engine.OpMouseUp, Button: engine.ButtonLeft}}
			late := make([]float64, 0, n)
			deadline := clk.Now()
			for i := 0; i < n; i++ {
				deadline += interval
				if !clk.WaitUntil(deadline) {
					t.Fatal("WaitUntil interrupted")
				}
				late = append(late, float64(clk.Now()-deadline))
				_, _ = inj.Send(ops)
			}

			errs := make([]float64, 0, n-1)
			sum := 0.0
			for i := 1; i < len(inj.times); i++ {
				d := float64(inj.times[i] - inj.times[i-1])
				sum += d
				errs = append(errs, math.Abs(d-float64(interval)))
			}
			mean := sum / float64(len(errs))
			meanErrPct := math.Abs(mean-float64(interval)) / float64(interval) * 100
			slices.Sort(errs)
			slices.Sort(late)
			pct := func(s []float64, p float64) time.Duration {
				return time.Duration(s[int(p*float64(len(s)-1))])
			}
			t.Logf("%s: %d clicks, mean interval %v (target %v, error %.3f%%), interval error p50 %v p99 %v max %v; wake lateness p50 %v p99 %v",
				tc.name, len(inj.times), time.Duration(mean), time.Duration(interval), meanErrPct,
				pct(errs, 0.5), pct(errs, 0.99), time.Duration(errs[len(errs)-1]),
				pct(late, 0.5), pct(late, 0.99))
			if meanErrPct >= 1 {
				t.Errorf("mean interval error %.3f%% >= 1%%", meanErrPct)
			}
			if tc.strict && pct(errs, 0.99) >= time.Millisecond {
				t.Errorf("p99 interval error %v >= 1ms", pct(errs, 0.99))
			}
		})
	}
}

// TestPowerThrottlingSmoke checks the opt-out is applied during a run and
// restored after.
func TestPowerThrottlingSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	before, ok := getThrottling()
	if !ok {
		t.Skip("GetProcessInformation(ProcessPowerThrottling) unsupported")
	}
	end, _ := BeginRun()
	during, _ := getThrottling()
	end()
	after, _ := getThrottling()
	t.Logf("throttling before %+v during %+v after %+v", before, during, after)
	if during.ControlMask&powerThrottlingExecutionSpeed == 0 || during.StateMask&powerThrottlingExecutionSpeed != 0 {
		t.Errorf("during run: %+v, want EXECUTION_SPEED controlled and off", during)
	}
	if after.ControlMask != before.ControlMask || after.StateMask != before.StateMask {
		t.Errorf("not restored: before %+v after %+v", before, after)
	}
}
