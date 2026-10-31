package vid

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"image"
	"image/draw"
	"image/png"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	effectfx "github.com/StephenSHorton/toru/internal/fx"
	"github.com/StephenSHorton/toru/internal/fx/fxtest"
)

// ParityCase is one effects case for the preview/export parity check
// (frontend/scripts/effects-parity.ts): the clip's effects and the ffmpeg
// chain the export builds for them on a ParityH-tall frame.
type parityCase struct {
	Name   string      `json:"name"`
	Chroma *ChromaJSON `json:"chroma,omitempty"`
	// Filters as the frontend's Filters (lower-case keys).
	Filters FiltersJSON `json:"filters"`
	Filter  string      `json:"filter"`
	// v3 cases: the effect stack (EffectJSON, as saved: keyframe times in
	// ms) and the filtergraph StackGraph builds for it, from [0:v] to
	// [fxout] (empty when the stack changes nothing).
	Effects []EffectJSON `json:"effects,omitempty"`
	Graph   string       `json:"graph,omitempty"`
	// T is the clip-local time in seconds the frame is drawn at: the preview
	// resolves keyframes and draws at T, the script gives the export's frame
	// that timestamp (keyframes themselves the Go side resolves, keyedCase).
	// Generated cases take it from the def's parity.t.
	T float64 `json:"t,omitempty"`
	// Limits come from the defs' parity blocks (the loosest in the stack);
	// Border is how many pixels at the frame's edge the comparison skips,
	// where neighbourhood effects treat the border differently (a hand-set
	// Border is a minimum; the defs' reach can raise it).
	Limits *effectfx.Parity `json:"limits,omitempty"`
	Border int              `json:"border"`
	// resolved is a keyed case's stack with its keys resolved at T, which
	// the export side builds.
	resolved []EffectJSON
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
		cases[i].Border = 2 + int(math.Ceil(cases[i].Filters.Blur*parityH/1080))
	}
	return cases
}

func fxOf(typ string, params map[string]any) EffectJSON {
	return EffectJSON{ID: typ, Type: typ, Enabled: true, Params: params}
}

// parityV3Cases are hand-picked v3 stacks that mix effects: the colour LUT
// (lut3d) over several effects, sharpen (cas), green screen under a look,
// and a group below full intensity (split/blend).
func parityV3Cases() []parityCase {
	half := 0.5
	grouped := func(e EffectJSON, amount *float64) EffectJSON {
		e.Group, e.Amount = "g", amount
		return e
	}
	return []parityCase{
		{Name: "v3 adjust: exposure, warm, vibrance", Effects: []EffectJSON{fxOf("adjust", map[string]any{"exposure": 0.6, "temperature": 4200.0, "vibrance": 0.5})}},
		{Name: "v3 adjust: tone + levels + fade", Effects: []EffectJSON{fxOf("adjust", map[string]any{"shadows": 0.8, "highlights": -0.6, "whites": 0.5, "blacks": -0.4, "fade": 0.5, "tint": -0.4})}},
		{Name: "v3 look sepia, then adjust", Effects: []EffectJSON{fxOf("look", map[string]any{"look": "sepia", "saturation": 1.6, "amount": 0.7}), fxOf("adjust", map[string]any{"contrast": 1.3, "brightness": -0.05})}},
		{Name: "v3 green screen + cool look", Effects: []EffectJSON{fxOf("chromaKey", map[string]any{}), fxOf("look", map[string]any{"look": "cool"})}},
		{Name: "v3 sharpen", Effects: []EffectJSON{fxOf("adjust", map[string]any{"sharpen": 0.8})}},
		{Name: "v3 group at 50%", Effects: []EffectJSON{grouped(fxOf("look", map[string]any{"look": "bw"}), &half), grouped(fxOf("adjust", map[string]any{"contrast": 1.5}), &half)}},
	}
}

