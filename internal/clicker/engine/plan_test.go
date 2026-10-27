package engine

import "testing"

func TestClickOrderAndHold(t *testing.T) {
	cfg := clickCfg(100*ms, 2)
	cfg.DutyFraction = 0.45
	cfg.Button = ButtonRight
	_, in, _, _ := run(t, cfg, nil)
	got := in.ops()
	want := []OpKind{OpMouseDown, OpMouseUp, OpMouseDown, OpMouseUp}
	if !eqKinds(kinds(got), want) {
		t.Fatalf("ops %v", kinds(got))
	}
	if got[1].t-got[0].t != 45*ms || got[2].t != 100*ms {
		t.Errorf("timing %d %d %d", got[0].t, got[1].t, got[2].t)
	}
	for _, r := range got {
		if r.op.Button != ButtonRight {
			t.Errorf("button %d", r.op.Button)
		}
	}
}

func TestDutyZeroIsOneSend(t *testing.T) {
	c := &fakeClock{}
	in := &countingInjector{}
	Run(clickCfg(10*ms, 5), in, c, nil, nil, nil)
	if in.calls != 5 || in.ops != 10 {
		t.Errorf("%d sends, %d ops", in.calls, in.ops)
	}
}

type countingInjector struct {
	nopInjector
	calls, ops int
}

func (c *countingInjector) Send(ops []Op) (int, error) {
	c.calls++
	c.ops += len(ops)
	return len(ops), nil
}

