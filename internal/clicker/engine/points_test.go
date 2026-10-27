package engine

import (
	"math"
	"testing"
)

func pointCfg(interval int64, limit int64, pts ...Point) Config {
	cfg := clickCfg(interval, limit)
	cfg.Points = pts
	return cfg
}

// clickSpots returns where the cursor was for each mouse-down.
func clickSpots(in *recInjector) [][2]int32 {
	var out [][2]int32
	var x, y int32
	for _, r := range in.log {
		if r.move != 0 {
			x, y = r.x, r.y
		} else if r.op.Kind == OpMouseDown {
			out = append(out, [2]int32{x, y})
		}
	}
	return out
}

func teleports(in *recInjector) int {
	n := 0
	for _, r := range in.log {
		if r.move == moveTeleport {
			n++
		}
	}
	return n
}

func TestPointsRoundRobin(t *testing.T) {
	a, b, c := Point{X: 10, Y: 10, Clicks: 1}, Point{X: 20, Y: 20, Clicks: 2}, Point{X: 30, Y: 30, Clicks: 1}
	v, in, _, prog := run(t, pointCfg(10*ms, 8, a, b, c), nil)
	if v.Code != CodeClickLimit {
		t.Fatalf("verdict %+v", v)
	}
	want := [][2]int32{{10, 10}, {20, 20}, {20, 20}, {30, 30}, {10, 10}, {20, 20}, {20, 20}, {30, 30}}
	got := clickSpots(in)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("spots %v", got)
		}
	}
	// The second click at b does not move the cursor (radius 0, same point).
	if n := teleports(in); n != 6 {
		t.Errorf("%d teleports, want 6", n)
	}
	// Progress: index/tick after each click (tick = clicks done at the point).
	wantP := [][2]int{{1, 0}, {1, 1}, {2, 0}, {0, 0}, {1, 0}, {1, 1}, {2, 0}, {0, 0}}
	for i, p := range prog {
		if [2]int{p.ActivePoint, p.PointTick} != wantP[i] || p.Clicks != int64(i+1) {
			t.Fatalf("progress %d = %+v", i, p)
		}
	}
}

func TestPointsStopAfterPass(t *testing.T) {
	cfg := pointCfg(10*ms, 0, Point{X: 1, Y: 1, Clicks: 2}, Point{X: 2, Y: 2, Clicks: 3})
	cfg.StopAfterPass = true
	v, _, _, prog := run(t, cfg, nil)
	if v.Kind != Stop || v.Code != CodePointsDone {
		t.Fatalf("verdict %+v", v)
	}
	last := prog[len(prog)-1]
	if last.Clicks != 5 || last.ActivePoint != 1 {
		t.Errorf("last progress %+v", last)
	}
}

func TestPointsSinglePointNoMove(t *testing.T) {
	_, in, _, _ := run(t, pointCfg(10*ms, 20, Point{X: 5, Y: 6, Clicks: 1}), nil)
	if n := teleports(in); n != 1 {
		t.Errorf("%d teleports for one exact point", n)
	}
}

func TestPointsRadiusMovesEveryCycle(t *testing.T) {
	_, in, _, _ := run(t, pointCfg(10*ms, 200, Point{X: 500, Y: 500, Clicks: 1, Radius: 7}), nil)
	if n := teleports(in); n != 200 {
		t.Errorf("%d teleports, want one per click", n)
	}
	for _, s := range clickSpots(in) {
		dx, dy := float64(s[0]-500), float64(s[1]-500)
		if math.Hypot(dx, dy) > 7 {
			t.Fatalf("spot %v outside radius", s)
		}
	}
}

func TestScatterWithinDisk(t *testing.T) {
	rnd := seeded(3)
	const n = 100_000
	var sx, sy float64
	var outer int
	for range n {
		dx, dy := scatter(rnd, 25)
		d := math.Hypot(float64(dx), float64(dy))
		if d > 25 {
			t.Fatalf("(%d, %d) outside 25", dx, dy)
		}
		if d > 25/math.Sqrt2 {
			outer++
		}
		sx += float64(dx)
		sy += float64(dy)
	}
	if math.Abs(sx/n) > 0.2 || math.Abs(sy/n) > 0.2 {
		t.Errorf("scatter biased: mean (%.2f, %.2f)", sx/n, sy/n)
	}
	// Uniform over the area: about half the samples sit beyond r/sqrt(2).
	if f := float64(outer) / n; f < 0.47 || f > 0.53 {
		t.Errorf("%.3f of samples in the outer half of the area", f)
	}
	if dx, dy := scatter(rnd, 0); dx != 0 || dy != 0 {
		t.Error("radius 0 scattered")
	}
}

func TestGlideToNewPoint(t *testing.T) {
	cfg := pointCfg(100*ms, 2, Point{X: 0, Y: 0, Clicks: 1}, Point{X: 300, Y: 400, Clicks: 1})
	cfg.Glide = true
	cfg.DutyFraction = 0.5
	_, in, _, _ := run(t, cfg, nil)
	var steps []rec
	var finalTp rec
	for _, r := range in.log {
		if r.move == moveGlide {
			steps = append(steps, r)
		}
		if r.move == moveTeleport {
			finalTp = r
		}
	}
	if len(steps) < 2 {
		t.Fatalf("%d glide steps", len(steps))
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].t < steps[i-1].t {
			t.Fatal("glide steps out of order")
		}
	}
	if finalTp.x != 300 || finalTp.y != 400 {
		t.Errorf("glide ended at (%d, %d)", finalTp.x, finalTp.y)
	}
	// The glide (40 ms of the 100 ms cycle) comes out of the interval: the
	// press lands after it and is released before the next deadline.
	downs, ups := in.times(OpMouseDown), in.times(OpMouseUp)
	if downs[1] != 140*ms || ups[1] > 199*ms {
		t.Errorf("second click %d..%d", downs[1], ups[1])
	}
}

func TestNoGlideWhenFast(t *testing.T) {
	cfg := pointCfg(10*ms, 4, Point{X: 0, Y: 0, Clicks: 1}, Point{X: 300, Y: 400, Clicks: 1})
	cfg.Glide = true
	_, in, _, _ := run(t, cfg, nil)
	for _, r := range in.log {
		if r.move == moveGlide {
			t.Fatal("glided at 10 ms")
		}
	}
}

func TestGlideInterruptible(t *testing.T) {
	cfg := pointCfg(400*ms, 0, Point{X: 0, Y: 0, Clicks: 1}, Point{X: 1000, Y: 0, Clicks: 1})
	cfg.Glide = true
	c := &fakeClock{}
	in := &recInjector{clk: c}
	// The glide to point 1 starts at 400 ms and takes 160 ms; stop half way.
	c.at(480*ms, func(c *fakeClock) { c.Interrupt() })
	v := Run(cfg, in, c, nil, nil, nil)
	if v.Code != CodeInterrupted {
		t.Fatalf("verdict %+v", v)
	}
	if c.now > 490*ms {
		t.Errorf("stopped at %d", c.now)
	}
	if n := len(in.times(OpMouseDown)); n != 1 {
		t.Errorf("%d clicks", n)
	}
}
