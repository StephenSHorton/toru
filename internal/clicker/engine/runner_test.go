package engine

import "testing"

func TestDoubleCountsTwo(t *testing.T) {
	cfg := clickCfg(100*ms, 10)
	cfg.Mode = ModeDouble
	cfg.DoubleGapNs = 30 * ms
	v, in, _, prog := run(t, cfg, nil)
	if v.Code != CodeClickLimit || prog[len(prog)-1].Clicks != 10 {
		t.Fatalf("verdict %+v, progress %+v", v, prog[len(prog)-1])
	}
	if n := len(in.times(OpMouseDown)); n != 10 {
		t.Errorf("%d physical clicks", n)
	}
	// 5 cycles: the last one starts at 400 ms.
	if downs := in.times(OpMouseDown); downs[8] != 400*ms {
		t.Errorf("cycle 5 at %d", downs[8])
	}
}

func TestInterruptedHoldDoesNotCount(t *testing.T) {
	cfg := Config{BaseIntervalNs: 1000 * ms, Mode: ModeMouseHoldInterval, HoldGapNs: 100 * ms, Seed: 1}
	stopAt := int64(1600 * ms) // second hold, 500 ms in
	g := guardFunc(func(now, _ int64) Verdict {
		if now >= stopAt {
			return Verdict{Kind: Stop, Code: "corner", Reason: "Corner"}
		}
		return Verdict{}
	})
	c := &fakeClock{}
	in := &recInjector{clk: c}
	var clicks int64
	v := Run(cfg, in, c, g, nil, func(p Progress) { clicks = p.Clicks })
	if v.Code != "corner" || clicks != 1 {
		t.Fatalf("verdict %+v, clicks %d", v, clicks)
	}
	ups := in.times(OpMouseUp)
	if len(ups) != 2 || ups[1] != stopAt {
		t.Errorf("ups %v: the cut hold must release at once", ups)
	}
}

func TestClickHoldReleasedOnStop(t *testing.T) {
	cfg := clickCfg(1000*ms, 0)
	cfg.DutyFraction = 0.9
	c := &fakeClock{}
	in := &recInjector{clk: c}
	c.at(300*ms, func(c *fakeClock) { c.Interrupt() })
	var calls int
	v := Run(cfg, in, c, nil, nil, func(Progress) { calls++ })
	if v.Code != CodeInterrupted || calls != 0 {
		t.Fatalf("verdict %+v, %d progress calls", v, calls)
	}
	if got := kinds(in.ops()); !eqKinds(got, []OpKind{OpMouseDown, OpMouseUp}) {
		t.Errorf("ops %v", got)
	}
}

func TestGuardStopWithinOneSlice(t *testing.T) {
	g := guardFunc(func(now, _ int64) Verdict {
		if now >= 25*ms {
			return Verdict{Kind: Stop, Code: "edge", Reason: "Edge"}
		}
		return Verdict{}
	})
	c := &fakeClock{}
	in := &recInjector{clk: c}
	v := Run(clickCfg(5000*ms, 0), in, c, g, nil, nil)
	if v.Code != "edge" || v.Kind != Stop {
		t.Fatalf("verdict %+v", v)
	}
	if c.now > 25*ms+guardSliceNs {
		t.Errorf("stopped at %d, more than a slice late", c.now)
	}
}

func TestGuardCheckedBeforeEveryClick(t *testing.T) {
	var checks []int64
	g := guardFunc(func(now, clicks int64) Verdict {
		checks = append(checks, clicks)
		return Verdict{}
	})
	c := &fakeClock{}
	Run(clickCfg(5*ms, 50), &recInjector{clk: c}, c, g, nil, nil)
	if len(checks) != 50 {
		t.Errorf("%d checks for 50 clicks at 5 ms", len(checks))
	}
	for i, n := range checks {
		if n != int64(i) {
			t.Fatalf("check %d saw %d clicks", i, n)
		}
	}
}

