package engine

import (
	"math"
	"math/rand/v2"
	"testing"
)

func seeded(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, seed^0xD1B54A32D192ED03)) }

func TestIntervalDeterministic(t *testing.T) {
	a := newInterval(40*ms, 0.35, seeded(7))
	b := newInterval(40*ms, 0.35, seeded(7))
	for i := range 1000 {
		if x, y := a.draw(), b.draw(); x != y {
			t.Fatalf("draw %d: %d != %d", i, x, y)
		}
	}
}

func TestIntervalMeanAndBounds(t *testing.T) {
	for _, sigma := range []float64{0.35, 1.0} {
		base := 40 * ms
		iv := newInterval(base, sigma, seeded(42))
		lo, hi := iv.bounds()
		if lo != 4*ms || hi != 76*ms {
			t.Fatalf("sigma %v: bounds [%d, %d]", sigma, lo, hi)
		}
		const n = 100_000
		var sum float64
		for range n {
			d := iv.draw()
			if d < lo || d > hi {
				t.Fatalf("sigma %v: draw %d outside [%d, %d]", sigma, d, lo, hi)
			}
			sum += float64(d)
		}
		mean := sum / n
		if dev := math.Abs(mean-float64(base)) / float64(base); dev > 0.01 {
			t.Errorf("sigma %v: mean %.0f ns is %.2f%% off base", sigma, mean, dev*100)
		}
	}
}

func TestIntervalFloor(t *testing.T) {
	// The 1 ms floor applies to the window: at base 5 ms, lo is 1 ms not 0.5.
	iv := newInterval(5*ms, 0.5, seeded(1))
	if lo, hi := iv.bounds(); lo != 1*ms || hi != 9*ms {
		t.Errorf("bounds [%d, %d]", lo, hi)
	}
	// At base 1 ms there is no room to jitter.
	iv = newInterval(1*ms, 0.5, seeded(1))
	for range 100 {
		if d := iv.draw(); d != ms {
			t.Fatalf("draw %d at the floor", d)
		}
	}
	// Off means exactly base, and a base under the floor is lifted to it.
	iv = newInterval(0, 0, seeded(1))
	if d := iv.draw(); d != ms {
		t.Errorf("draw %d", d)
	}
}
