package fx

import (
	"math"
	"strconv"
)

// glitch: bursts in time slots of 1/rate s; a slot bursts when its first
// random is under density. During a burst: red and blue split by shift px
// (rgbashift), up to six horizontal slices of the frame moved sideways by
// up to sliceShift px (each a crop of the frame overlaid back at an
// offset), then noise. Pixel sizes are in pixels of a 1080-line frame. The
// slot randoms are expressions in t (slotRandExpr), so nothing depends on
// the clip's length; types/glitch.ts computes the same numbers.

// GlitchSlices is the number of slices drawn (0..6).
func GlitchSlices(p map[string]any) int {
	return int(math.Max(0, math.Min(6, math.Round(pnum(p, "slices")))))
}

// glitchPx is a 1080-line pixel size on an h-tall frame, whole pixels.
func glitchPx(v float64, h int) int { return int(math.Round(v * float64(h) / 1080)) }

func init() {
	Register("glitch", func(p map[string]any, ctx Ctx) []Step {
		density := pnum(p, "density")
		if density <= 0 || ctx.W <= 0 || ctx.H <= 0 {
			return nil
		}
		rate, seed := pnum(p, "rate"), pnum(p, "seed")
		r := func(k int) string { return slotRandExpr(rate, seed, k) }
		burst := quoted("lt(" + r(0) + "," + num(density) + ")")
		var out []Step
		if d := glitchPx(pnum(p, "shift"), ctx.H); d > 0 {
			out = append(out, Step{Filters: []string{"rgbashift=rh=" + strconv.Itoa(d) + ":bh=" + strconv.Itoa(-d) + ":edge=smear:enable=" + burst}, RGB: true})
		}
		n := GlitchSlices(p)
		bh := max(1, int(math.Round(pnum(p, "sliceSize")*float64(ctx.H))))
		off := float64(glitchPx(pnum(p, "sliceShift"), ctx.H))
		if n > 0 && off > 0 && bh < ctx.H {
			w := num(float64(ctx.W))
			out = append(out, Step{RGB: true, Graph: func(in, outL string, label func() string) []string {
				cur := label()
				splits := make([]string, n)
				head := in + "split=" + strconv.Itoa(n+1) + cur
				for k := range splits {
					splits[k] = label()
					head += splits[k]
				}
				stmts := []string{head}
				for k, src := range splits {
					y := quoted("floor(" + r(1+2*k) + "*" + num(float64(ctx.H-bh)) + ")")
					x := quoted("floor((" + r(2+2*k) + "*2-1)*" + num(off) + ")")
					band, next := label(), label()
					if k == n-1 {
						next = outL
					}
					stmts = append(stmts,
						src+"crop=w="+w+":h="+strconv.Itoa(bh)+":x=0:y="+y+band,
						cur+band+"overlay=x="+x+":y="+y+":format=gbrp:enable="+burst+next)
					cur = next
				}
				return stmts
			}})
		}
		if s := math.Round(pnum(p, "noise") * 40); s > 0 {
			v := num(s)
			out = append(out, Step{Filters: []string{"noise=c0s=" + v + ":c1s=" + v + ":c2s=" + v + ":allf=t+u:enable=" + burst}, RGB: true})
		}
		return out
	})
}