func TestDoubleClick(t *testing.T) {
	cfg := clickCfg(1000*ms, 3)
	cfg.Mode = ModeDouble
	cfg.DoubleGapNs = 60 * ms
	cfg.DutyFraction = 0.45
	v, in, _, prog := run(t, cfg, nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	// Cycle 1: tap, 60 ms gap, press held 450 ms. Cycle 2: one click left, so
	// a single click.
	got := in.ops()
	want := []OpKind{OpMouseDown, OpMouseUp, OpMouseDown, OpMouseUp, OpMouseDown, OpMouseUp}
	if !eqKinds(kinds(got), want) {
		t.Fatalf("ops %v", kinds(got))
	}
	ts := []int64{got[0].t, got[1].t, got[2].t, got[3].t, got[4].t}
	if ts[0] != 0 || ts[1] != 0 || ts[2] != 60*ms || ts[3] != 510*ms || ts[4] != 1000*ms {
		t.Errorf("times %v", ts)
	}
	if len(prog) != 3 || prog[1].Clicks != 2 || prog[2].Clicks != 3 {
		t.Errorf("progress %+v", prog)
	}
}

func TestDoubleClickGapCappedByInterval(t *testing.T) {
	cfg := clickCfg(40*ms, 4)
	cfg.Mode = ModeDouble
	cfg.DoubleGapNs = 450 * ms
	cfg.DutyFraction = 0.45
	_, in, _, _ := run(t, cfg, nil)
	downs, ups := in.times(OpMouseDown), in.times(OpMouseUp)
	// Gap capped to 39 ms, second hold to what is left before 39 ms: none.
	if downs[1] != 39*ms || ups[1] != 39*ms || downs[2] != 40*ms {
		t.Errorf("downs %v ups %v", downs, ups)
	}
}

func TestHoldInterval(t *testing.T) {
	cfg := Config{BaseIntervalNs: 500 * ms, Mode: ModeMouseHoldInterval, HoldGapNs: 100 * ms, ClickLimit: 3, DutyFraction: 0.1, Seed: 1}
	v, in, _, _ := run(t, cfg, nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	downs, ups := in.times(OpMouseDown), in.times(OpMouseUp)
	wantD := []int64{0, 600 * ms, 1200 * ms}
	wantU := []int64{500 * ms, 1100 * ms, 1700 * ms}
	for i := range wantD {
		if downs[i] != wantD[i] || ups[i] != wantU[i] {
			t.Fatalf("downs %v ups %v", downs, ups)
		}
	}
}

func keyCfg() Config {
	return Config{BaseIntervalNs: 100 * ms, Mode: ModeClick, Keyboard: true, Scan: 0x1E, ClickLimit: 1, Seed: 1}
}

func TestKeyShiftWrapping(t *testing.T) {
	cases := []struct {
		upper, lower, caps bool
		want               []OpKind
	}{
		{true, false, false, []OpKind{OpShiftDown, OpKeyDown, OpKeyUp, OpShiftUp}},
		{true, false, true, []OpKind{OpKeyDown, OpKeyUp}},
		{false, true, true, []OpKind{OpShiftDown, OpKeyDown, OpKeyUp, OpShiftUp}},
		{false, true, false, []OpKind{OpKeyDown, OpKeyUp}},
		{false, false, true, []OpKind{OpKeyDown, OpKeyUp}},
	}
	for _, c := range cases {
		cfg := keyCfg()
		cfg.WantUpper, cfg.WantLower = c.upper, c.lower
		caps := c.caps
		cfg.CapsLock = func() bool { return caps }
		_, in, _, _ := run(t, cfg, nil)
		got := in.ops()
		if !eqKinds(kinds(got), c.want) {
			t.Errorf("upper=%v lower=%v caps=%v: %v", c.upper, c.lower, c.caps, kinds(got))
		}
		for _, r := range got {
			if (r.op.Kind == OpKeyDown || r.op.Kind == OpKeyUp) && r.op.Scan != 0x1E {
				t.Errorf("scan %x", r.op.Scan)
			}
		}
	}
}

// shiftBalanced checks every Shift down is lifted before the next press and
// nothing lifts a Shift that is not down.
func shiftBalanced(t *testing.T, rs []rec) {
	t.Helper()
	down := false
	for i, r := range rs {
		switch r.op.Kind {
		case OpShiftDown:
			if down {
				t.Fatalf("op %d: Shift down twice", i)
			}
			down = true
		case OpShiftUp:
			if !down {
				t.Fatalf("op %d: Shift up without down", i)
			}
			down = false
		}
	}
	if down {
		t.Fatalf("Shift left down")
	}
}

func TestShiftPairedWhenCapsFlips(t *testing.T) {
	cfg := keyCfg()
	cfg.ClickLimit = 50
	cfg.DutyFraction = 0.5
	cfg.WantUpper = true
	caps := false
	cfg.CapsLock = func() bool { caps = !caps; return caps } // flips on every read
	_, in, _, _ := run(t, cfg, nil)
	got := in.ops()
	shiftBalanced(t, got)
	if n := len(in.times(OpShiftDown)); n == 0 || n == 50 {
		t.Errorf("%d shifted presses: the flip was not exercised", n)
	}
}

func TestKeyHoldRepeatsAndReleasesOnPause(t *testing.T) {
	cfg := Config{BaseIntervalNs: 100 * ms, Mode: ModeKeyHold, Keyboard: true, Scan: 0x1E, Extended: true,
		WantUpper: true, CapsLock: func() bool { return false },
		KeyRepeat: KeyRepeat{DelayNs: 500 * ms, PeriodNs: 50 * ms}, TimeLimitNs: 1500 * ms, Seed: 1}
	paused := false
	c := &fakeClock{}
	in := &recInjector{clk: c}
	c.at(620*ms, func(c *fakeClock) { paused = true; c.Interrupt() })
	c.at(900*ms, func(c *fakeClock) { paused = false })
	v := Run(cfg, in, c, nil, func() bool { return paused }, nil)
	if v.Code != CodeTimeLimit {
		t.Fatalf("verdict %+v", v)
	}
	got := in.ops()
	shiftBalanced(t, got)
	downs, ups := in.times(OpKeyDown), in.times(OpKeyUp)
	// 0, 500, 550, 600; released at the 620 pause; pressed again at 900
	// (noticed within a slice), then 1400, 1450; released at the 1500 stop.
	if len(ups) != 2 || ups[0] != 620*ms || ups[1] != 1500*ms {
		t.Fatalf("ups %v", ups)
	}
	want := []int64{0, 500 * ms, 550 * ms, 600 * ms, 900 * ms, 1400 * ms, 1450 * ms}
	if len(downs) != len(want) {
		t.Fatalf("downs %v", downs)
	}
	for i := range want {
		if downs[i] != want[i] {
			t.Fatalf("downs %v", downs)
		}
	}
	for _, r := range got {
		if (r.op.Kind == OpKeyDown || r.op.Kind == OpKeyUp) && !r.op.Extended {
			t.Fatalf("extended bit lost")
		}
	}
	if n := len(in.times(OpShiftDown)); n != 2 {
		t.Errorf("%d Shift downs, want one per press", n)
	}
}

func TestKeyHoldNeedsKey(t *testing.T) {
	v, _, _, _ := run(t, Config{BaseIntervalNs: ms, Mode: ModeKeyHold}, nil)
	if v.Kind != Stop || v.Code != CodeError {
		t.Errorf("verdict %+v", v)
	}
	v, _, _, _ = run(t, Config{BaseIntervalNs: ms, Keyboard: true}, nil)
	if v.Code != CodeError {
		t.Errorf("no scan: verdict %+v", v)
	}
}