// registryCases are generated from every registered definition, so a new
// effect is covered the moment its JSON lands: its defaults; each number at
// its min and at its max; each other enum option; a flipped boolean; a point
// at both corners; two seeded random parameter sets; a random set at half
// intensity; and random keyframes on every animatable param, resolved at a
// random time (keyedCase). The randomness is seeded by the type's name, so a
// def's cases don't change when another def is added.
func registryCases() []parityCase {
	var out []parityCase
	for _, d := range effectfx.Defs() {
		h := fnv.New64a()
		h.Write([]byte(d.Type))
		r := rand.New(rand.NewSource(int64(h.Sum64())))
		add := func(name string, params map[string]any) {
			out = append(out, parityCase{Name: d.Type + " " + name, Effects: []EffectJSON{fxOf(d.Type, params)}, T: d.Parity.T})
		}
		add("default", map[string]any{})
		def := d.Resolve(nil)
		for _, p := range d.Params {
			switch p.Type {
			case "number":
				for _, v := range []float64{*p.Min, *p.Max} {
					if v != def[p.ID] {
						add(fmt.Sprintf("%s=%s", p.ID, caseNum(v)), map[string]any{p.ID: v})
					}
				}
			case "enum":
				for _, o := range p.Options {
					if o.ID != def[p.ID] {
						add(p.ID+"="+o.ID, map[string]any{p.ID: o.ID})
					}
				}
			case "boolean":
				add(fmt.Sprintf("%s=%v", p.ID, !def[p.ID].(bool)), map[string]any{p.ID: !def[p.ID].(bool)})
			case "point":
				add(p.ID+"=0,0", map[string]any{p.ID: [2]float64{0, 0}})
				add(p.ID+"=1,1", map[string]any{p.ID: [2]float64{1, 1}})
			}
		}
		for i := range 2 {
			add(fmt.Sprintf("random %d", i), randomParams(d, r))
		}
		if d.Amount != "none" {
			half := 0.5
			e := fxOf(d.Type, randomParams(d, r))
			e.Amount = &half
			out = append(out, parityCase{Name: d.Type + " random at 50%", Effects: []EffectJSON{e}, T: d.Parity.T})
		}
		if c, ok := keyedCase(d, r); ok {
			out = append(out, c)
		}
	}
	return out
}

// randomParams is a value for every param, uniform over its range (numbers),
// options, both booleans, any colour, the unit square (points). Files keep
// their default.
func randomParams(d effectfx.Def, r *rand.Rand) map[string]any {
	out := map[string]any{}
	for _, p := range d.Params {
		if v, ok := randomValue(p, r); ok {
			out[p.ID] = v
		}
	}
	return out
}

func randomValue(p effectfx.ParamDef, r *rand.Rand) (any, bool) {
	switch p.Type {
	case "number":
		return *p.Min + (*p.Max-*p.Min)*r.Float64(), true
	case "enum":
		return p.Options[r.Intn(len(p.Options))].ID, true
	case "boolean":
		return r.Intn(2) == 1, true
	case "color":
		return fmt.Sprintf("#%06x", r.Intn(1<<24)), true
	case "point":
		return [2]float64{r.Float64(), r.Float64()}, true
	}
	return nil, false
}

// keyedCase keys every animatable param of d (three keys over 0..2 s with
// random values and eases) and picks a random time inside them. The export
// side resolves the keys at that time with effectfx.ValueAt (the Go twin of
// keyframes.ts) and builds the static graph; the preview resolves the same
// keys with resolveStack(effects, t). That holds the two valueAt twins and
// the effect to the same pixels. When the export animates params itself
// (sendcmd/expr, C3c), this case should render the frame at t from the
// animated graph instead.
func keyedCase(d effectfx.Def, r *rand.Rand) (parityCase, bool) {
	eases := []string{"linear", "in", "out", "inOut", "hold"}
	e := fxOf(d.Type, randomParams(d, r))
	e.Keys = map[string][]KeyframeJSON{}
	tMs := 100 + r.Float64()*1800
	at := map[string]any{}
	for k, v := range e.Params {
		at[k] = v
	}
	for _, p := range d.Params {
		if p.Animate == "" {
			continue
		}
		var keys []KeyframeJSON
		var fkeys []effectfx.Key
		for _, t := range []int{0, 1000, 2000} {
			v, ok := randomValue(p, r)
			if !ok {
				break
			}
			ease := eases[r.Intn(len(eases))]
			keys = append(keys, KeyframeJSON{T: t, V: v, Ease: ease})
			fkeys = append(fkeys, effectfx.Key{T: float64(t), V: v, Ease: ease})
		}
		if len(keys) == 0 {
			continue
		}
		e.Keys[p.ID] = keys
		if v, ok := p.Clamp(effectfx.ValueAt(fkeys, tMs, d.Resolve(e.Params)[p.ID])); ok {
			at[p.ID] = v
		}
	}
	if len(e.Keys) == 0 {
		return parityCase{}, false
	}
	c := parityCase{Name: fmt.Sprintf("%s keyed at %.3f s", d.Type, tMs/1000), Effects: []EffectJSON{e}, T: tMs / 1000}
	c.resolved = []EffectJSON{fxOf(d.Type, at)}
	return c, true
}

