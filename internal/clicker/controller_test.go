package clicker

import (
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/hotkey"
)

func TestToggleRunsAndStops(t *testing.T) {
	r := newRig(t)
	st, err := r.s.Toggle()
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateRunning {
		t.Fatalf("state = %s, want running", st.State)
	}
	r.waitFor("clicks", func() bool { return r.s.Status().Clicks >= 5 })
	st, err = r.s.Toggle()
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateIdle || st.StopCode != StopUser || st.StopReason == "" {
		t.Fatalf("after stop: %+v", st)
	}
	if st.Clicks < 5 {
		t.Fatalf("stopped status lost the click count: %d", st.Clicks)
	}
	r.waitFor("stats", func() bool { return r.s.Stats().Runs == 1 })
	if r.s.Stats().Clicks < 5 {
		t.Fatalf("stats clicks = %d", r.s.Stats().Clicks)
	}
	// The run marked itself as injecting and cleared it again.
	r.mu.Lock()
	inj := append([]bool(nil), r.injecting...)
	r.mu.Unlock()
	if len(inj) < 2 || !inj[0] || inj[len(inj)-1] {
		t.Fatalf("SetInjecting calls = %v, want true ... false", inj)
	}
}

func TestStartCountsDownThenRuns(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) { s.StartDelayMs = 150 })
	st, err := r.s.Start()
	if err != nil {
		t.Fatal(err)
	}
	if st.State != StateCountdown || st.CountdownMs <= 0 || st.CountdownMs > 150 {
		t.Fatalf("start status = %+v, want countdown", st)
	}
	if r.runs.Load() != 0 {
		t.Fatal("engine ran during the countdown")
	}
	r.waitFor("running", func() bool { return r.state() == StateRunning })
	r.waitFor("clicks", func() bool { return r.s.Status().Clicks > 0 })
	r.s.Stop()
}

func TestStopCancelsCountdown(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) { s.StartDelayMs = 200 })
	if _, err := r.s.Start(); err != nil {
		t.Fatal(err)
	}
	st := r.s.Stop()
	if st.State != StateIdle || st.StopCode != StopUser {
		t.Fatalf("stop during countdown = %+v", st)
	}
	time.Sleep(300 * time.Millisecond)
	if r.runs.Load() != 0 || r.state() != StateIdle {
		t.Fatal("a cancelled countdown still started the engine")
	}
}

func TestEscCancelsCountdown(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) { s.StartDelayMs = 500 })
	if _, err := r.s.Start(); err != nil {
		t.Fatal(err)
	}
	r.esc.Store(true)
	r.waitFor("idle", func() bool { return r.state() == StateIdle })
	if r.s.Status().StopCode != StopUser || r.runs.Load() != 0 {
		t.Fatalf("esc: %+v runs=%d", r.s.Status(), r.runs.Load())
	}
}

func TestAlreadyRunning(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	if _, err := r.s.Start(); err == nil || !strings.Contains(err.Error(), ErrAlreadyRunning) {
		t.Fatalf("second start err = %v", err)
	}
}

// A stopped run that is slow to let go must not overwrite the status of the
// run started after it.
func TestStaleRunDoesNotClobberNewRun(t *testing.T) {
	r := newRig(t)
	first := true
	r.s.d.run = func(cfg engine.Config, inj engine.Injector, clk engine.Clock, g engine.Guard, paused func() bool, p func(engine.Progress)) engine.Verdict {
		linger := time.Duration(0)
		if first {
			first = false
			linger = 150 * time.Millisecond
		}
		r.runs.Add(1)
		return fakeRun(linger)(cfg, inj, clk, g, paused, p)
	}
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	r.waitFor("first run", func() bool { return r.runs.Load() == 1 })
	r.s.StopFor(StopHotkey) // detaches at once; the old goroutine lingers
	if _, err := r.s.Toggle(); err != nil {
		t.Fatalf("restart while the old run winds down: %v", err)
	}
	r.waitFor("second run", func() bool { return r.runs.Load() == 2 })
	time.Sleep(250 * time.Millisecond) // the first run returns meanwhile
	if st := r.s.Status(); st.State != StateRunning {
		t.Fatalf("stale run clobbered the new one: %+v", st)
	}
	r.waitFor("both runs counted", func() bool { return r.s.Stats().Runs == 1 })
	r.s.Stop()
	r.waitFor("stats", func() bool { return r.s.Stats().Runs == 2 })
	// Injecting stayed on while the new run was live, and is off at the end.
	r.mu.Lock()
	last := r.injecting[len(r.injecting)-1]
	r.mu.Unlock()
	if last {
		t.Fatal("SetInjecting left on after both runs")
	}
}

