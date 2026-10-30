package fx

import "strings"

// chromaKey: ffmpeg chromakey (the preview's shader follows its maths).
// It reads YUV, so it runs before anything converts the frame to RGB.
func init() {
	Register("chromaKey", func(p map[string]any, _ Ctx) []Step {
		color, _ := p["color"].(string)
		return []Step{{Filters: []string{"chromakey=color=0x" + strings.ToUpper(strings.TrimPrefix(color, "#")) +
			":similarity=" + num(pnum(p, "similarity")) + ":blend=" + num(pnum(p, "blend"))}}}
	})
}