// finishV3 builds each v3 case's graph (from its keyframe-resolved stack,
// when it has one) and its limits and border from the defs.
func finishV3(cases []parityCase, work string) error {
	for i := range cases {
		c := &cases[i]
		stack := c.Effects
		if c.resolved != nil {
			stack = c.resolved
		}
		stmts, err := effectfx.StackGraph(stackOf(ClipJSON{Effects: stack}), effectfx.Ctx{W: 320, H: parityH}, work, "p", "[0:v]", "[fxout]")
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		c.Graph = strings.Join(stmts, ";")
		var border int
		c.Limits, border = limitsOf(stack)
		c.Border = max(c.Border, border)
	}
	return nil
}

// limitsOf is the loosest parity limit in a stack ("none" beats "stats"
// beats "pixels"; then the largest mean and p99) and the border to skip: 2
// pixels, plus the widest reach in the stack: a def's parity.border, else
// 20 px of a 1080-line frame for neighbourhood and geometry effects.
func limitsOf(stack []EffectJSON) (*effectfx.Parity, int) {
	rank := map[string]int{"pixels": 0, "stats": 1, "none": 2}
	var lim *effectfx.Parity
	border := 2
	for _, e := range stack {
		d, ok := effectfx.Lookup(e.Type)
		if !ok {
			continue
		}
		reach := d.Parity.Border
		if reach == 0 && d.Kind != "pixel" {
			reach = 20
		}
		border = max(border, 2+int(math.Ceil(reach*parityH/1080)))
		p := d.Parity
		switch {
		case lim == nil || rank[p.Mode] > rank[lim.Mode]:
			lim = &p
		case p.Mode == lim.Mode:
			lim.Mean, lim.P99 = math.Max(lim.Mean, p.Mean), math.Max(lim.P99, p.P99)
		}
	}
	return lim, border
}

