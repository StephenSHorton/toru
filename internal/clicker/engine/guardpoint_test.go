package engine

import "testing"

// downsAt counts the presses (mouse or key downs) that went out while the
// cursor was at (x, y), and the time of the first one (-1 when none).
func downsAt(in *recInjector, x, y int32) (n int, first int64) {
	first = -1
	var cx, cy int32
	for _, r := range in.log {
		if r.move != 0 {
			cx, cy = r.x, r.y
			continue
		}
		if (r.op.Kind == OpMouseDown || r.op.Kind == OpKeyDown) && cx == x && cy == y {
			if n == 0 {
				first = r.t
			}
			n++
		}
	}
	return n, first
}

// cursorGuard answers from the injector's live cursor, like the real guard's
// zones and corners: v whenever the cursor sits on (x, y) and active(now).
func cursorGuard(in *recInjector, x, y int32, v Verdict, active func(now int64) bool) Guard {
	return guardFunc(func(now, _ int64) Verdict {
		if in.cx == x && in.cy == y && active(now) {
			return v
		}
		return Verdict{}
	})
}

func always(int64) bool { return true }

// guardPointCfgs are the cycle shapes that move to a point and then press.
func guardPointCfgs() map[string]Config {
	pts := []Point{{X: 10, Y: 10, Clicks: 2}, {X: 20, Y: 20, Clicks: 2}}
	click := clickCfg(50*ms, 0)
	click.DutyFraction = 0.3
	click.Points = pts
	hold := Config{BaseIntervalNs: 50 * ms, Mode: ModeMouseHoldInterval, HoldGapNs: 10 * ms, Seed: 1, Points: pts}
	key := Config{BaseIntervalNs: 50 * ms, Mode: ModeKeyHold, Keyboard: true, Scan: 0x1E, Seed: 1, Points: pts,
		KeyRepeat: KeyRepeat{DelayNs: 40 * ms, PeriodNs: 30 * ms}}
	glide := click
	glide.Glide = true
	return map[string]Config{"click": click, "hold": hold, "keyhold": key, "glide": glide}
}

// TestGuardStopAtNewPoint: a Stop zone over point 2 ends the run when the
// cursor arrives there, before anything is pressed at it.
func TestGuardStopAtNewPoint(t *testing.T) {
	for name, cfg := range guardPointCfgs() {
		if cfg.Mode == ModeKeyHold {
			// A key hold stays on point 1 until a pause; it never reaches 2.
			cfg.Points = []Point{{X: 10, Y: 10, Clicks: 1}, {X: 20, Y: 20, Clicks: 1}}
		}
		c := &fakeClock{}
		in := &recInjector{clk: c}
		g := cursorGuard(in, 20, 20, Verdict{Kind: Stop, Code: "zoneStop"}, always)
		v := Run(cfg, in, c, g, nil, nil)
		if v.Code != "zoneStop" {
			t.Errorf("%s: verdict %+v", name, v)
			continue
		}
		if n, _ := downsAt(in, 20, 20); n != 0 {
			t.Errorf("%s: %d presses inside the stop zone", name, n)
		}
		if n, _ := downsAt(in, 10, 10); n == 0 {
			t.Errorf("%s: nothing pressed at point 1", name)
		}
	}
}

// TestGuardPauseAtNewPoint: a Pause zone over point 2 pauses the run on
// arrival with nothing pressed there; on resume the run moves back onto the
// point and asks the guard again before pressing.
func TestGuardPauseAtNewPoint(t *testing.T) {
	const until = 400 * ms
	for name, cfg := range guardPointCfgs() {
		if cfg.Mode == ModeKeyHold {
			continue // covered by the stop test; a key hold never leaves point 1 unpaused
		}
		cfg.ClickLimit = 6
		c := &fakeClock{}
		in := &recInjector{clk: c}
		g := cursorGuard(in, 20, 20, Verdict{Kind: Pause, Code: "zonePause"},
			func(now int64) bool { return now < until })
		v := Run(cfg, in, c, g, nil, nil)
		if v.Code != CodeClickLimit {
			t.Errorf("%s: verdict %+v", name, v)
			continue
		}
		n, first := downsAt(in, 20, 20)
		if n == 0 || first < until {
			t.Errorf("%s: %d presses at point 2, first at %d (zone open until %d)", name, n, first, until)
		}
	}
}

// interruptingInjector interrupts the clock during the nth Send and makes that
// Send take long enough that the next deadline is already past.
type interruptingInjector struct {
	*recInjector
	n, sends int
	stall    int64
}

func (in *interruptingInjector) Send(ops []Op) (int, error) {
	k, err := in.recInjector.Send(ops)
	in.sends++
	if in.sends == in.n {
		in.clk.now += in.stall
		in.clk.Interrupt()
	}
	return k, err
}

// TestStopDuringSendSeenBeforeCatchUp: a stop that lands while a Send runs is
// seen before the next press, even when that press is already due.
func TestStopDuringSendSeenBeforeCatchUp(t *testing.T) {
	c := &fakeClock{}
	in := &interruptingInjector{recInjector: &recInjector{clk: c}, n: 3, stall: 35 * ms}
	v := Run(clickCfg(10*ms, 0), in, c, nil, nil, nil)
	if v.Code != CodeInterrupted {
		t.Fatalf("verdict %+v", v)
	}
	if n := len(in.times(OpMouseDown)); n != 3 {
		t.Errorf("%d presses, want 3 (none after the stop)", n)
	}
}

// TestPauseDuringSendSeenBeforeCatchUp: the same for a pause, which then
// resumes without a catch-up burst.
func TestPauseDuringSendSeenBeforeCatchUp(t *testing.T) {
	c := &fakeClock{}
	paused := false
	in := &interruptingInjector{recInjector: &recInjector{clk: c}, n: 3, stall: 35 * ms}
	c.at(100*ms, func(*fakeClock) { paused = false })
	cfg := clickCfg(10*ms, 6)
	inj := &pauseOnInterrupt{interruptingInjector: in, paused: &paused}
	v := Run(cfg, inj, c, nil, func() bool { return paused }, nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	ts := in.times(OpMouseDown)
	if len(ts) != 6 || ts[3] < 100*ms {
		t.Errorf("presses at %v: the 4th should wait for the resume at 100 ms", ts)
	}
}

// pauseOnInterrupt sets the pause flag before its interrupt fires, as the
// controller does for a user pause.
type pauseOnInterrupt struct {
	*interruptingInjector
	paused *bool
}

func (p *pauseOnInterrupt) Send(ops []Op) (int, error) {
	if p.sends+1 == p.n {
		*p.paused = true
	}
	return p.interruptingInjector.Send(ops)
}
