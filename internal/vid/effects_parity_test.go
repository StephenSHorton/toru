package vid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
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
}

const parityH = 180

func parityCases() []parityCase {
	green := &ChromaJSON{Color: "#00ff00", Similarity: 0.3, Blend: 0.1}
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

// TestWriteEffectsParityCases writes the parity cases to
// $TORU_FX_PARITY_DIR/cases.json for frontend/scripts/effects-parity.ts,
// which renders each with ffmpeg and with the preview shader and compares.
// Skipped unless the variable is set.
func TestWriteEffectsParityCases(t *testing.T) {
	dir := os.Getenv("TORU_FX_PARITY_DIR")
	if dir == "" {
		t.Skip("TORU_FX_PARITY_DIR not set (run frontend/scripts/effects-parity.ts)")
	}
	b, err := json.MarshalIndent(map[string]any{"height": parityH, "cases": parityCases()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
