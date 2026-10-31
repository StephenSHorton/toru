package vid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	effectfx "github.com/StephenSHorton/toru/internal/fx"
)

// ParityCase is one effects case for the preview/export parity check
// (frontend/scripts/effects-parity.ts): the clip's effects and the ffmpeg
// chain ClipVideoFilter builds for them on a ParityH-tall frame.
type parityCase struct {
	Name   string      `json:"name"`
	Chroma *ChromaJSON `json:"chroma,omitempty"`
	// Filters as the frontend's Filters (lower-case keys).
	Filters FiltersJSON `json:"filters"`
	Filter  string      `json:"filter"`
	// v3 cases: the effect stack (the frontend's EffectInstance shape) and
	// the filtergraph StackGraph builds for it, from [0:v] to [fxout].
	Effects []EffectJSON `json:"effects,omitempty"`
	Graph   string       `json:"graph,omitempty"`
	// T is the clip-local time the frame is drawn at (time-based effects);
	// Border is how many pixels at the frame's edge to skip, where the two
	// sides treat pixels past the edge differently.
	T      float64 `json:"t,omitempty"`
	Border int     `json:"border,omitempty"`
}

const parityH = 180

func parityCases() []parityCase {
	dc := DefaultChroma
	green := &dc
	none := FiltersJSON{Contrast: 1, Saturation: 1, Look: "none"}
	with := func(mod func(*FiltersJSON)) FiltersJSON {
		f := none
		mod(&f)
		return f
	}
	cases := []parityCase{
		{Name: "green screen", Chroma: green, Filters: none},
		{Name: "green screen, hard edge", Chroma: &ChromaJSON{Color: "#00ff00", Similarity: 0.2}, Filters: none},
		{Name: "brightness + contrast", Filters: with(func(f *FiltersJSON) { f.Brightness = 0.15; f.Contrast = 1.4 })},
		{Name: "saturation 2", Filters: with(func(f *FiltersJSON) { f.Saturation = 2 })},
		{Name: "blur 6", Filters: with(func(f *FiltersJSON) { f.Blur = 6 })},
	}
	for _, look := range []string{"bw", "sepia", "vintage", "cool", "warm"} {
		cases = append(cases, parityCase{Name: "green screen + " + look, Chroma: green, Filters: with(func(f *FiltersJSON) { f.Look = look })})
	}
	cases = append(cases, parityCase{Name: "everything", Chroma: green,
		Filters: FiltersJSON{Brightness: -0.1, Contrast: 1.2, Saturation: 0.6, Look: "warm", Blur: 3}})
	for i := range cases {
		cases[i].Filter = ClipVideoFilter(ClipJSON{Chroma: cases[i].Chroma, Filters: cases[i].Filters}, parityH)
	}
	return cases
}

