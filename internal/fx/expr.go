package fx

import (
	"math"
	"strings"
)

// expr.go holds what the time-based builders share: numbers written into
// filter arguments, and ffmpeg expressions in t for the curves the preview
// computes in JS (types/<type>.ts). Both sides compute in doubles, so the
// values agree.

// round4 rounds to 4 decimals, so an argument reads the same as the
// preview's value (the preview rounds the same way).
func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }

// easeExpr is keyframes.go's ease(kind, x) as an expression of x (an
// expression already clamped to 0..1). inOut keeps x in register 1.
func easeExpr(kind, x string) string {
	switch kind {
	case "in":
		return "pow(" + x + ",3)"
	case "out":
		return "(1-pow(1-" + x + ",3))"
	case "inOut":
		return "(st(1," + x + ");if(lt(ld(1),0.5),4*pow(ld(1),3),1-pow(2-2*ld(1),3)/2))"
	case "hold":
		return "0"
	}
	return x
}

// KeysExpr is ValueAt(keys, t, base) for numbers as an expression of the
// time expression t (seconds; keys' T are ms, as the export receives
// them): a ladder of if()s over the segments, each eased as ValueAt eases
// it. Keys must be sorted; non-number values count as base.
func KeysExpr(keys []Key, base float64, t string) string {
	if len(keys) == 0 {
		return num(base)
	}
	v := func(k Key) float64 {
		if f, ok := k.V.(float64); ok {
			return f
		}
		return base
	}
	sec := func(k Key) string { return num(k.T / 1000) }
	e := num(v(keys[len(keys)-1]))
	for i := len(keys) - 2; i >= 0; i-- {
		a, b := keys[i], keys[i+1]
		seg := num(v(b))
		if span := b.T - a.T; span > 0 {
			x := "clip((" + t + "-" + sec(a) + ")/" + num(span/1000) + ",0,1)"
			seg = num(v(a)) + "+" + num(v(b)-v(a)) + "*" + easeExpr(a.Ease, x)
		}
		e = "if(lt(" + t + "," + sec(b) + ")," + seg + "," + e + ")"
	}
	return "if(lte(" + t + "," + sec(keys[0]) + ")," + num(v(keys[0])) + "," + e + ")"
}

// quoted wraps an expression for a filter option, so its commas and colons
// stay inside it.
func quoted(e string) string { return "'" + strings.ReplaceAll(e, "'", "") + "'" }

// minstdM is the Park–Miller "minimal standard" generator's modulus. Its
// products stay under 2^53, so JS numbers and Go float64s step it exactly
// alike.
const minstdM = 2147483647

// seeded is n numbers in 0..1 from seed (types/shake.ts seeded).
func seeded(seed float64, n int) []float64 {
	s := math.Mod(math.Abs(math.Trunc(seed)), minstdM-1) + 1
	out := make([]float64, n)
	for i := range out {
		s = math.Mod(s*48271, minstdM)
		out[i] = s / minstdM
	}
	return out
}

// wobble is a smooth, seeded -1..1 motion at f Hz: two sines, the second
// 1.7 times as fast, with phases from ph (types/shake.ts wobbleAt). terms
// gives its angular speeds and phases as written into expressions.
type wobble struct{ w1, p1, w2, p2 float64 }

func newWobble(f, ph1, ph2 float64) wobble {
	return wobble{2 * math.Pi * f, 2 * math.Pi * ph1, 2 * math.Pi * f * 1.7, 2 * math.Pi * ph2}
}

func (w wobble) expr() string {
	return "(0.6*sin(" + num(w.w1) + "*t+" + num(w.p1) + ")+0.4*sin(" + num(w.w2) + "*t+" + num(w.p2) + "))"
}

// slotRandExpr is the k-th 0..1 random of the current slot (floor(t·rate))
// as an expression (types/glitch.ts slotRand): two Park–Miller steps from
// a mix of slot, seed and k. Every product stays under 2^53, so ffmpeg's
// doubles and JS numbers agree exactly.
func slotRandExpr(rate, seed float64, k int) string {
	mix := "floor(t*" + num(rate) + ")*7919+" + num(math.Trunc(seed)*104729+float64(k)*15485863+1)
	m := num(minstdM)
	return "(mod(mod((" + mix + ")*48271," + m + ")*48271," + m + ")/" + m + ")"
}
