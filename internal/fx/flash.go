package fx

import (
	"strconv"
	"strings"
)

// flash: the picture mixes towards a colour at start, then back over
// duration: k = intensity·(1 − (t − start)/duration)². The export is geq
// (each channel moved towards the colour by k, alpha kept), enabled only
// during the flash so its per-pixel cost stays inside it.

func init() {
	Register("flash", func(p map[string]any, _ Ctx) []Step {
		in := pnum(p, "intensity")
		if in <= 0 {
			return nil
		}
		s0, d := pnum(p, "start"), pnum(p, "duration")
		color, _ := p["color"].(string)
		rgb, _ := strconv.ParseUint(strings.TrimPrefix(color, "#"), 16, 32)
		k := num(in) + "*pow(1-(T-" + num(s0) + ")/" + num(d) + ",2)"
		ch := func(fn string, v uint64) string {
			return quoted("st(0," + k + ");" + fn + "(X,Y)+(" + num(float64(v)) + "-" + fn + "(X,Y))*ld(0)+0.5")
		}
		return []Step{{Filters: []string{"geq=r=" + ch("r", rgb>>16&255) + ":g=" + ch("g", rgb>>8&255) + ":b=" + ch("b", rgb&255) +
			":a=" + quoted("alpha(X,Y)") + ":enable=" + quoted("between(t,"+num(s0)+","+num(s0+d)+")")}, RGB: true}}
	})
}
