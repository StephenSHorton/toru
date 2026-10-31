package fx

// lut: an imported .cube LUT (cube.go) with an intensity, all colour
// (CubeFn), so it is sampled into the stack's generated LUT like look and
// adjust. A LUT that isn't imported on this PC (a project from elsewhere,
// or a deleted file) is left out, as the preview draws it.
func init() {
	Register("lut", func(p map[string]any, ctx Ctx) []Step {
		fn := lutStepFn(p)
		if fn != nil || ctx.Live {
			return []Step{{Color: orIdentity(fn)}}
		}
		return nil
	})
}

func lutStepFn(p map[string]any) ColorFn {
	id, _ := p["lut"].(string)
	if id == "" {
		return nil
	}
	c, err := LoadLut(id)
	if err != nil {
		return nil
	}
	return CubeFn(c, pnum(p, "amount"))
}

// CubeFn is the lut effect's colour: clamp(mix(c, cube(c), amount)), the
// same rule as cube.ts cubeFn. nil when amount is 0.
func CubeFn(c *Cube, amount float64) ColorFn {
	if amount <= 0 {
		return nil
	}
	return func(in RGB) RGB {
		o := c.Sample(in[0], in[1], in[2])
		if amount >= 1 {
			return RGB(o)
		}
		var out RGB
		for k := 0; k < 3; k++ {
			out[k] = clamp01(in[k] + (o[k]-in[k])*amount)
		}
		return out
	}
}
