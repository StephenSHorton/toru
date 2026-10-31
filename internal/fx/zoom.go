package fx

// zoom: a punch-in. Over in seconds from start the picture scales up to
// scale around centre, holds, then scales back over out. The export scales
// the frame per frame (scale eval=frame) to trunc(W·z) × trunc(H·z) and
// crops a W×H window from it (types/zoom.ts samples the same window). The
// crop's position is written in t too: crop's iw/ih keep the first frame's
// size when the frame size changes.

// zoomExpr is the zoom factor at t as an expression (zoomAt in
// types/zoom.ts), or "" when it's 1 throughout.
func zoomExpr(p map[string]any) string {
	scale := pnum(p, "scale")
	if scale <= 1 {
		return ""
	}
	ease, _ := p["ease"].(string)
	s0 := pnum(p, "start")
	in, hold, out := pnum(p, "in"), pnum(p, "hold"), pnum(p, "out")
	s1, s2 := s0+in, s0+in+hold
	s3 := s2 + out
	// u is 0..1 of the way to the full zoom.
	u := "0"
	if out > 0 {
		u = "if(lt(t," + num(s3) + ")," + easeExpr(ease, "clip(("+num(s3)+"-t)/"+num(out)+",0,1)") + ",0)"
	}
	u = "if(lt(t," + num(s2) + "),1," + u + ")"
	if in > 0 {
		u = "if(lt(t," + num(s1) + ")," + easeExpr(ease, "clip((t-"+num(s0)+")/"+num(in)+",0,1)") + "," + u + ")"
	}
	u = "if(lt(t," + num(s0) + "),0," + u + ")"
	return "(1+" + num(scale-1) + "*" + u + ")"
}

// zoomWindow is the scale and crop filters for a W×H frame zoomed by z (an
// expression in t) towards (cx, cy) in 0..1. Each crop expression keeps its
// scaled size in register 0.
func zoomWindow(z string, cx, cy float64, w, h int) []string {
	size := func(n int) string { return "trunc(" + num(float64(n)) + "*" + z + ")" }
	pos := func(c float64, n int) string {
		return quoted("st(0," + size(n) + ");floor(clip(" + num(c) + "*ld(0)-" + num(float64(n)/2) + ",0,ld(0)-" + num(float64(n)) + "))")
	}
	return []string{
		"scale=w=" + quoted(size(w)) + ":h=" + quoted(size(h)) + ":eval=frame:flags=bilinear",
		"crop=w=" + num(float64(w)) + ":h=" + num(float64(h)) + ":x=" + pos(cx, w) + ":y=" + pos(cy, h),
	}
}

func init() {
	Register("zoom", func(p map[string]any, ctx Ctx) []Step {
		z := zoomExpr(p)
		if z == "" || ctx.W <= 0 || ctx.H <= 0 {
			return nil
		}
		c, _ := p["center"].([2]float64)
		return []Step{{Filters: zoomWindow(z, c[0], c[1], ctx.W, ctx.H), RGB: true}}
	})
}
