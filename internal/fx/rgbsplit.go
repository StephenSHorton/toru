package fx

import "math"

// rgbSplit: ffmpeg rgbashift, red moved one way and blue the other by
// whole pixels, edges smeared (types/rgbSplit.ts reads the same texels).
// amount is in pixels of a 1080-line frame; angle 0 moves red right.

// RGBSplitShift is red's shift in whole pixels on an h-tall frame (blue
// moves the opposite way).
func RGBSplitShift(amount, angle float64, h int) (int, int) {
	d := amount * float64(h) / 1080
	a := angle * math.Pi / 180
	return int(math.Round(d * math.Cos(a))), int(math.Round(d * math.Sin(a)))
}

func init() {
	Register("rgbSplit", func(p map[string]any, ctx Ctx) []Step {
		dx, dy := RGBSplitShift(pnum(p, "amount"), pnum(p, "angle"), ctx.H)
		if dx == 0 && dy == 0 && !ctx.Live {
			return nil
		}
		return []Step{{Filters: []string{"rgbashift=rh=" + num(float64(dx)) + ":rv=" + num(float64(dy)) +
			":bh=" + num(float64(-dx)) + ":bv=" + num(float64(-dy)) + ":edge=smear"}, RGB: true}}
	})
}
