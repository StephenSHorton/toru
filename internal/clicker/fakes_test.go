package clicker

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/clicker/overlay"
	"github.com/StephenSHorton/toru/internal/clicker/safety"
	"github.com/StephenSHorton/toru/internal/hotkey"
)

// isolateConfig points os.UserConfigDir at a temp dir (APPDATA on Windows,
// XDG_CONFIG_HOME / HOME elsewhere) so tests never touch the real clicker.json.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return dir
}

// fakeClock is an engine.Clock whose waits are real sleeps and whose
// interrupt is a flag.
type fakeClock struct {
	intr atomic.Bool
}

func (c *fakeClock) Now() int64 { return time.Now().UnixNano() }
func (c *fakeClock) WaitUntil(deadline int64) bool {
	for time.Now().UnixNano() < deadline {
		if c.intr.Load() {
			return false
		}
		time.Sleep(time.Millisecond)
	}
	return !c.intr.Load()
}
func (c *fakeClock) Interrupt()      { c.intr.Store(true) }
func (c *fakeClock) ClearInterrupt() { c.intr.Store(false) }

// fakeInjector counts sends; short makes every Send report one op fewer.
type fakeInjector struct {
	sends atomic.Int64
	short bool
}

func (f *fakeInjector) Send(ops []engine.Op) (int, error) {
	f.sends.Add(1)
	if f.short {
		return len(ops) - 1, nil
	}
	return len(ops), nil
}
func (f *fakeInjector) Teleport(int32, int32) error  { return nil }
func (f *fakeInjector) GlideStep(int32, int32) error { return nil }
func (f *fakeInjector) Cursor() (int32, int32, bool) { return 0, 0, true }

// scriptGuard returns whatever verdict the test last set.
type scriptGuard struct {
	mu   sync.Mutex
	v    engine.Verdict
	warn string
}

func (g *scriptGuard) set(v engine.Verdict) { g.mu.Lock(); g.v = v; g.mu.Unlock() }
func (g *scriptGuard) Check(int64, int64) engine.Verdict {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.v
}
func (g *scriptGuard) Warning() string { g.mu.Lock(); defer g.mu.Unlock(); return g.warn }

// fakeRun is a tiny engine.Run: one click every 2 ms, guard checked each
// cycle, pause and interrupt honoured like the real contract. ignoreStop
// makes it keep going for linger after an interrupt (a slow release).
func fakeRun(linger time.Duration) func(engine.Config, engine.Injector, engine.Clock, engine.Guard, func() bool, func(engine.Progress)) engine.Verdict {
	return func(cfg engine.Config, inj engine.Injector, clk engine.Clock, g engine.Guard, paused func() bool, onProgress func(engine.Progress)) engine.Verdict {
		var clicks int64
		for {
			v := g.Check(clk.Now(), clicks)
			if v.Kind == engine.Stop {
				return v
			}
			if v.Kind == engine.Continue && !paused() {
				_, _ = inj.Send([]engine.Op{{Kind: engine.OpMouseDown, Button: cfg.Button}, {Kind: engine.OpMouseUp, Button: cfg.Button}})
				clicks++
				if onProgress != nil {
					onProgress(engine.Progress{Clicks: clicks, ActivePoint: -1})
				}
				if cfg.ClickLimit > 0 && clicks >= cfg.ClickLimit {
					return engine.Verdict{Kind: engine.Stop, Code: engine.CodeClickLimit}
				}
			}
			if !clk.WaitUntil(clk.Now() + int64(2*time.Millisecond)) {
				if paused() {
					clk.ClearInterrupt()
					continue
				}
				time.Sleep(linger)
				return engine.Verdict{Kind: engine.Stop, Code: engine.CodeInterrupted}
			}
		}
	}
}

// fakeKeys is a bindingSource.
type fakeKeys struct {
	mu       sync.Mutex
	bindings map[string]hotkey.Binding
	conflict map[hotkey.Binding]string
	active   map[string]bool
}

func newFakeKeys() *fakeKeys {
	return &fakeKeys{
		bindings: map[string]hotkey.Binding{actionToggle: hotkey.DefaultClickerToggle},
		conflict: map[hotkey.Binding]string{},
		active:   map[string]bool{},
	}
}

func (k *fakeKeys) Binding(id string) (hotkey.Binding, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	b, ok := k.bindings[id]
	return b, ok
}
func (k *fakeKeys) Conflicts(b hotkey.Binding) (string, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	a, ok := k.conflict[b]
	return a, ok
}
func (k *fakeKeys) SetActive(id string, on bool) {
	k.mu.Lock()
	k.active[id] = on
	k.mu.Unlock()
}
func (k *fakeKeys) isActive(id string) (bool, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	on, ok := k.active[id]
	return on, ok
}

// fakeSystem is a safety.System with fixed monitors.
type fakeSystem struct{}

func (fakeSystem) Cursor() (int32, int32, bool)  { return 0, 0, true }
func (fakeSystem) Monitors() []safety.Rect       { return []safety.Rect{{L: 0, T: 0, R: 1920, B: 1080}} }
func (fakeSystem) Foreground() safety.Foreground { return safety.Foreground{} }
func (fakeSystem) TaskSwitcherActive() bool      { return false }
func (fakeSystem) PhysicallyDown(uint16) bool    { return false }
func (fakeSystem) SelfElevated() bool            { return false }