func TestFailsafeStopToastsAndReports(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	r.waitFor("clicks", func() bool { return r.s.Status().Clicks > 0 })
	r.guard.set(engine.Verdict{Kind: engine.Stop, Code: string(StopCorner), Reason: "Top-left corner failsafe"})
	r.waitFor("idle", func() bool { return r.state() == StateIdle })
	st := r.s.Status()
	if st.StopCode != StopCorner || st.StopReason != "Top-left corner failsafe" {
		t.Fatalf("status = %+v", st)
	}
	r.waitFor("toast", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.toasts) > 0
	})
	r.mu.Lock()
	toasts := append([]string(nil), r.toasts...)
	r.mu.Unlock()
	if len(toasts) != 1 || toasts[0] != "Top-left corner failsafe" {
		t.Fatalf("toasts = %v", toasts)
	}
}

func TestUserStopDoesNotToast(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	r.s.Stop()
	time.Sleep(20 * time.Millisecond)
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.toasts) != 0 {
		t.Fatalf("toasts = %v", r.toasts)
	}
}

func TestClickLimitReason(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) {
		s.ToastOnStop = false
		s.Profile.Limit = Limit{Kind: LimitClicks, Clicks: 3, Seconds: 60}
	})
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	r.waitFor("idle", func() bool { return r.state() == StateIdle })
	st := r.s.Status()
	if st.StopCode != StopClickLimit || st.StopReason != "Click limit reached (3)" || st.Clicks != 3 {
		t.Fatalf("status = %+v", st)
	}
}

func TestPauseResume(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	r.waitFor("clicks", func() bool { return r.s.Status().Clicks > 0 })
	st := r.s.SetPaused(true)
	if st.State != StatePaused || st.PauseCause != PauseUser {
		t.Fatalf("paused status = %+v", st)
	}
	time.Sleep(20 * time.Millisecond)
	n := r.s.Status().Clicks
	time.Sleep(40 * time.Millisecond)
	if got := r.s.Status().Clicks; got != n {
		t.Fatalf("clicked while paused: %d -> %d", n, got)
	}
	st = r.s.SetPaused(false)
	if st.State != StateRunning || st.PauseCause != PauseNone {
		t.Fatalf("resumed status = %+v", st)
	}
	r.waitFor("clicks after resume", func() bool { return r.s.Status().Clicks > n })
}

func TestZonePauseShowsInStatus(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	r.guard.set(engine.Verdict{Kind: engine.Pause})
	r.waitFor("zone pause", func() bool {
		st := r.s.Status()
		return st.State == StatePaused && st.PauseCause == PauseZone
	})
	r.guard.set(engine.Verdict{})
	r.waitFor("running", func() bool { return r.state() == StateRunning })
}

func TestDisarmStopsAndBlocksStart(t *testing.T) {
	r := newRig(t)
	if on, ok := r.keys.isActive(actionToggle); !ok || !on {
		t.Fatal("Init did not activate clickerToggle")
	}
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.SetArmed(false); err != nil {
		t.Fatal(err)
	}
	st := r.s.Status()
	if st.State != StateIdle || st.StopCode != StopDisarmed || st.Armed {
		t.Fatalf("after disarm: %+v", st)
	}
	if on, _ := r.keys.isActive(actionToggle); on {
		t.Fatal("clickerToggle still intercepted while disarmed")
	}
	if on, _ := r.keys.isActive(actionArm); !on {
		t.Fatal("clickerArm must stay active")
	}
	if _, err := r.s.Toggle(); err == nil || !strings.Contains(err.Error(), ErrDisarmed) {
		t.Fatalf("start while disarmed: %v", err)
	}
	r.s.HotkeyArm()
	if !r.s.GetSettings().Armed {
		t.Fatal("HotkeyArm did not re-arm")
	}
}

