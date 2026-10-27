package engine

import (
	"math"
	"testing"
)

func TestRateToIntervalMs(t *testing.T) {
	cases := []struct {
		rate float64
		unit string
		want float64
	}{
		{25, UnitSecond, 40},
		{1, UnitSecond, 1000},
		{2, UnitMinute, 30_000},
		{1, UnitHour, 3_600_000},
		{40, UnitHour, 90_000},
		{1, UnitDay, 86_400_000},
		{2000, UnitSecond, 1}, // 1 ms floor
		{0, UnitSecond, 0},
		{-3, UnitSecond, 0},
		{5, "x", 0},
		{math.NaN(), UnitSecond, 0},
	}
	for _, c := range cases {
		if got := RateToIntervalMs(c.rate, c.unit); got != c.want {
			t.Errorf("RateToIntervalMs(%v, %q) = %v, want %v", c.rate, c.unit, got, c.want)
		}
	}
}

func TestIntervalToRate(t *testing.T) {
	cases := []struct {
		ms, max float64
		rate    float64
		unit    string
	}{
		{40, 500, 25, UnitSecond},
		{1000, 500, 1, UnitSecond},
		{60_000, 500, 1, UnitMinute},
		// 90 s is exactly 40 an hour; 1 a minute would be 30 s off.
		{90_000, 500, 40, UnitHour},
		{86_400_000, 500, 1, UnitDay},
		{1.5 * 86_400_000, 500, 1, UnitDay},
		// 33 ms: 1818/m would be closer, but the rate clamp (500) applies to
		// every unit, so 30/s wins.
		{33, 500, 30, UnitSecond},
		{33, 2000, 1818, UnitMinute},
		{1, 1000, 1000, UnitSecond},
		{0.2, 1000, 1000, UnitSecond}, // floored to 1 ms
		{1, 500, 500, UnitSecond},     // clamped
	}
	for _, c := range cases {
		r, u := IntervalToRate(c.ms, c.max)
		if r != c.rate || u != c.unit {
			t.Errorf("IntervalToRate(%v, %v) = %v/%s, want %v/%s", c.ms, c.max, r, u, c.rate, c.unit)
		}
	}
}

func TestRateRoundTrip(t *testing.T) {
	for _, u := range rateUnits {
		for _, r := range []float64{1, 2, 7, 25, 100, 500} {
			ms := RateToIntervalMs(r, u)
			if ms < MinIntervalMs {
				continue
			}
			gr, gu := IntervalToRate(ms, 0)
			if got := RateToIntervalMs(gr, gu); math.Abs(got-ms) > 1e-6*ms {
				t.Errorf("%v/%s -> %v ms -> %v/%s = %v ms", r, u, ms, gr, gu, got)
			}
		}
	}
}

func TestDutyCap(t *testing.T) {
	cases := []struct{ cps, want float64 }{
		{1, 100}, {49.99, 100},
		{50, 98}, {99.99, 98},
		{100, 70}, {199.99, 70},
		{200, 30}, {500, 30},
		{500.01, 1}, {1000, 1},
	}
	for _, c := range cases {
		if got := DutyCap(c.cps); got != c.want {
			t.Errorf("DutyCap(%v) = %v, want %v", c.cps, got, c.want)
		}
	}
	if got := IntervalToCPS(40); got != 25 {
		t.Errorf("IntervalToCPS(40) = %v", got)
	}
}
