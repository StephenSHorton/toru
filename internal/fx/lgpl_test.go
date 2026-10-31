package fx

import (
	_ "embed"
	"encoding/json"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// lgpl_test.go keeps the export LGPL-only (docs/video-editor-v3.md, LGPL-only
// export): every builder, at default, min and max params, may only emit
// filters the pinned FFmpeg build has (lgpl_filters.txt, its -filters list,
// which has no GPL filters such as eq, boxblur or perspective), and only
// the ones its definition lists in export.filters.

//go:embed lgpl_filters.txt
var lgplFilters string

// scaffold filters StackGraph adds around any effect.
var scaffold = []string{"format", "split", "blend", "null"}

func allowlist() map[string]bool {
	out := map[string]bool{}
	for _, l := range strings.Split(lgplFilters, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out[l] = true
		}
	}
	return out
}

// filtersIn is the filter names in a filtergraph (labels stripped; commas
// inside '…' quoted expressions don't split filters).
func filtersIn(graph string) []string {
	graph = regexp.MustCompile(`'[^']*'`).ReplaceAllString(graph, "q")
	graph = regexp.MustCompile(`\[[^\]]*\]`).ReplaceAllString(graph, ",")
	var out []string
	for _, part := range strings.FieldsFunc(graph, func(r rune) bool { return r == ',' || r == ';' }) {
		name, _, _ := strings.Cut(strings.TrimSpace(part), "=")
		name, _, _ = strings.Cut(name, "@") // name@instance
		if name != "" && !strings.Contains(name, "/") && !strings.Contains(name, ":") {
			out = append(out, name)
		}
	}
	return out
}

// paramSets is a def's params at default, at every number's min and at
// every number's max (other types at default).
func paramSets(d Def) []map[string]any {
	def := d.Resolve(nil)
	lo, hi := map[string]any{}, map[string]any{}
	for _, p := range d.Params {
		lo[p.ID], hi[p.ID] = def[p.ID], def[p.ID]
		if p.Type == "number" {
			lo[p.ID], hi[p.ID] = *p.Min, *p.Max
		}
		if p.Type == "enum" {
			hi[p.ID] = p.Options[len(p.Options)-1].ID
		}
	}
	return []map[string]any{def, lo, hi}
}

func TestEveryDefHasABuilder(t *testing.T) {
	for _, d := range Defs() {
		if builders[d.Type] == nil {
			t.Errorf("%s: no Go builder (internal/fx/%s.go)", d.Type, d.Type)
		}
	}
	for typ := range builders {
		if _, ok := Lookup(typ); !ok {
			t.Errorf("builder %q has no definition", typ)
		}
	}
}

func TestBuildersAreLGPL(t *testing.T) {
	allow := allowlist()
	for _, bad := range []string{"eq", "boxblur", "perspective", "frei0r"} {
		if allow[bad] {
			t.Fatalf("lgpl_filters.txt lists %s: is it the LGPL build's list?", bad)
		}
	}
	work := t.TempDir()
	for _, d := range Defs() {
		for i, p := range paramSets(d) {
			for _, amount := range []float64{1, 0.5} {
				stmts, err := StackGraph([]Effect{{Type: d.Type, Enabled: true, Params: p, Amount: amount}}, Ctx{W: 1920, H: 1080}, work, "t", "[in]", "[out]")
				if err != nil {
					t.Fatalf("%s set %d: %v", d.Type, i, err)
				}
				for _, f := range filtersIn(strings.Join(stmts, ";")) {
					if !allow[f] {
						t.Errorf("%s set %d emits %q, which the pinned LGPL ffmpeg doesn't have", d.Type, i, f)
					}
					if !slices.Contains(d.Export.Filters, f) && !slices.Contains(scaffold, f) {
						t.Errorf("%s set %d emits %q, not in its export.filters %v", d.Type, i, f, d.Export.Filters)
					}
				}
			}
		}
	}
}

func TestLutMatchesGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/lut_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Size  int `json:"size"`
		Cases []struct {
			Name  string `json:"name"`
			Stack []struct {
				Type   string         `json:"type"`
				Params map[string]any `json:"params"`
			} `json:"stack"`
			Samples [][4]float64 `json:"samples"`
			Sum     [3]float64   `json:"sum"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if g.Size != LutSize || len(g.Cases) == 0 {
		t.Fatalf("golden size %d with %d cases", g.Size, len(g.Cases))
	}
	n := float64(LutSize * LutSize * LutSize)
	for _, c := range g.Cases {
		var fns []ColorFn
		for _, e := range c.Stack {
			d, _ := Lookup(e.Type)
			for _, s := range builders[e.Type](d.Resolve(e.Params), Ctx{W: 1920, H: 1080}) {
				if s.Color == nil {
					t.Fatalf("%s: %s has a non-colour step", c.Name, e.Type)
				}
				fns = append(fns, s.Color)
			}
		}
		lut := GenerateLut(fns, LutSize)
		worst := 0.0
		for _, s := range c.Samples {
			i := int(s[0]) * 3
			for k := 0; k < 3; k++ {
				worst = math.Max(worst, math.Abs(lut[i+k]-s[k+1]))
			}
		}
		var sum [3]float64
		for i := 0; i < len(lut); i += 3 {
			for k := 0; k < 3; k++ {
				sum[k] += lut[i+k]
			}
		}
		for k := 0; k < 3; k++ {
			worst = math.Max(worst, math.Abs(sum[k]-c.Sum[k])/n)
		}
		if worst > 1e-9 {
			t.Errorf("%s: Go LUT differs from the preview's by %g", c.Name, worst)
		}
	}
}

func TestFiltersIn(t *testing.T) {
	got := filtersIn("[in]format=gbrap,split[a][b];[b]lut3d=file=C\\:/x.cube:interp=trilinear,cas=strength=1[c];[a][c]blend=all_opacity=0.5[d];[d]null[out]")
	want := []string{"format", "split", "lut3d", "cas", "blend", "null"}
	if !slices.Equal(got, want) {
		t.Errorf("filtersIn = %v, want %v", got, want)
	}
	got = filtersIn("[in]crop=w=320:h=180:x='floor(clip(t*2,0,10))':y=0,geq=r='r(X,Y)':enable='between(t,0.2,0.5)'[out]")
	if want := []string{"crop", "geq"}; !slices.Equal(got, want) {
		t.Errorf("filtersIn (quoted) = %v, want %v", got, want)
	}
}
