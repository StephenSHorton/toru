package fx

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// lut.go is the export's twin of frontend/src/trim/fx/lut.ts: the colour
// effects as functions on RGB, and the 33³ LUT a run of them compiles into
// (the preview samples the same table with a sampler3D; the export hands it
// to lut3d). testdata/lut_golden.json holds cases both must reproduce. Keep
// the arithmetic in the same order as lut.ts.

// LutSize is the LUT's points per side.
const LutSize = 33

// RGB is a colour, 0..1 per channel.
type RGB [3]float64

// ColorFn maps a colour to its new value. Each effect clamps its output.
type ColorFn func(RGB) RGB

func clamp01(n float64) float64 {
	if n < 0 {
		return 0
	}
	if n > 1 {
		return 1
	}
	return n
}

func clampRGB(c RGB) RGB { return RGB{clamp01(c[0]), clamp01(c[1]), clamp01(c[2])} }

const lumaR, lumaG, lumaB = 0.299, 0.587, 0.114

func luma(c RGB) float64 { return lumaR*c[0] + lumaG*c[1] + lumaB*c[2] }

func smoothstep(a, b, x float64) float64 {
	t := clamp01((x - a) / (b - a))
	return t * t * (3 - 2*t)
}

// LookMatrices are v2's looks as RGB matrices, row-major (lut.ts
// LOOK_MATRICES).
var LookMatrices = map[string][9]float64{
	"bw":      {0.299, 0.587, 0.114, 0.299, 0.587, 0.114, 0.299, 0.587, 0.114},
	"sepia":   {0.393, 0.769, 0.189, 0.349, 0.686, 0.168, 0.272, 0.534, 0.131},
	"vintage": {0.62, 0.32, 0.06, 0.22, 0.68, 0.1, 0.18, 0.28, 0.54},
	"cool":    {0.9, 0.0, 0.1, 0.0, 0.97, 0.08, 0.0, 0.05, 1.1},
	"warm":    {1.1, 0.05, 0.0, 0.03, 1.0, 0.0, 0.0, 0.0, 0.85},
}

// SatMatrix is v2's saturation as an RGB matrix.
func SatMatrix(s float64) [9]float64 {
	l := [3]float64{lumaR, lumaG, lumaB}
	var m [9]float64
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			m[r*3+c] = (1-s)*l[c] + b2f(r == c)*s
		}
	}
	return m
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func mul(m [9]float64, c RGB) RGB {
	return RGB{
		m[0]*c[0] + m[1]*c[1] + m[2]*c[2],
		m[3]*c[0] + m[4]*c[1] + m[5]*c[2],
		m[6]*c[0] + m[7]*c[1] + m[8]*c[2],
	}
}

func kelvinRGB(k float64) RGB {
	t := k / 100
	var r, g, b float64
	if t <= 66 {
		r = 255
		g = 99.4708025861*math.Log(t) - 161.1195681661
	} else {
		r = 329.698727446 * math.Pow(t-60, -0.1332047592)
		g = 288.1221695283 * math.Pow(t-60, -0.0755148492)
	}
	switch {
	case t >= 66:
		b = 255
	case t <= 19:
		b = 0
	default:
		b = 138.5177312231*math.Log(t-10) - 305.0447927307
	}
	c := func(v float64) float64 { return math.Max(0, math.Min(255, v)) }
	return RGB{c(r), c(g), c(b)}
}

// WhiteBalance is the per-channel gain for a light temperature (6500 K
// neutral) and a tint (-1 green .. 1 magenta), keeping luma (lut.ts
// whiteBalance).
func WhiteBalance(kelvin, tint float64) RGB {
	n := kelvinRGB(6500)
	l := kelvinRGB(kelvin)
	g := RGB{math.Sqrt(l[0] / n[0]), math.Sqrt(l[1] / n[1]), math.Sqrt(l[2] / n[2])}
	m := tint * 0.2
	g[0] *= 1 + m/2
	g[1] *= 1 - m
	g[2] *= 1 + m/2
	y := luma(g)
	return RGB{g[0] / y, g[1] / y, g[2] / y}
}

func pnum(p map[string]any, id string) float64 { f, _ := p[id].(float64); return f }