// rig is a service wired to fakes, with recorders for everything it emits.
type rig struct {
	t      *testing.T
	s      *ClickerService
	keys   *fakeKeys
	guard  *scriptGuard
	inj    *fakeInjector
	safeMu sync.Mutex
	safe   []safety.Config

	mu        sync.Mutex
	events    []string
	statuses  []Status
	toasts    []string
	pills     []string
	injecting []bool
	scenes    int
	pickCB    *overlay.PickCallbacks
	pickCxl   atomic.Int32
	esc       atomic.Bool
	cursorX   atomic.Int32
	runs      atomic.Int32
}

func newRig(t *testing.T) *rig {
	t.Helper()
	isolateConfig(t)
	r := &rig{t: t, keys: newFakeKeys(), guard: &scriptGuard{}, inj: &fakeInjector{}}
	d := deps{
		run: func(cfg engine.Config, inj engine.Injector, clk engine.Clock, g engine.Guard, paused func() bool, p func(engine.Progress)) engine.Verdict {
			r.runs.Add(1)
			return fakeRun(0)(cfg, inj, clk, g, paused, p)
		},
		newInjector:     func() engine.Injector { return r.inj },
		newClock:        func() (engine.Clock, func(), error) { return &fakeClock{}, func() {}, nil },
		newFallbackClk:  func() (engine.Clock, func()) { return &fakeClock{}, func() {} },
		beginRun:        func() (func(), string) { return func() {}, "" },
		keyScan:         func(k string) (uint16, bool, bool, error) { return 0x1E, false, isLetterKey(k), nil },
		capsLock:        func() bool { return false },
		keyRepeat:       func() engine.KeyRepeat { return engine.KeyRepeat{DelayNs: 5e8, PeriodNs: 3e7} },
		doubleClickTime: func() time.Duration { return 500 * time.Millisecond },
		cursorPos:       func() (int32, int32, bool) { return r.cursorX.Load(), 10, true },
		newGuard: func(cfg safety.Config, _ safety.System) safety.Guard {
			r.safeMu.Lock()
			r.safe = append(r.safe, cfg)
			r.safeMu.Unlock()
			return r.guard
		},
		newSystem: func() safety.System { return fakeSystem{} },
		zoneAt: func(zones []safety.Zone, x, y int32) (uint8, int) {
			for i, z := range zones {
				if x >= z.Rect.L && x < z.Rect.R && y >= z.Rect.T && y < z.Rect.B {
					return z.Action, i
				}
			}
			return 0, -1
		},
		listProcesses: func() ([]safety.ProcessInfo, error) {
			return []safety.ProcessInfo{{Exe: "Game.exe", Title: "Game"}, {Exe: "notepad.exe"}}, nil
		},
		showSafety: func(overlay.SafetyScene, bool) { r.mu.Lock(); r.scenes++; r.mu.Unlock() },
		showPoints: func([]overlay.ScenePoint, bool) {},
		startPick: func(_ overlay.PickKind, _ overlay.SafetyScene, _ []overlay.ScenePoint, cb overlay.PickCallbacks) (func(), error) {
			r.mu.Lock()
			r.pickCB = &cb
			r.mu.Unlock()
			return func() { r.pickCxl.Add(1); cb.OnEnd(true) }, nil
		},
		updatePickPoints: func([]overlay.ScenePoint) {},
		indicatorShow:    func(text string) { r.mu.Lock(); r.pills = append(r.pills, text); r.mu.Unlock() },
		indicatorHide:    func() {},
		overlayShutdown:  func() {},
		toast:            func(n toast.Note) { r.mu.Lock(); r.toasts = append(r.toasts, n.Body); r.mu.Unlock() },
		physicallyDown:   func(vk uint16) bool { return vk == escVK && r.esc.Load() },
		triggerVKs:       func(hotkey.Binding) []uint16 { return []uint16{'Y'} },
		setInjecting:     func(on bool) { r.mu.Lock(); r.injecting = append(r.injecting, on); r.mu.Unlock() },
		now:              time.Now,
		saveDialog:       func(string, string) (string, error) { return "", nil },
		openDialog:       func(string) (string, error) { return "", nil },
	}
	r.s = newService(d)
	r.s.keys = r.keys
	r.s.emit = func(name string, data any) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, name)
		if st, ok := data.(Status); ok {
			r.statuses = append(r.statuses, st)
		}
	}
	r.s.Init()
	t.Cleanup(r.s.Shutdown)
	// Fresh installs start disarmed until the page opens; the rig acts as a
	// user who has opened it.
	if _, err := r.s.PageOpened(); err != nil {
		t.Fatal(err)
	}
	return r
}

// waitFor polls cond for up to 2 s.
func (r *rig) waitFor(what string, cond func() bool) {
	r.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	r.t.Fatalf("timed out waiting for %s (status %+v)", what, r.s.Status())
}

func (r *rig) state() RunState { return r.s.Status().State }

func (r *rig) lastSafety() safety.Config {
	r.waitFor("a run to build its guard", func() bool {
		r.safeMu.Lock()
		defer r.safeMu.Unlock()
		return len(r.safe) > 0
	})
	r.safeMu.Lock()
	defer r.safeMu.Unlock()
	return r.safe[len(r.safe)-1]
}

// settings edits the settings through SetSettings.
func (r *rig) settings(mut func(*Settings)) {
	r.t.Helper()
	s := r.s.GetSettings()
	mut(&s)
	if _, err := r.s.SetSettings(s); err != nil {
		r.t.Fatal(err)
	}
}