func TestHotkeyHoldModePassesTriggerVK(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) { s.HotkeyMode = HotkeyHold })
	r.s.HotkeyToggle()
	r.waitFor("running", func() bool { return r.state() == StateRunning })
	if vk := r.lastSafety().HoldTriggerVK; vk != 'Y' {
		t.Fatalf("HoldTriggerVK = %d, want the trigger key", vk)
	}
	r.guard.set(engine.Verdict{Kind: engine.Stop, Code: string(StopHoldRelease)})
	r.waitFor("idle", func() bool { return r.state() == StateIdle })
	if c := r.s.Status().StopCode; c != StopHoldRelease {
		t.Fatalf("stop code = %s", c)
	}
}

func TestHotkeyToggleModeStartsAndStops(t *testing.T) {
	r := newRig(t)
	r.s.HotkeyToggle()
	r.waitFor("running", func() bool { return r.state() == StateRunning })
	if vk := r.lastSafety().HoldTriggerVK; vk != 0 {
		t.Fatalf("toggle mode set HoldTriggerVK = %d", vk)
	}
	r.s.HotkeyToggle()
	if st := r.s.Status(); st.State != StateIdle || st.StopCode != StopHotkey {
		t.Fatalf("status = %+v", st)
	}
}

func TestTrayToggleAndStatusHotkeyLabel(t *testing.T) {
	r := newRig(t)
	r.s.TrayToggle()
	r.waitFor("running", func() bool { return r.state() == StateRunning })
	if h := r.s.Status().Hotkey; h != "Ctrl + Shift + Y" {
		t.Fatalf("hotkey label = %q", h)
	}
	r.waitFor("pill", func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, p := range r.pills {
			if strings.HasPrefix(p, "Clicking · ") && strings.HasSuffix(p, " · Ctrl+Shift+Y to stop") {
				return true
			}
		}
		return false
	})
	r.s.TrayToggle()
	if c := r.s.Status().StopCode; c != StopTray {
		t.Fatalf("stop code = %s", c)
	}
}

func TestConflictRefusesStart(t *testing.T) {
	r := newRig(t)
	r.keys.conflict[hotkey.Binding{Shift: true, Key: 'Q'}] = "Open capture overlay"
	r.settings(func(s *Settings) {
		s.Profile.Input = InputKeyboard
		s.Profile.Key = "q"
		s.Profile.Uppercase = true
	})
	_, err := r.s.Toggle()
	if err == nil || !strings.Contains(err.Error(), "clicker: "+ErrHotkeyConflict+": ") || !strings.Contains(err.Error(), "Open capture overlay") {
		t.Fatalf("err = %v", err)
	}
	if r.state() != StateIdle {
		t.Fatal("conflicting run started")
	}
}

func TestStartErrors(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) { s.Profile.Input = InputKeyboard; s.Profile.Key = "" })
	if _, err := r.s.Toggle(); err == nil || !strings.Contains(err.Error(), ErrNoKey) {
		t.Fatalf("no key: %v", err)
	}
	r.s.d.keyScan = func(string) (uint16, bool, bool, error) { return 0, false, false, errTest }
	r.settings(func(s *Settings) { s.Profile.Key = "F5" })
	if _, err := r.s.Toggle(); err == nil || !strings.Contains(err.Error(), ErrUnknownKey) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestShortSendSetsWarning(t *testing.T) {
	r := newRig(t)
	r.inj.short = true
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	r.waitFor("warning", func() bool { return r.s.Status().Warning == blockedWarning })
}

func TestGuardWarningWins(t *testing.T) {
	r := newRig(t)
	r.guard.warn = "game.exe runs as administrator"
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	r.waitFor("warning", func() bool { return r.s.Status().Warning == "game.exe runs as administrator" })
}

