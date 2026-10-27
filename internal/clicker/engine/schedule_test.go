package engine

import "testing"

// clickCfg is a plain left-click run at the given interval with instant taps.
func clickCfg(interval int64, limit int64) Config {
	return Config{BaseIntervalNs: interval, Mode: ModeClick, Button: ButtonLeft, ClickLimit: limit, Seed: 1}
}

func gaps(ts []int64) []int64 {
	out := make([]int64, 0, len(ts))
	for i := 1; i < len(ts); i++ {
		out = append(out, ts[i]-ts[i-1])
	}
	return out
}

func TestScheduleExactSpacing(t *testing.T) {
	v, in, _, _ := run(t, clickCfg(10*ms, 1000), nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	downs := in.times(OpMouseDown)
	if len(downs) != 1000 || downs[0] != 0 {
		t.Fatalf("%d downs, first at %d", len(downs), downs[0])
	}
	for i, g := range gaps(downs) {
		if g != 10*ms {
			t.Fatalf("gap %d = %d", i, g)
		}
	}
}

func TestScheduleAbsorbsSendTime(t *testing.T) {
	// Each Send takes 3 ms of virtual time; the deadline schedule must not drift.
	c := &fakeClock{}
	in := &recInjector{clk: c, sendNs: 3 * ms}
	Run(clickCfg(10*ms, 100), in, c, nil, nil, nil)
	downs := in.times(OpMouseDown)
	if last := downs[len(downs)-1]; last != 990*ms {
		t.Errorf("click 100 at %d, want %d", last, 990*ms)
	}
}

func TestScheduleStallRebases(t *testing.T) {
	v, in, _, _ := run(t, clickCfg(10*ms, 40), func(c *fakeClock) {
		c.at(205*ms, func(c *fakeClock) { c.now += 50 * ms })
	})
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	downs := in.times(OpMouseDown)
	for i, g := range gaps(downs) {
		if g < 10*ms {
			t.Errorf("gap %d = %d after a stall: a burst", i, g)
		}
	}
	// 0..200 then the stalled one at 255, then every 10 ms from there.
	if downs[21] != 255*ms || downs[22] != 265*ms {
		t.Errorf("after stall: %d, %d", downs[21], downs[22])
	}
}

func TestScheduleCatchUpCapped(t *testing.T) {
	var s schedule
	base := 10 * ms
	s.start(0, base)
	s.fire(0, base)
	// 19 ms late (under the 2-interval stall rule) and every draw at the
	// 1 ms minimum: each fire leaves the deadline behind, so only the cap
	// ends the catch-up.
	now := int64(29 * ms)
	behindRun := 0
	for range 10 {
		s.fire(now, ms)
		if s.deadline < now {
			behindRun++
			if s.due() != now+minGapNs {
				t.Fatalf("catch-up fire due %d after %d", s.due(), now)
			}
		} else {
			break
		}
		now = s.due()
	}
	if behindRun != maxCatchUp {
		t.Errorf("%d back-to-back catch-up fires, want the cap %d", behindRun, maxCatchUp)
	}
	if s.deadline < now {
		t.Errorf("schedule still behind after the cap")
	}
}

func TestScheduleCatchUpSmallLateness(t *testing.T) {
	// A 15 ms stall at 10 ms base: one catch-up fire 1 ms later, then on time.
	_, in, _, _ := run(t, clickCfg(10*ms, 30), func(c *fakeClock) {
		c.at(105*ms, func(c *fakeClock) { c.now += 15 * ms })
	})
	downs := in.times(OpMouseDown)
	if downs[11] != 120*ms || downs[12] != 121*ms || downs[13] != 130*ms {
		t.Errorf("catch-up: %v", downs[10:15])
	}
}

func TestPauseResumeNoBurst(t *testing.T) {
	paused := false
	c := &fakeClock{}
	in := &recInjector{clk: c}
	c.at(105*ms, func(c *fakeClock) { paused = true; c.Interrupt() })
	c.at(605*ms, func(c *fakeClock) { paused = false })
	v := Run(clickCfg(10*ms, 30), in, c, nil, func() bool { return paused }, nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	downs := in.times(OpMouseDown)
	for i, d := range downs {
		if d > 100*ms && d < 605*ms {
			t.Errorf("click %d at %d while paused", i, d)
		}
	}
	for i, g := range gaps(downs) {
		if g < 10*ms {
			t.Errorf("gap %d = %d: burst after resume", i, g)
		}
	}
	// Resume is noticed within one 10 ms slice.
	if downs[11] > 615*ms {
		t.Errorf("first click after resume at %d", downs[11])
	}
}

func TestHoldFitsDrawnInterval(t *testing.T) {
	cfg := clickCfg(20*ms, 2000)
	cfg.DutyFraction = 0.99
	cfg.JitterSigma = 0.35
	_, in, _, _ := run(t, cfg, nil)
	downs, ups := in.times(OpMouseDown), in.times(OpMouseUp)
	if len(downs) != 2000 || len(ups) != 2000 {
		t.Fatalf("%d downs %d ups", len(downs), len(ups))
	}
	for i := 0; i+1 < len(downs); i++ {
		// With zero latency the next press is at the drawn deadline.
		drawn := downs[i+1] - downs[i]
		if hold := ups[i] - downs[i]; hold > drawn-ms {
			t.Fatalf("click %d held %d of a %d interval", i, hold, drawn)
		}
		if ups[i] <= downs[i] {
			t.Fatalf("click %d: up before down", i)
		}
	}
}