// AdjustFn is the adjust effect's colour (lut.ts adjustFn); nil when it
// changes nothing.
func AdjustFn(p map[string]any) ColorFn {
	ev := pnum(p, "exposure")
	contrast := pnum(p, "contrast")
	brightness := pnum(p, "brightness")
	highlights := pnum(p, "highlights")
	shadows := pnum(p, "shadows")
	whites := pnum(p, "whites")
	blacks := pnum(p, "blacks")
	kelvin := pnum(p, "temperature")
	tint := pnum(p, "tint")
	sat := pnum(p, "saturation")
	vib := pnum(p, "vibrance")
	fade := pnum(p, "fade")
	var wb *RGB
	if kelvin != 6500 || tint != 0 {
		v := WhiteBalance(kelvin, tint)
		wb = &v
	}
	gain := 1.0
	if ev != 0 {
		gain = math.Pow(2, ev)
	}
	var satM *[9]float64
	if sat != 1 {
		m := SatMatrix(sat)
		satM = &m
	}
	levels := whites != 0 || blacks != 0
	lo := -blacks * 0.1
	hi := 1 - whites*0.1
	tone := highlights != 0 || shadows != 0
	cb := contrast != 1 || brightness != 0
	if wb == nil && gain == 1 && satM == nil && vib == 0 && !levels && !tone && !cb && fade == 0 {
		return nil
	}
	return func(o RGB) RGB {
		if wb != nil {
			o = RGB{o[0] * wb[0], o[1] * wb[1], o[2] * wb[2]}
		}
		if gain != 1 {
			e := func(v float64) float64 { return math.Pow(math.Pow(math.Max(v, 0), 2.2)*gain, 1/2.2) }
			o = RGB{e(o[0]), e(o[1]), e(o[2])}
		}
		if satM != nil {
			o = mul(*satM, o)
		}
		if vib != 0 {
			mx := math.Max(o[0], math.Max(o[1], o[2]))
			mn := math.Min(o[0], math.Min(o[1], o[2]))
			k := 1 + vib*(1-clamp01(mx-mn))
			y := luma(o)
			o = RGB{y + (o[0]-y)*k, y + (o[1]-y)*k, y + (o[2]-y)*k}
		}
		o = clampRGB(o)
		if levels {
			o = RGB{(o[0] - lo) / (hi - lo), (o[1] - lo) / (hi - lo), (o[2] - lo) / (hi - lo)}
		}
		if tone {
			y := luma(o)
			d := shadows*0.25*(1-smoothstep(0, 0.6, y)) + highlights*0.25*smoothstep(0.4, 1, y)
			o = RGB{o[0] + d, o[1] + d, o[2] + d}
		}
		if cb {
			o = RGB{(o[0]-0.5)*contrast + 0.5 + brightness, (o[1]-0.5)*contrast + 0.5 + brightness, (o[2]-0.5)*contrast + 0.5 + brightness}
		}
		if fade != 0 {
			k := 1 - fade*0.2
			lift := fade * 0.12
			o = RGB{lift + o[0]*k, lift + o[1]*k, lift + o[2]*k}
		}
		return clampRGB(o)
	}
}

// LookFn is the look effect (lut.ts lookFn): clamp(mix(c, look · sat · c,
// amount)); nil when it changes nothing.
func LookFn(p map[string]any) ColorFn {
	look, _ := p["look"].(string)
	sat := pnum(p, "saturation")
	amount := pnum(p, "amount")
	m, hasLook := LookMatrices[look]
	var satM *[9]float64
	if sat != 1 {
		s := SatMatrix(sat)
		satM = &s
	}
	if (!hasLook && satM == nil) || amount == 0 {
		return nil
	}
	return func(c RGB) RGB {
		o := c
		if satM != nil {
			o = mul(*satM, o)
		}
		if hasLook {
			o = mul(m, o)
		}
		if amount != 1 {
			o = RGB{c[0] + (o[0]-c[0])*amount, c[1] + (o[1]-c[1])*amount, c[2] + (o[2]-c[2])*amount}
		}
		return clampRGB(o)
	}
}

// GenerateLut runs every grid colour through fns in order: RGB triples,
// red changing fastest, then green, then blue (the .cube order).
func GenerateLut(fns []ColorFn, n int) []float64 {
	out := make([]float64, 0, n*n*n*3)
	for b := 0; b < n; b++ {
		for g := 0; g < n; g++ {
			for r := 0; r < n; r++ {
				c := RGB{float64(r) / float64(n-1), float64(g) / float64(n-1), float64(b) / float64(n-1)}
				for _, f := range fns {
					c = f(c)
				}
				out = append(out, c[0], c[1], c[2])
			}
		}
	}
	return out
}

// CubeText is a LUT as a .cube file (6 decimals), what lut3d reads.
func CubeText(lut []float64, n int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "LUT_3D_SIZE %d\n", n)
	for i := 0; i < len(lut); i += 3 {
		sb.WriteString(strconv.FormatFloat(lut[i], 'f', 6, 64))
		sb.WriteByte(' ')
		sb.WriteString(strconv.FormatFloat(lut[i+1], 'f', 6, 64))
		sb.WriteByte(' ')
		sb.WriteString(strconv.FormatFloat(lut[i+2], 'f', 6, 64))
		sb.WriteByte('\n')
	}
	return sb.String()
}
