package fx

// adjust: the colour sliders go into the stack's LUT (AdjustFn); sharpen is
// ffmpeg's cas on RGB planes after them (the preview's CAS pass).
func init() {
	Register("adjust", func(p map[string]any, _ Ctx) []Step {
		var out []Step
		if fn := AdjustFn(p); fn != nil {
			out = append(out, Step{Color: fn})
		}
		if s := pnum(p, "sharpen"); s > 0 {
			out = append(out, Step{Filters: []string{"cas=strength=" + num(s) + ":planes=7"}, RGB: true})
		}
		return out
	})
}
