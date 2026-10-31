package fx

import "math"

// shake: the picture zoomed in by zoom, moved by a seeded wobble of up to
// amplitude pixels (of a 1080-line frame) and turned by up to rotation
// degrees. The export scales once, crops a W×H window whose position
// follows the wobble in t (whole pixels, inside the zoomed frame), then
// rotates by an angle in t with a transparent fill (types/shake.ts draws
// the same two steps).

// shakeMotion is the three wobbles (x, y, angle) for a seed.
func shakeMotion(f, seed float64) [3]wobble {
	r := seeded(seed, 6)
	return [3]wobble{newWobble(f, r[0], r[1]), newWobble(f, r[2], r[3]), newWobble(f, r[4], r[5])}
}

func init() {
	Register("shake", func(p map[string]any, ctx Ctx) []Step {
		amp := pnum(p, "amplitude") * float64(ctx.H) / 1080
		rot := pnum(p, "rotation")
		if (amp <= 0 && rot <= 0) || ctx.W <= 0 || ctx.H <= 0 {
			return nil
		}
		m := shakeMotion(pnum(p, "frequency"), pnum(p, "seed"))
		z := pnum(p, "zoom")
		sw, sh := math.Trunc(float64(ctx.W)*z), math.Trunc(float64(ctx.H)*z)
		var f []string
		if sw > float64(ctx.W) || sh > float64(ctx.H) {
			pos := func(size float64, n int, w wobble) string {
				e := num((size - float64(n)) / 2)
				if amp > 0 {
					e += "+" + num(amp) + "*" + w.expr()
				}
				return quoted("floor(clip(" + e + ",0," + num(size-float64(n)) + "))")
			}
			f = append(f, "scale=w="+num(sw)+":h="+num(sh)+":flags=bilinear",
				"crop=w="+num(float64(ctx.W))+":h="+num(float64(ctx.H))+":x="+pos(sw, ctx.W, m[0])+":y="+pos(sh, ctx.H, m[1]))
		}
		if rot > 0 {
			f = append(f, "rotate=a="+quoted(num(rot*math.Pi/180)+"*"+m[2].expr())+":fillcolor=black@0")
		}
		if len(f) == 0 {
			return nil
		}
		return []Step{{Filters: f, RGB: true}}
	})
}
