package fx

// look: v2's look matrices with saturation and an intensity, all colour
// (LookFn), so it lives in the stack's LUT.
func init() {
	Register("look", func(p map[string]any, ctx Ctx) []Step {
		if fn := LookFn(p); fn != nil || ctx.Live {
			return []Step{{Color: orIdentity(fn)}}
		}
		return nil
	})
}