func TestFallbackClockWarns(t *testing.T) {
	r := newRig(t)
	r.s.d.newClock = func() (engine.Clock, func(), error) { return nil, nil, errTest }
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	r.waitFor("warning", func() bool { return strings.HasPrefix(r.s.Status().Warning, "Low-precision timer") })
}

func TestStatusEmitsThrottled(t *testing.T) {
	r := newRig(t)
	r.mu.Lock()
	r.statuses = nil
	r.mu.Unlock()
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(550 * time.Millisecond)
	r.s.Stop()
	r.mu.Lock()
	n := len(r.statuses)
	r.mu.Unlock()
	// ~300 clicks happened; emits are the start, ~5 ticks and the stop.
	if n < 3 || n > 10 {
		t.Fatalf("%d status events in 550 ms, want about 7", n)
	}
}

func TestPickBlocksStartAndAddsPoints(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	if err := r.s.StartPointPick(); err != nil {
		t.Fatal(err)
	}
	if st := r.s.Status(); st.State != StateIdle || st.StopCode != StopPick {
		t.Fatalf("pick did not stop the run: %+v", st)
	}
	if err := r.s.StartZonePick(""); err == nil || !strings.Contains(err.Error(), ErrPickActive) {
		t.Fatalf("second pick: %v", err)
	}
	if _, err := r.s.Toggle(); err == nil || !strings.Contains(err.Error(), ErrPickActive) {
		t.Fatalf("start during pick: %v", err)
	}
	r.mu.Lock()
	cb := *r.pickCB
	r.mu.Unlock()
	cb.OnPoint(100, 200, true)
	cb.OnPoint(300, 400, true)
	r.waitFor("points", func() bool { return len(r.s.GetSettings().Profile.ClickPoints.Points) == 2 })
	cb.OnDelete(104, 203)
	r.waitFor("delete", func() bool { return len(r.s.GetSettings().Profile.ClickPoints.Points) == 1 })
	if p := r.s.GetSettings().Profile.ClickPoints.Points[0]; p.X != 300 || p.Y != 400 || p.Clicks != 1 {
		t.Fatalf("remaining point = %+v", p)
	}
	cb.OnEnd(false)
	r.waitFor("pick end", func() bool {
		r.s.mu.Lock()
		defer r.s.mu.Unlock()
		return !r.s.picking
	})
	// The picked points were persisted.
	if got := loadSettings().Profile.ClickPoints.Points; len(got) != 1 {
		t.Fatalf("persisted points = %+v", got)
	}
}

func TestZonePickAddsThenEdits(t *testing.T) {
	r := newRig(t)
	if err := r.s.StartZonePick(""); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	cb := *r.pickCB
	r.mu.Unlock()
	cb.OnZone(50, 60, 10, 20) // dragged up-left
	r.waitFor("zone", func() bool { return len(r.s.GetSettings().StopZones) == 1 })
	z := r.s.GetSettings().StopZones[0]
	if z.X != 10 || z.Y != 20 || z.W != 40 || z.H != 40 || z.Action != ZoneStop {
		t.Fatalf("zone = %+v", z)
	}
	r.s.CancelPick()
	r.waitFor("pick end", func() bool {
		r.s.mu.Lock()
		defer r.s.mu.Unlock()
		return !r.s.picking
	})
	if err := r.s.StartZonePick(z.ID); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	cb = *r.pickCB
	r.mu.Unlock()
	cb.OnZone(0, 0, 5, 5)
	r.waitFor("edit", func() bool { return r.s.GetSettings().StopZones[0].W == 5 })
	if n := len(r.s.GetSettings().StopZones); n != 1 {
		t.Fatalf("editing added a zone: %d", n)
	}
}

