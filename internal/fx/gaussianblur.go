package fx

// gaussianBlur: ffmpeg gblur on RGB planes with 2 steps (its recursive
// filter, which types/gaussianBlur.ts samples as the kernel it equals).
// sigma is in pixels of a 1080-line frame, scaled to the frame's height.

// GblurSteps is the gblur steps both sides use.
const GblurSteps = 2

// GblurSigma is sigma in pixels of an h-tall frame.
func GblurSigma(sigma float64, h int) float64 {
	if sigma <= 0 || h <= 0 {
		return 0
	}
	return sigma * float64(h) / 1080
}

func init() {
	Register("gaussianBlur", func(p map[string]any, ctx Ctx) []Step {
		s := GblurSigma(pnum(p, "sigma"), ctx.H)
		if s < 0.05 {
			return nil
		}
		return []Step{{Filters: []string{"gblur=sigma=" + num(round4(s)) + ":steps=" + num(GblurSteps)}, RGB: true}}
	})
}
