package engine

import "math"

// Rate units, spelled like clicker.RateUnit so the service can pass
// string(unit) straight through.
const (
	UnitSecond = "s"
	UnitMinute = "m"
	UnitHour   = "h"
	UnitDay    = "d"
)

// MinIntervalMs is the interval floor everywhere: no run clicks faster than
// once a millisecond.
const MinIntervalMs = 1.0

// rateUnits is the order IntervalToRate tries units in; on an equal error the
// shorter unit wins.
var rateUnits = [...]string{UnitSecond, UnitMinute, UnitHour, UnitDay}

// UnitMs is the length of a rate unit in milliseconds, or 0 for an unknown one.
func UnitMs(unit string) float64 {
	switch unit {
	case UnitSecond:
		return 1000
	case UnitMinute:
		return 60_000
	case UnitHour:
		return 3_600_000
	case UnitDay:
		return 86_400_000
	}
	return 0
}

// RateToIntervalMs turns "rate clicks per unit" into an interval in ms, floored
// at MinIntervalMs. A rate <= 0 or an unknown unit gives 0 so the caller can
// report ZERO_RATE instead of running at the floor by accident.
func RateToIntervalMs(rate float64, unit string) float64 {
	u := UnitMs(unit)
	if rate <= 0 || u == 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return 0
	}
	return math.Max(u/rate, MinIntervalMs)
}

// IntervalToRate expresses an interval as a whole number of clicks per unit:
// for each unit (s, m, h, d) the rate is rounded to an integer and clamped to
// 1..maxRate, and the unit whose rate reproduces the interval with the
// smallest error in ms wins (ties go to the shorter unit). 40 ms gives 25/s,
// 90 s gives 40/h, 1.5 days gives 1/d. maxRate <= 0 means no upper clamp.
func IntervalToRate(intervalMs, maxRate float64) (rate float64, unit string) {
	intervalMs = math.Max(intervalMs, MinIntervalMs)
	bestErr := math.Inf(1)
	for _, u := range rateUnits {
		um := UnitMs(u)
		r := math.Max(math.Round(um/intervalMs), 1)
		if maxRate > 0 && r > maxRate {
			r = math.Max(math.Floor(maxRate), 1)
		}
		if e := math.Abs(um/r - intervalMs); e < bestErr {
			bestErr, rate, unit = e, r, u
		}
	}
	return rate, unit
}

// DutyCap is the most of each interval, in percent, a press may be held at a
// given base rate in clicks per second. Fast rates need the button up for most
// of the cycle or the target app sees one long press:
//
//	cps >  500        1 %
//	200 <= cps <= 500 30 %
//	100 <= cps < 200  70 %
//	 50 <= cps < 100  98 %
//	cps <  50         100 % (uncapped)
//
// The service clamps Profile.DutyPercent with it before building
// Config.DutyFraction.
func DutyCap(cps float64) float64 {
	switch {
	case cps > 500:
		return 1
	case cps >= 200:
		return 30
	case cps >= 100:
		return 70
	case cps >= 50:
		return 98
	}
	return 100
}

// IntervalToCPS is the base click rate for an interval in ms (DutyCap's input).
func IntervalToCPS(intervalMs float64) float64 {
	return 1000 / math.Max(intervalMs, MinIntervalMs)
}