func caseNum(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
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
	v3 := append(parityV3Cases(), registryCases()...)
	if err := finishV3(v3, dir); err != nil {
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

// TestRegistryCasesCoverEveryDef keeps the generator honest: every def gets
// cases, every case builds, and an animatable def gets a keyed case.
func TestRegistryCasesCoverEveryDef(t *testing.T) {
	cases := registryCases()
	if err := finishV3(cases, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, d := range effectfx.Defs() {
		n, keyed := 0, false
		for _, c := range cases {
			if c.Effects[0].Type == d.Type {
				n++
				keyed = keyed || c.Effects[0].Keys != nil
			}
		}
		animates := false
		for _, p := range d.Params {
			animates = animates || p.Animate != ""
		}
		if n < 4 || keyed != animates {
			t.Errorf("%s: %d cases, keyed %v (animatable %v)", d.Type, n, keyed, animates)
		}
	}
}

// migrationCases are 12 v2 clips covering each v2→v3 migration rule
// (docs/video-editor-v3.md, "Migration v2 → v3"): nothing set, the green
// screen alone, contrast/brightness alone, saturation with and without a
// look (it moves into the look), contrast/brightness after a look, blur
// alone, and mixes of everything. migration-golden.ts renders them through
// the v2 export chain into testdata/migration (the reference PNGs) and
// records the editor's migrated stacks next to them.
func migrationCases() []parityCase {
	dc := DefaultChroma
	f := func(b, c, s float64, look string, blur float64) FiltersJSON {
		return FiltersJSON{Brightness: b, Contrast: c, Saturation: s, Look: look, Blur: blur}
	}
	cases := []parityCase{
		{Name: "nothing set", Filters: f(0, 1, 1, "none", 0)},
		{Name: "green screen", Chroma: &dc, Filters: f(0, 1, 1, "none", 0)},
		{Name: "green screen, hard edge", Chroma: &ChromaJSON{Color: "#00ff00", Similarity: 0.2}, Filters: f(0, 1, 1, "none", 0)},
		{Name: "brightness + contrast", Filters: f(0.15, 1.4, 1, "none", 0)},
		{Name: "saturation alone", Filters: f(0, 1, 0.3, "none", 0)},
		{Name: "sepia", Filters: f(0, 1, 1, "sepia", 0)},
		{Name: "vintage + saturation", Filters: f(0, 1, 1.6, "vintage", 0)},
		{Name: "warm, then contrast + brightness", Filters: f(-0.1, 1.3, 1, "warm", 0)},
		{Name: "blur alone", Filters: f(0, 1, 1, "none", 4)},
		{Name: "green screen + cool + blur", Chroma: &dc, Filters: f(0, 1, 1, "cool", 2)},
		{Name: "bw, no saturation, low contrast", Filters: f(0, 0.7, 0, "bw", 0)},
		{Name: "everything", Chroma: &dc, Filters: f(-0.1, 1.2, 0.6, "warm", 3)},
	}
	for i := range cases {
		cases[i].Filter = ClipVideoFilter(ClipJSON{Chroma: cases[i].Chroma, Filters: cases[i].Filters}, parityH)
		cases[i].Border = 2 + int(math.Ceil(cases[i].Filters.Blur*parityH/1080))
	}
	return cases
}

// TestWriteMigrationCases writes migrationCases to
// $TORU_MIGRATION_DIR/cases.json for migration-golden.ts --write. Skipped
// unless the variable is set.
func TestWriteMigrationCases(t *testing.T) {
	dir := os.Getenv("TORU_MIGRATION_DIR")
	if dir == "" {
		t.Skip("TORU_MIGRATION_DIR not set (run frontend/scripts/migration-golden.ts --write)")
	}
	b, err := json.MarshalIndent(map[string]any{"height": parityH, "cases": migrationCases()}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// migrationGolden is testdata/migration/golden.json: per case the v2 clip,
// the stack the editor migrates it to, and the reference PNG the v2 export
// chain drew from src.png.
type migrationGolden struct {
	Height int `json:"height"`
	Cases  []struct {
		Name    string       `json:"name"`
		Effects []EffectJSON `json:"effects"`
		PNG     string       `json:"png"`
		Border  int          `json:"border"`
	} `json:"cases"`
}

// TestMigrationGoldenExport renders each migrated stack through the v3
// export (StackGraph) with the pinned ffmpeg and holds it to the v2
// reference PNG, with v2's limits (mean ≤ 1, p99 ≤ 6). migration-golden.ts
// does the same for the preview. Skipped without the pinned ffmpeg.
func TestMigrationGoldenExport(t *testing.T) {
	ff := fxtest.PinnedFFmpeg(t)
	b, err := os.ReadFile("testdata/migration/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g migrationGolden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cases) != len(migrationCases()) {
		t.Fatalf("golden.json has %d cases, migrationCases %d: run migration-golden.ts --write", len(g.Cases), len(migrationCases()))
	}
	work := t.TempDir()
	src, err := filepath.Abs("testdata/migration/src.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range g.Cases {
		want := readPNG(t, filepath.Join("testdata/migration", c.PNG))
		w, h := want.Bounds().Dx(), want.Bounds().Dy()
		stmts, err := effectfx.StackGraph(stackOf(ClipJSON{Effects: c.Effects}), effectfx.Ctx{W: w, H: h}, work, "m", "[0:v]", "[fxout]")
		if err != nil {
			t.Fatalf("%s: %v", c.Name, err)
		}
		graph := "[0:v]null[fxout]"
		if stmts != nil {
			graph = strings.Join(stmts, ";")
		}
		out, err := exec.Command(ff, "-v", "error", "-i", src, "-filter_complex", graph+";[fxout]format=rgba[o]", "-map", "[o]",
			"-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-").Output()
		if err != nil {
			t.Fatalf("%s: %v\ngraph: %s", c.Name, err, graph)
		}
		mean, p99 := frameDiff(want.Pix, out, w, h, c.Border)
		if mean > 1 || p99 > 6 {
			t.Errorf("%s: export of the migrated stack differs from v2: mean %.2f, p99 %.1f (limits 1, 6)", c.Name, mean, p99)
		}
	}
}

func readPNG(t *testing.T, path string) *image.NRGBA {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, img.Bounds().Min, draw.Src)
	return out
}

// frameDiff is the parity script's pixel comparison (fx-edge.ts compare):
// colour premultiplied by alpha, plus alpha, in 0..255 levels, skipping
// border pixels at the edge; it returns the mean and p99 of the differences.
func frameDiff(a, b []byte, w, h, border int) (mean, p99 float64) {
	var diffs []float64
	at := func(buf []byte, o, k int) float64 {
		if k == 3 {
			return float64(buf[o+3])
		}
		return float64(buf[o+k]) * float64(buf[o+3]) / 255
	}
	for y := border; y < h-border; y++ {
		for x := border; x < w-border; x++ {
			o := (y*w + x) * 4
			for k := range 4 {
				diffs = append(diffs, math.Abs(at(a, o, k)-at(b, o, k)))
			}
		}
	}
	sort.Float64s(diffs)
	sum := 0.0
	for _, d := range diffs {
		sum += d
	}
	return sum / float64(len(diffs)), diffs[len(diffs)*99/100]
}
