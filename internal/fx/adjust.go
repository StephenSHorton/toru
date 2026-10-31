package fx

// adjust: the colour sliders go into the stack's LUT (AdjustFn); sharpen is
// ffmpeg's cas on RGB planes after them (the preview's CAS pass).
func init() {
	Register("adjust", func(p map[string]any, ctx Ctx) []Step {
		var out []Step
		if fn := AdjustFn(p); fn != nil || ctx.Live {
			out = append(out, Step{Color: orIdentity(fn)})
		}
		return append(out, casStep(pnum(p, "sharpen"), ctx.Live)...)
	})
}
