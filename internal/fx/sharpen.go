package fx

// sharpen: ffmpeg cas (contrast-adaptive sharpening) on the RGB planes, as
// adjust's sharpen slider; types/sharpen.ts is the preview's CAS pass.

// casStep is cas at strength s (0..1) on RGB planes; none at 0.
func casStep(s float64) []Step {
	if s <= 0 {
		return nil
	}
	return []Step{{Filters: []string{"cas=strength=" + num(s) + ":planes=7"}, RGB: true}}
}

func init() {
	Register("sharpen", func(p map[string]any, _ Ctx) []Step { return casStep(pnum(p, "amount")) })
}
