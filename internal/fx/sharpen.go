package fx

// sharpen: ffmpeg cas (contrast-adaptive sharpening) on the RGB planes, as
// adjust's sharpen slider; types/sharpen.ts is the preview's CAS pass.

// casStep is cas at strength s (0..1) on RGB planes; none at 0. Live
// (keyframed), it's always there and switched off at 0 with enable, since
// cas sharpens a little even at strength 0.
func casStep(s float64, live bool) []Step {
	if s <= 0 && !live {
		return nil
	}
	f := "cas=strength=" + num(s) + ":planes=7"
	if live {
		f += ":enable=" + onOff(s > 0)
	}
	return []Step{{Filters: []string{f}, RGB: true}}
}

func init() {
	Register("sharpen", func(p map[string]any, ctx Ctx) []Step { return casStep(pnum(p, "amount"), ctx.Live) })
}
