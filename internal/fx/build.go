package fx

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// build.go turns a clip's effect stack into an ffmpeg filtergraph for the
// export, the way frontend/src/trim/fx/renderer.ts draws it in the preview:
// each type's builder (registered by internal/fx/<type>.go) returns steps;
// consecutive colour steps compile into ONE LUT file read by lut3d (with
// trilinear interpolation, as the preview's sampler3D samples it), and
// filter steps go in as they are. A group below full intensity runs on a
// split copy of its input and is blended back (blend=all_opacity).

// Step is one piece of an effect: a colour function (Color non-nil, fused
// into the run's LUT) or ffmpeg filters (Filters, each "name=args", in
// order, wanting gbrap input when RGB is set, else any format).
type Step struct {
	Color   ColorFn
	Filters []string
	RGB     bool
}

// Ctx is what a builder may depend on besides params: the frame size the
// effects run at (the clip's source size, as the preview).
type Ctx struct{ W, H int }

// Builder turns resolved params (Def.Resolve) into steps; none when the
// effect changes nothing.
type Builder func(p map[string]any, ctx Ctx) []Step

var builders = map[string]Builder{}

// Register adds a type's builder (from its file's init).
func Register(typ string, b Builder) { builders[typ] = b }

// Effect is one stack entry as the export reads it.
type Effect struct {
	Type    string
	Enabled bool
	Params  map[string]any
	Group   string
	Amount  float64 // 0..1; 1 = full
}

// StackGraph is the filtergraph statements that take label in through the
// clip's effects to label out ("[x]" style labels), with LUT files written
// to work. id makes the graph's internal labels unique. It returns nil
// when the stack changes nothing (the caller connects in to out itself).
func StackGraph(effects []Effect, ctx Ctx, work, id, in, out string) ([]string, error) {
	type tagged struct {
		step  Step
		group string
		amt   float64
	}
	var steps []tagged
	for i, e := range effects {
		d, ok := Lookup(e.Type)
		b := builders[e.Type]
		if !ok || b == nil || !e.Enabled || e.Amount <= 0 {
			continue
		}
		g := ""
		if e.Amount < 1 {
			g = e.Group
			if g == "" {
				g = "fx" + strconv.Itoa(i)
			}
		}
		for _, s := range b(d.Resolve(e.Params), ctx) {
			steps = append(steps, tagged{s, g, math.Min(1, e.Amount)})
		}
	}
	if len(steps) == 0 {
		return nil, nil
	}
	var stmts []string
	n := 0
	label := func() string { n++; return fmt.Sprintf("[%s_%d]", id, n) }
	cur := in
	for i := 0; i < len(steps); {
		j := i + 1
		for j < len(steps) && steps[j].group == steps[i].group {
			j++
		}
		span := make([]Step, 0, j-i)
		for _, t := range steps[i:j] {
			span = append(span, t.step)
		}
		chain, err := chainOf(span, work)
		if err != nil {
			return nil, err
		}
		next := label()
		if steps[i].group == "" {
			stmts = append(stmts, cur+chain+next)
		} else {
			// The group's input, kept as straight-alpha gbrap for the blend.
			a, b, done := label(), label(), label()
			stmts = append(stmts,
				cur+"format=gbrap,split"+a+b,
				b+chain+",format=gbrap"+done,
				a+done+"blend=all_mode=normal:all_opacity="+num(steps[i].amt)+next)
		}
		cur = next
		i = j
	}
	// The caller's out label: one pass-through to name it.
	stmts = append(stmts, cur+"null"+out)
	return stmts, nil
}

// chainOf is one linear chain for steps: colour runs as lut3d, filters as
// they are, format=gbrap once before the first step that needs RGB.
func chainOf(steps []Step, work string) (string, error) {
	var parts []string
	rgb := false
	toRGB := func() {
		if !rgb {
			parts = append(parts, "format=gbrap")
			rgb = true
		}
	}
	for i := 0; i < len(steps); {
		if steps[i].Color != nil {
			j := i
			var fns []ColorFn
			for j < len(steps) && steps[j].Color != nil {
				fns = append(fns, steps[j].Color)
				j++
			}
			path, err := writeLut(fns, work)
			if err != nil {
				return "", err
			}
			toRGB()
			parts = append(parts, "lut3d=file="+filterValue(filepath.ToSlash(path))+":interp=trilinear")
			i = j
			continue
		}
		if steps[i].RGB {
			toRGB()
		}
		parts = append(parts, steps[i].Filters...)
		i++
	}
	return strings.Join(parts, ","), nil
}

// writeLut writes a run's LUT as a .cube in work, named by its content (so
// a stack used by many clips writes it once).
func writeLut(fns []ColorFn, work string) (string, error) {
	text := CubeText(GenerateLut(fns, LutSize), LutSize)
	sum := sha1.Sum([]byte(text))
	path := filepath.Join(work, "lut-"+hex.EncodeToString(sum[:8])+".cube")
	if _, err := os.Stat(path); err == nil {
		return path, nil
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		return "", fmt.Errorf("effects: write LUT: %w", err)
	}
	return path, nil
}

// num formats a filter argument: shortest form, no exponent.
func num(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if s == "-0" {
		return "0"
	}
	return s
}

// filterValue escapes s for use as one option value inside a filtergraph
// (as internal/vid's filterValue).
func filterValue(s string) string {
	return escapeChars(escapeChars(s, `\:'`), `\'[],;`)
}

func escapeChars(s, special string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