func TestGuardPauseReleasesAndResumes(t *testing.T) {
	cfg := Config{BaseIntervalNs: 1000 * ms, Mode: ModeMouseHoldInterval, HoldGapNs: 0, ClickLimit: 2, Seed: 1}
	g := guardFunc(func(now, _ int64) Verdict {
		if now >= 200*ms && now < 700*ms {
			return Verdict{Kind: Pause, Code: "zonePause"}
		}
		return Verdict{}
	})
	c := &fakeClock{}
	in := &recInjector{clk: c}
	v := Run(cfg, in, c, g, nil, nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	downs, ups := in.times(OpMouseDown), in.times(OpMouseUp)
	// Hold 1 cut at 200 (released, uncounted); resumed at 700; then two
	// complete holds.
	wantD := []int64{0, 700 * ms, 1700 * ms}
	wantU := []int64{200 * ms, 1700 * ms, 2700 * ms}
	if len(downs) != 3 || len(ups) != 3 {
		t.Fatalf("downs %v ups %v", downs, ups)
	}
	for i := range wantD {
		if downs[i] != wantD[i] || ups[i] != wantU[i] {
			t.Fatalf("downs %v ups %v", downs, ups)
		}
	}
}

func TestStopWhilePaused(t *testing.T) {
	paused := false
	c := &fakeClock{}
	in := &recInjector{clk: c}
	c.at(55*ms, func(c *fakeClock) { paused = true; c.Interrupt() })
	c.at(300*ms, func(c *fakeClock) { paused = false; c.Interrupt() })
	v := Run(clickCfg(10*ms, 0), in, c, nil, func() bool { return paused }, nil)
	if v.Code != CodeInterrupted {
		t.Fatalf("verdict %+v", v)
	}
	if c.now != 300*ms {
		t.Errorf("stopped at %d", c.now)
	}
	if n := len(in.times(OpMouseDown)); n != 6 {
		t.Errorf("%d clicks", n)
	}
}

func TestGuardStopWhilePaused(t *testing.T) {
	paused := false
	g := guardFunc(func(now, _ int64) Verdict {
		if now >= 200*ms {
			return Verdict{Kind: Stop, Code: "zoneStop"}
		}
		return Verdict{}
	})
	c := &fakeClock{}
	c.at(55*ms, func(c *fakeClock) { paused = true; c.Interrupt() })
	v := Run(clickCfg(10*ms, 0), &recInjector{clk: c}, c, g, func() bool { return paused }, nil)
	if v.Code != "zoneStop" || c.now > 200*ms+guardSliceNs {
		t.Fatalf("verdict %+v at %d", v, c.now)
	}
}

func TestTimeLimit(t *testing.T) {
	cfg := clickCfg(30*ms, 0)
	cfg.TimeLimitNs = 1000 * ms
	v, in, c, _ := run(t, cfg, nil)
	if v.Code != CodeTimeLimit {
		t.Fatalf("verdict %+v", v)
	}
	if c.now != 1000*ms {
		t.Errorf("ended at %d", c.now)
	}
	if n := len(in.times(OpMouseDown)); n != 34 { // 0, 30, ..., 990
		t.Errorf("%d clicks", n)
	}
}

func TestTimeLimitCountsPause(t *testing.T) {
	cfg := clickCfg(10*ms, 0)
	cfg.TimeLimitNs = 500 * ms
	paused := false
	c := &fakeClock{}
	c.at(105*ms, func(c *fakeClock) { paused = true; c.Interrupt() })
	v := Run(cfg, &recInjector{clk: c}, c, nil, func() bool { return paused }, nil)
	if v.Code != CodeTimeLimit || c.now != 500*ms {
		t.Fatalf("verdict %+v at %d", v, c.now)
	}
}

func TestSendErrorStops(t *testing.T) {
	c := &fakeClock{}
	in := &recInjector{clk: c, fail: true}
	v := Run(clickCfg(10*ms, 0), in, c, nil, nil, nil)
	if v.Code != CodeError {
		t.Fatalf("verdict %+v", v)
	}
}

// TestNoAllocsPerClick enforces what BenchmarkClickLoop shows, inside go test.
func TestNoAllocsPerClick(t *testing.T) {
	for _, mode := range []RunMode{ModeClick, ModeDouble} {
		r := benchRunner(mode)
		if allocs := testing.AllocsPerRun(1000, func() { r.clickCycle() }); allocs != 0 {
			t.Errorf("mode %d: %v allocs per cycle", mode, allocs)
		}
	}
}

// TestStopRightAfterPause covers the pause/stop race: a stop that lands before
// or after the run has handled the pause interrupt is never lost.
func TestStopRightAfterPause(t *testing.T) {
	scripts := map[string]func(c *fakeClock, paused *bool){
		"same instant": func(c *fakeClock, paused *bool) {
			c.at(55*ms, func(c *fakeClock) { *paused = true; c.Interrupt(); *paused = false; c.Interrupt() })
		},
		"next instant": func(c *fakeClock, paused *bool) {
			c.at(55*ms, func(c *fakeClock) { *paused = true; c.Interrupt() })
			c.at(55*ms, func(c *fakeClock) { *paused = false; c.Interrupt() })
		},
	}
	for name, script := range scripts {
		paused := false
		c := &fakeClock{}
		script(c, &paused)
		v := Run(clickCfg(10*ms, 0), &recInjector{clk: c}, c, nil, func() bool { return paused }, nil)
		if v.Code != CodeInterrupted || c.now != 55*ms {
			t.Errorf("%s: verdict %+v at %d", name, v, c.now)
		}
	}
}

func benchRunner(mode RunMode) *runner {
	c := &fakeClock{}
	cfg := Config{BaseIntervalNs: 2 * ms, JitterSigma: 0.35, DutyFraction: 0.3, Mode: mode, DoubleGapNs: ms / 2,
		Points: []Point{{X: 1, Y: 1, Clicks: 3, Radius: 4}, {X: 50, Y: 50, Clicks: 2}}, Seed: 9}
	g := guardFunc(func(int64, int64) Verdict { return Verdict{} })
	r := &runner{cfg: cfg, inj: &nopInjector{}, clk: c, g: g,
		paused: func() bool { return false }, onProgress: func(Progress) {}}
	if _, ok := r.setup(); !ok {
		panic("setup")
	}
	return r
}

func BenchmarkClickLoop(b *testing.B) {
	r := benchRunner(ModeClick)
	c := r.clk.(*fakeClock)
	b.ReportAllocs()
	for b.Loop() {
		c.waits = 0 // the runaway guard is for tests, not a long benchmark
		r.clickCycle()
	}
}
