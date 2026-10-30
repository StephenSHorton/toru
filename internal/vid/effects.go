package vid

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// effects.go turns a v2 clip's effects (docs/video-editor-v2.md, Effects) into
// ffmpeg filter chains for the multi-track export. The preview draws the
// same effects with the shader in frontend/src/trim/compositor/effects.ts;
// the two are one owner's job so they match (effects_parity_test.go and
// frontend/scripts/effects-parity.ts check that they do).
//
// Only LGPL filters: Toru's own FFmpeg is BtbN's LGPL build, which has no
// eq or boxblur. So colour is done in RGB with colorchannelmixer (saturation
// and the look, as one 3x3 matrix) and lutrgb (contrast and brightness), and
// blur with avgblur.

// ClipJSON is one clip of a v2 project as the frontend sends it
// (frontend/src/trim/project.ts, toExportRequest): the TS Clip with every
// time in whole milliseconds. The project export (project.go) uses this type.
type ClipJSON struct {
	ID        string        `json:"id"`
	Media     string        `json:"media"`   // Media.id
	Start     int           `json:"start"`   // timeline position, ms
	In        int           `json:"in"`      // source range, ms
	Out       int           `json:"out"`     //
	Speed     float64       `json:"speed"`   // 0.25..4
	Volume    float64       `json:"volume"`  // 0..2
	Muted     bool          `json:"muted"`   //
	FadeIn    int           `json:"fadeIn"`  // ms
	FadeOut   int           `json:"fadeOut"` // ms
	Levels    []TrackLevel  `json:"levels,omitempty"`
	Transform TransformJSON `json:"transform"`
	Chroma    *ChromaJSON   `json:"chroma,omitempty"` // nil = off
	Filters   FiltersJSON   `json:"filters"`
}

// TransformJSON places a clip on the canvas: centre (0..1 of the canvas),
// scale (1 = fit inside the canvas) and opacity.
type TransformJSON struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Scale   float64 `json:"scale"`
	Opacity float64 `json:"opacity"`
}

// ChromaJSON is a clip's green screen: the key colour ("#rrggbb"), how close
// a colour must be to go transparent (0.01..1) and how soft the edge is
// (0..1). Same meaning as ffmpeg chromakey's similarity and blend.
type ChromaJSON struct {
	Color      string  `json:"color"`
	Similarity float64 `json:"similarity"`
	Blend      float64 `json:"blend"`
}

// DefaultChroma is a new green screen's settings, the same as the editor's
// DEFAULT_CHROMA (frontend/src/trim/project.ts). chromakey compares colour
// only, so white, grey and black all sit 0.377 from pure green; similarity
// + blend stays below that so they stay opaque.
var DefaultChroma = ChromaJSON{Color: "#00ff00", Similarity: 0.25, Blend: 0.08}

// FiltersJSON is a clip's colour and blur: brightness -1..1 (0), contrast
// 0..2 (1), saturation 0..3 (1), a look, blur 0..20 (0).
type FiltersJSON struct {
	Brightness float64 `json:"brightness"`
	Contrast   float64 `json:"contrast"`
	Saturation float64 `json:"saturation"`
	Look       string  `json:"look"` // "none" | "bw" | "sepia" | "vintage" | "cool" | "warm"
	Blur       float64 `json:"blur"`
}

// LookMatrices are the looks as RGB matrices (row-major: out.r = m[0]*r +
// m[1]*g + m[2]*b, ...). effects.ts LOOK_MATRICES has the same numbers.
var LookMatrices = map[string][9]float64{
	"bw":      {0.299, 0.587, 0.114, 0.299, 0.587, 0.114, 0.299, 0.587, 0.114},
	"sepia":   {0.393, 0.769, 0.189, 0.349, 0.686, 0.168, 0.272, 0.534, 0.131},
	"vintage": {0.62, 0.32, 0.06, 0.22, 0.68, 0.10, 0.18, 0.28, 0.54},
	"cool":    {0.90, 0.00, 0.10, 0.00, 0.97, 0.08, 0.00, 0.05, 1.10},
	"warm":    {1.10, 0.05, 0.00, 0.03, 1.00, 0.00, 0.00, 0.00, 0.85},
}

// Rec. 601 luma weights, for saturation (as effects.ts).
const lumaR, lumaG, lumaB = 0.299, 0.587, 0.114

var identity3 = [9]float64{1, 0, 0, 0, 1, 0, 0, 0, 1}

// ColorMatrix is a clip's saturation and look as one RGB matrix: the look
// applied after the saturation (look · sat).
func ColorMatrix(f FiltersJSON) [9]float64 {
	s := f.Saturation
	sat := [9]float64{
		(1-s)*lumaR + s, (1 - s) * lumaG, (1 - s) * lumaB,
		(1 - s) * lumaR, (1-s)*lumaG + s, (1 - s) * lumaB,
		(1 - s) * lumaR, (1 - s) * lumaG, (1-s)*lumaB + s,
	}
	look, ok := LookMatrices[f.Look]
	if !ok {
		return sat
	}
	var m [9]float64
	for r := 0; r < 3; r++ {
		for c := 0; c < 3; c++ {
			for k := 0; k < 3; k++ {
				m[r*3+c] += look[r*3+k] * sat[k*3+c]
			}
		}
	}
	return m
}

// BlurRadius is a clip's blur in pixels of its own frame: the blur value is
// the radius at 1080 lines, scaled to the frame's height (as effects.ts).
func BlurRadius(blur float64, srcH int) int {
	if blur <= 0 || srcH <= 0 {
		return 0
	}
	r := int(math.Round(blur * float64(srcH) / 1080))
	// avgblur needs the box inside the frame; the preview shader loops up
	// to maxBlurRadius (effects.ts MAX_BLUR_RADIUS).
	return min(r, max(0, srcH/2-1), maxBlurRadius)
}

