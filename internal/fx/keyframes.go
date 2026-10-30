package fx

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// keyframes.go is how a keyframed value moves over time, for the export: the
// same maths as frontend/src/trim/fx/keyframes.ts (testdata/
// keyframes_golden.json holds cases both must reproduce).
//
// Keys are sorted by T (any unit, as long as t uses the same one; the export
// passes ms). Before the first key the value is the first key's, after the
// last the last key's; between two keys it moves from one to the next along
// the first key's ease. Numbers and points move smoothly, colours
// ("#rrggbb") per RGB channel; booleans and other strings jump at the next
// key, like "hold".

// Key is one keyframe. V is a float64, bool, string or a point ([2]float64,
// or []any of two numbers as JSON decodes it).
type Key struct {
	T    float64
	V    any
	Ease string // "linear" (or ""), "in", "out", "inOut", "hold"
}

// Ease maps 0..1 progress through a segment to 0..1 of the change (cubic).
func Ease(kind string, x float64) float64 {
	u := math.Max(0, math.Min(1, x))
	switch kind {
	case "in":
		return u * u * u
	case "out":
		v := 1 - u
		return 1 - v*v*v
	case "inOut":
		if u < 0.5 {
			return 4 * u * u * u
		}
		v := -2*u + 2
		return 1 - v*v*v/2
	case "hold":
		return 0
	}
	return u
}

// ValueAt is the value at time t: base when there are no keys.
func ValueAt(keys []Key, t float64, base any) any {
	if len(keys) == 0 {
		return base
	}
	if !(t > keys[0].T) {
		return keys[0].V
	}
	last := keys[len(keys)-1]
	if t >= last.T {
		return last.V
	}
	i := 0
	for i < len(keys)-2 && t >= keys[i+1].T {
		i++
	}
	a, b := keys[i], keys[i+1]
	f := 1.0
	if span := b.T - a.T; span > 0 {
		f = Ease(a.Ease, (t-a.T)/span)
	}
	return Mix(a.V, b.V, f)
}

// Mix moves from a towards b by f (0..1).
func Mix(a, b any, f float64) any {
	if x, ok := a.(float64); ok {
		if y, ok := b.(float64); ok {
			return x + (y-x)*f
		}
	}
	if x, ok := point(a); ok {
		if y, ok := point(b); ok {
			return [2]float64{x[0] + (y[0]-x[0])*f, x[1] + (y[1]-x[1])*f}
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			if ca, ok := rgb(x); ok {
				if cb, ok := rgb(y); ok {
					var c [3]int
					for k := range c {
						// JS Math.round: half rounds up.
						c[k] = int(math.Floor(float64(ca[k]) + float64(cb[k]-ca[k])*f + 0.5))
					}
					return fmt.Sprintf("#%02x%02x%02x", clamp255(c[0]), clamp255(c[1]), clamp255(c[2]))
				}
			}
		}
	}
	if f >= 1 {
		return b
	}
	return a
}

func point(v any) ([2]float64, bool) {
	switch p := v.(type) {
	case [2]float64:
		return p, true
	case []any:
		if len(p) == 2 {
			x, ok1 := p[0].(float64)
			y, ok2 := p[1].(float64)
			return [2]float64{x, y}, ok1 && ok2
		}
	case []float64:
		if len(p) == 2 {
			return [2]float64{p[0], p[1]}, true
		}
	}
	return [2]float64{}, false
}

var hexColor = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func rgb(s string) ([3]int, bool) {
	if !hexColor.MatchString(s) {
		return [3]int{}, false
	}
	n, _ := strconv.ParseUint(s[1:], 16, 32)
	return [3]int{int(n >> 16 & 255), int(n >> 8 & 255), int(n & 255)}, true
}

func clamp255(v int) int {
	return max(0, min(255, v))
}
