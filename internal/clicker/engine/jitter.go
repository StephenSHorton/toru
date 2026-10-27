package engine

import (
	"math"
	"math/rand/v2"
)

// msNs is one millisecond in nanoseconds.
const msNs int64 = 1_000_000

// newRand seeds the run's random source. Seed 0 derives one from the clock so
// real runs differ while fake-clock tests stay deterministic.
func newRand(seed uint64, clk Clock) *rand.Rand {
	if seed == 0 {
		seed = uint64(clk.Now()) ^ 0x9E3779B97F4A7C15
	}
	return rand.New(rand.NewPCG(seed, seed^0xD1B54A32D192ED03))
}

// interval draws each cycle's length: the base interval, or with jitter on a
// Gaussian around it with sigma = Sigma x base.
//
// Draws outside the window are thrown away and drawn again rather than
// clamped, because clamping piles the tail onto the bounds and drags the mean.
// The window is [lo, hi] with lo = max(1 ms, 0.1 x base) and hi mirrored to
// 2 x base - lo (always inside 3 x base), so it is symmetric around base and
// the mean stays exactly on the base interval at every sigma.
type interval struct {
	base   int64
	sigma  float64 // in ns
	lo, hi float64
	rnd    *rand.Rand
}

// maxResample bounds the redraw loop; at sigma = 100 % about 70 % of draws
// land in the window, so 64 misses in a row does not happen in practice.
const maxResample = 64

func newInterval(baseNs int64, sigmaFrac float64, rnd *rand.Rand) interval {
	baseNs = max(baseNs, msNs)
	lo := math.Ceil(math.Max(float64(msNs), 0.1*float64(baseNs)))
	hi := 2*float64(baseNs) - lo // whole ns, mirrors lo around base, < 3 x base
	if sigmaFrac < 0 || math.IsNaN(sigmaFrac) || lo >= float64(baseNs) {
		sigmaFrac = 0 // at the 1 ms floor there is no room below base, so no jitter
	}
	return interval{base: baseNs, sigma: sigmaFrac * float64(baseNs), lo: lo, hi: hi, rnd: rnd}
}

// bounds is the window draws fall in, in ns.
func (iv *interval) bounds() (lo, hi int64) {
	if iv.sigma == 0 {
		return iv.base, iv.base
	}
	return int64(iv.lo), int64(iv.hi)
}

// draw returns the next cycle length in ns.
func (iv *interval) draw() int64 {
	if iv.sigma == 0 {
		return iv.base
	}
	b := float64(iv.base)
	for range maxResample {
		d := b + iv.rnd.NormFloat64()*iv.sigma
		if d >= iv.lo && d <= iv.hi {
			return int64(math.Round(d))
		}
	}
	return iv.base
}