const maxBlurRadius = 64

// ClipVideoFilter is the ffmpeg chain for a video or image clip's effects,
// in the order the preview draws them: green screen, colour (saturation and
// look, then contrast and brightness), blur. srcH is the clip's frame height
// (for the blur radius). The output keeps alpha (rgba or gbrap), so the
// caller adds its scale / opacity / overlay after it. "" when the clip has
// no effects.
func ClipVideoFilter(c ClipJSON, srcH int) string {
	var parts []string
	if c.Chroma != nil {
		parts = append(parts, fmt.Sprintf("chromakey=color=%s:similarity=%s:blend=%s",
			chromaColor(c.Chroma.Color), num(clampF(c.Chroma.Similarity, 0.01, 1)), num(clampF(c.Chroma.Blend, 0, 1))))
	}
	f := normFilters(c.Filters)
	rgba := false
	toRGBA := func() {
		if !rgba {
			parts = append(parts, "format=rgba")
			rgba = true
		}
	}
	if m := ColorMatrix(f); m != identity3 {
		toRGBA()
		parts = append(parts, fmt.Sprintf(
			"colorchannelmixer=rr=%s:rg=%s:rb=%s:gr=%s:gg=%s:gb=%s:br=%s:bg=%s:bb=%s",
			num(m[0]), num(m[1]), num(m[2]), num(m[3]), num(m[4]), num(m[5]), num(m[6]), num(m[7]), num(m[8])))
	}
	if f.Contrast != 1 || f.Brightness != 0 {
		// (v - mid) * contrast + mid + brightness, on 0..255, clipped. The
		// quotes keep the commas inside the expression.
		expr := fmt.Sprintf("'clip((val-127.5)*%s+127.5+%s,0,255)'", num(f.Contrast), num(f.Brightness*255))
		toRGBA()
		parts = append(parts, fmt.Sprintf("lutrgb=r=%s:g=%s:b=%s", expr, expr, expr))
	}
	if r := BlurRadius(f.Blur, srcH); r > 0 {
		// Planar RGB+alpha so all four planes get the same full-size box.
		parts = append(parts, "format=gbrap", fmt.Sprintf("avgblur=sizeX=%d:sizeY=%d", r, r))
	}
	return strings.Join(parts, ",")
}

// ClipAudioFilter is the ffmpeg chain for a clip's audio after it is cut to
// the clip's source range: speed (atempo, chained since one atempo takes
// 0.5..2), volume, fade in and fade out. segLen is the clip's length on the
// timeline in seconds (after speed), where the fade out ends. A muted clip
// is silenced. "" when there is nothing to do.
func ClipAudioFilter(c ClipJSON, segLen float64) string {
	var parts []string
	for _, f := range atempoFactors(c.Speed) {
		parts = append(parts, "atempo="+num(f))
	}
	switch v := clampF(c.Volume, 0, 2); {
	case c.Muted:
		parts = append(parts, "volume=0")
	case v != 1:
		parts = append(parts, "volume="+num(v))
	}
	fadeIn, fadeOut := float64(max(0, c.FadeIn))/1000, float64(max(0, c.FadeOut))/1000
	if segLen > 0 {
		fadeIn = math.Min(fadeIn, segLen)
		fadeOut = math.Min(fadeOut, segLen-fadeIn)
	}
	if fadeIn > 0 {
		parts = append(parts, "afade=t=in:st=0:d="+num(fadeIn))
	}
	if fadeOut > 0 {
		parts = append(parts, fmt.Sprintf("afade=t=out:st=%s:d=%s", num(math.Max(0, segLen-fadeOut)), num(fadeOut)))
	}
	return strings.Join(parts, ",")
}

// normFilters clamps each filter to its range. The zero FiltersJSON (a clip
// built without filters) means none: brightness 0, contrast 1, saturation 1.
func normFilters(f FiltersJSON) FiltersJSON {
	if f == (FiltersJSON{}) {
		return FiltersJSON{Contrast: 1, Saturation: 1, Look: "none"}
	}
	return FiltersJSON{
		Brightness: clampF(f.Brightness, -1, 1),
		Contrast:   clampF(f.Contrast, 0, 2),
		Saturation: clampF(f.Saturation, 0, 3),
		Look:       f.Look,
		Blur:       clampF(f.Blur, 0, 20),
	}
}

// atempoFactors splits a speed (clamped to 0.25..4) into atempo steps each
// within 0.5..2; none for 1x.
func atempoFactors(speed float64) []float64 {
	s := clampF(speed, 0.25, 4)
	if s == 0 || math.Abs(s-1) < 1e-9 {
		return nil
	}
	var out []float64
	for s > 2+1e-9 {
		out = append(out, 2)
		s /= 2
	}
	for s < 0.5-1e-9 {
		out = append(out, 0.5)
		s /= 0.5
	}
	if math.Abs(s-1) > 1e-9 {
		out = append(out, s)
	}
	return out
}

// chromaColor turns "#rrggbb" into ffmpeg's 0xRRGGBB (green if unreadable).
func chromaColor(hex string) string {
	h := strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	if _, err := strconv.ParseUint(h, 16, 32); err != nil || len(h) != 6 {
		return "0x00FF00"
	}
	return "0x" + strings.ToUpper(h)
}

// num formats a filter number: up to 6 decimals, no trailing zeros.
func num(f float64) string {
	s := strconv.FormatFloat(f, 'f', 6, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

func clampF(f, lo, hi float64) float64 {
	if math.IsNaN(f) {
		return lo
	}
	return math.Max(lo, math.Min(hi, f))
}
