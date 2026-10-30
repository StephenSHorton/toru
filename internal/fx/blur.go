package fx

import "math"

// blur: v2's box blur, avgblur on RGB planes. amount is the radius at 1080
// lines, scaled to the frame's height (types/blur.ts blurRadius).

// MaxBlurRadius is the largest radius the preview's shader loops over.
const MaxBlurRadius = 64

// BlurRadius is the radius in pixels of an h-tall frame.
func BlurRadius(amount float64, h int) int {
	if amount <= 0 || h <= 0 {
		return 0
	}
	r := int(math.Round(amount * float64(h) / 1080))
	return min(r, max(0, h/2-1), MaxBlurRadius)
}

func init() {
	Register("blur", func(p map[string]any, ctx Ctx) []Step {
		r := BlurRadius(pnum(p, "amount"), ctx.H)
		if r <= 0 {
			return nil
		}
		return []Step{{Filters: []string{"avgblur=sizeX=" + num(float64(r)) + ":sizeY=" + num(float64(r))}, RGB: true}}
	})
}
