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