// parityV3Cases are v3 effect stacks: the colour LUT (lut3d), sharpen (cas),
// box blur, green screen and a group below full intensity (split/blend).
func parityV3Cases(work string) ([]parityCase, error) {
	half := 0.5
	fx := func(typ string, params map[string]any) EffectJSON {
		return EffectJSON{ID: typ, Type: typ, Enabled: true, Params: params}
	}
	grouped := func(e EffectJSON, amount *float64) EffectJSON {
		e.Group, e.Amount = "g", amount
		return e
	}
	cases := []parityCase{
		{Name: "v3 adjust: exposure, warm, vibrance", Effects: []EffectJSON{fx("adjust", map[string]any{"exposure": 0.6, "temperature": 4200.0, "vibrance": 0.5})}},
		{Name: "v3 adjust: tone + levels + fade", Effects: []EffectJSON{fx("adjust", map[string]any{"shadows": 0.8, "highlights": -0.6, "whites": 0.5, "blacks": -0.4, "fade": 0.5, "tint": -0.4})}},
		{Name: "v3 look sepia, then adjust", Effects: []EffectJSON{fx("look", map[string]any{"look": "sepia", "saturation": 1.6, "amount": 0.7}), fx("adjust", map[string]any{"contrast": 1.3, "brightness": -0.05})}},
		{Name: "v3 green screen + cool look", Effects: []EffectJSON{fx("chromaKey", map[string]any{}), fx("look", map[string]any{"look": "cool"})}},
		{Name: "v3 sharpen", Effects: []EffectJSON{fx("adjust", map[string]any{"sharpen": 0.8})}},
		{Name: "v3 blur 6", Effects: []EffectJSON{fx("blur", map[string]any{"amount": 6.0})}},
		{Name: "v3 group at 50%", Effects: []EffectJSON{grouped(fx("look", map[string]any{"look": "bw"}), &half), grouped(fx("adjust", map[string]any{"contrast": 1.5}), &half)}},
		{Name: "v3 gaussian blur 4", Effects: []EffectJSON{fx("gaussianBlur", map[string]any{"sigma": 4.0})}, Border: 8},
		{Name: "v3 gaussian blur 12", Effects: []EffectJSON{fx("gaussianBlur", map[string]any{"sigma": 12.0})}, Border: 16},
		{Name: "v3 gaussian blur 0.6", Effects: []EffectJSON{fx("gaussianBlur", map[string]any{"sigma": 0.6})}, Border: 3},
		{Name: "v3 rgb split right", Effects: []EffectJSON{fx("rgbSplit", map[string]any{"amount": 24.0})}, Border: 6},
		{Name: "v3 rgb split 135deg", Effects: []EffectJSON{fx("rgbSplit", map[string]any{"amount": 30.0, "angle": 135.0})}, Border: 6},
		{Name: "v3 sharpen effect", Effects: []EffectJSON{fx("sharpen", map[string]any{"amount": 0.8})}, Border: 2},
		{Name: "v3 zoom in, mid-ease", Effects: []EffectJSON{fx("zoom", map[string]any{"scale": 1.6, "in": 0.5, "center": []any{0.3, 0.6}})}, T: 0.3, Border: 2},
		{Name: "v3 zoom hold, corner", Effects: []EffectJSON{fx("zoom", map[string]any{"scale": 2.37, "ease": "linear", "center": []any{0.9, 0.1}})}, T: 0.8, Border: 2},
		{Name: "v3 zoom out, default", Effects: []EffectJSON{fx("zoom", map[string]any{})}, T: 1.45, Border: 2},
		{Name: "v3 flash, default, t=0", Effects: []EffectJSON{fx("flash", map[string]any{})}},
		{Name: "v3 flash red, decaying", Effects: []EffectJSON{fx("flash", map[string]any{"color": "#ff2010", "intensity": 1.0, "start": 0.5, "duration": 0.8})}, T: 0.73},
		{Name: "v3 flash on a green screen", Effects: []EffectJSON{fx("chromaKey", map[string]any{}), fx("flash", map[string]any{})}, T: 0.1},
		{Name: "v3 shake, default", Effects: []EffectJSON{fx("shake", map[string]any{})}, T: 0.37, Border: 4},
		{Name: "v3 shake, big turn", Effects: []EffectJSON{fx("shake", map[string]any{"amplitude": 40.0, "rotation": 8.0, "zoom": 1.3, "seed": 77.0})}, T: 1.234, Border: 4},
		{Name: "v3 shake, rotation only", Effects: []EffectJSON{fx("shake", map[string]any{"amplitude": 0.0, "rotation": 6.0, "zoom": 1.0})}, T: 0.5, Border: 4},
		{Name: "v3 glitch burst (no noise)", Effects: []EffectJSON{fx("glitch", map[string]any{"noise": 0.0})}, T: 0.05, Border: 16},
		{Name: "v3 glitch quiet slot", Effects: []EffectJSON{fx("glitch", map[string]any{"noise": 0.0})}, T: 0.15},
		{Name: "v3 glitch 6 slices, seed 41", Effects: []EffectJSON{fx("glitch", map[string]any{"noise": 0.0, "seed": 41.0, "slices": 6.0, "sliceShift": 120.0, "sliceSize": 0.1, "shift": 20.0})}, T: 0.35, Border: 24},
		{Name: "v3 glitch in a 50% group", Effects: []EffectJSON{grouped(fx("glitch", map[string]any{"noise": 0.0}), &half)}, T: 0.85, Border: 16},
		{Name: "v3 zoom before start", Effects: []EffectJSON{fx("zoom", map[string]any{"start": 2.0})}, T: 1.0},
	}
	for i := range cases {
		stmts, err := effectfx.StackGraph(stackOf(ClipJSON{Effects: cases[i].Effects}), effectfx.Ctx{W: 320, H: parityH}, work, "p", "[0:v]", "[fxout]")
		if err != nil {
			return nil, err
		}
		cases[i].Graph = strings.Join(stmts, ";")
	}
	return cases, nil
}

// TestWriteEffectsParityCases writes the parity cases to
// $TORU_FX_PARITY_DIR/cases.json for frontend/scripts/effects-parity.ts,
// which renders each with ffmpeg and with the preview shader and compares.
// Skipped unless the variable is set.
func TestWriteEffectsParityCases(t *testing.T) {
	dir := os.Getenv("TORU_FX_PARITY_DIR")
	if dir == "" {
		t.Skip("TORU_FX_PARITY_DIR not set (run frontend/scripts/effects-parity.ts)")
	}
	v3, err := parityV3Cases(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.MarshalIndent(map[string]any{"height": parityH, "cases": append(parityCases(), v3...)}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