func TestStartZoneMonitor(t *testing.T) {
	r := newRig(t)
	r.cursorX.Store(500) // inside the zone before it exists: must not start
	r.settings(func(s *Settings) {
		s.Profile.ZonesEnabled = true
		s.Profile.Corners.Enabled = false
		s.StopZones = []StopZone{{X: 400, Y: 0, W: 200, H: 100, Action: ZoneStart}}
	})
	time.Sleep(250 * time.Millisecond)
	if r.state() != StateIdle {
		t.Fatal("the first sample started a run")
	}
	r.cursorX.Store(10)
	time.Sleep(150 * time.Millisecond)
	r.cursorX.Store(450)
	r.waitFor("zone start", func() bool { return r.state() == StateRunning })
	if idx := r.lastSafety().StartZoneIndex; idx != 0 {
		t.Fatalf("StartZoneIndex = %d", idx)
	}
	r.cursorX.Store(10)
	r.waitFor("zone stop", func() bool { return r.state() == StateIdle })
	if c := r.s.Status().StopCode; c != StopZoneLeft {
		t.Fatalf("stop code = %s", c)
	}
	// Disarming stops the monitor.
	if _, err := r.s.SetArmed(false); err != nil {
		t.Fatal(err)
	}
	r.s.mu.Lock()
	running := r.s.monStop != nil
	r.s.mu.Unlock()
	if running {
		t.Fatal("monitor still running while disarmed")
	}
}

func TestSafetyPreviewOnlyOnChange(t *testing.T) {
	r := newRig(t)
	r.mu.Lock()
	base := r.scenes
	r.mu.Unlock()
	if base != 0 {
		t.Fatal("Init flashed the overlay")
	}
	r.settings(func(*Settings) {}) // unchanged
	r.settings(func(s *Settings) { s.StartDelayMs = 1000 })
	r.mu.Lock()
	if r.scenes != 0 {
		r.mu.Unlock()
		t.Fatal("non-geometry change flashed the overlay")
	}
	r.mu.Unlock()
	r.settings(func(s *Settings) { s.Profile.Corners.TL = 80 })
	r.mu.Lock()
	n := r.scenes
	r.mu.Unlock()
	if n != 1 {
		t.Fatalf("corner change flashed %d times", n)
	}
	r.settings(func(s *Settings) { s.ShowSafetyOverlay = false; s.Profile.Corners.TL = 90 })
	r.s.PreviewSafety()
	r.mu.Lock()
	n = r.scenes
	r.mu.Unlock()
	if n != 2 {
		t.Fatalf("scenes = %d, want only the explicit preview", n)
	}
}

func TestListProcessesMergesSaved(t *testing.T) {
	r := newRig(t)
	r.settings(func(s *Settings) {
		s.Profile.ProcessList.Entries = []ProcessEntry{{Exe: "zed.exe", Enabled: true}, {Exe: "game.exe", Enabled: true}}
	})
	list, err := r.s.ListProcesses()
	if err != nil {
		t.Fatal(err)
	}
	want := []ProcessInfo{{Exe: "game.exe", Title: "Game", Running: true}, {Exe: "notepad.exe", Running: true}, {Exe: "zed.exe"}}
	if len(list) != len(want) {
		t.Fatalf("list = %+v", list)
	}
	for i := range want {
		if list[i] != want[i] {
			t.Fatalf("list[%d] = %+v, want %+v", i, list[i], want[i])
		}
	}
}

func TestShutdownStopsRun(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	r.s.Shutdown()
	if st := r.s.Status(); st.State != StateIdle || st.StopCode != StopShutdown {
		t.Fatalf("status = %+v", st)
	}
}

func TestPresetsLockedWhileRunning(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.Toggle(); err != nil {
		t.Fatal(err)
	}
	defer r.s.Stop()
	for name, fn := range map[string]func() error{
		"save":   func() error { _, err := r.s.SavePreset("x"); return err },
		"apply":  func() error { _, err := r.s.ApplyPreset("factory-rapid"); return err },
		"delete": func() error { _, err := r.s.DeletePreset("factory-rapid"); return err },
		"import": func() error { _, err := r.s.ImportPreset(); return err },
	} {
		if err := fn(); err == nil || !strings.Contains(err.Error(), ErrPresetLocked) {
			t.Errorf("%s while running: %v", name, err)
		}
	}
}
