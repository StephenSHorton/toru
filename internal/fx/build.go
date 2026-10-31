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
// into the run's LUT), ffmpeg filters (Filters, each "name=args", in
// order), or a small graph of its own (Graph: statements from label in to
// label out, naming any labels it needs in between with label). Filters
// and Graph want gbrap input when RGB is set, else any format.
type Step struct {
	Color   ColorFn
	Filters []string
	Graph   func(in, out string, label func() string) []string
	RGB     bool

	// A keyframed colour step's function and params key per frame
	// (keyed.go); nil for a static one.
	ColorFrames []ColorFn
	ColorKeys   []string
}

// Ctx is what a builder may depend on besides params: the frame size the
// effects run at (the clip's source size, as the preview). FPS and Frames
// are the clip's output rate and length in frames, for keyframes (FPS 0:
// keyframes are ignored and params are their static values). Live is set
// when the params change over time: the builder must then return the same
// steps whatever the values (no leaving a filter out at zero).
type Ctx struct {
	W, H   int
	FPS    float64
	Frames int
	Live   bool
}

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
	Amount  float64          // 0..1; 1 = full
	Keys    map[string][]Key // per param, T in clip-local ms
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
	var cmds []Command
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
		built := b(d.Resolve(e.Params), ctx)
		if _, keyed := keyedParams(d, e); keyed && ctx.FPS > 0 && ctx.Frames > 1 {
			var c []Command
			var err error
			if built, c, err = keyedSteps(d, e, b, ctx, fmt.Sprintf("%s_%d", strings.Trim(id, "[]"), i)); err != nil {
				return nil, err
			}
			cmds = append(cmds, c...)
		}
		for _, s := range built {
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
	if len(cmds) > 0 {
		send, err := SendcmdFilter(cmds, ctx.FPS, work)
		if err != nil {
			return nil, err
		}
		next := label()
		stmts = append(stmts, cur+send+next)
		cur = next
	}
	for i := 0; i < len(steps); {
		j := i + 1
		for j < len(steps) && steps[j].group == steps[i].group {
			j++
		}
		span := make([]Step, 0, j-i)
		for _, t := range steps[i:j] {
			span = append(span, t.step)
		}
		next := label()
		if steps[i].group == "" {
			body, err := spanGraph(span, work, ctx.FPS, cur, next, label)
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, body...)
		} else {
			// The group's input, kept as straight-alpha gbrap for the blend.
			a, b, done := label(), label(), label()
			body, err := spanGraph(span, work, ctx.FPS, b, done, label, "format=gbrap")
			if err != nil {
				return nil, err
			}
			stmts = append(stmts, cur+"format=gbrap,split"+a+b)
			stmts = append(stmts, body...)
			stmts = append(stmts, a+done+"blend=all_mode=normal:all_opacity="+num(steps[i].amt)+next)
		}
		cur = next
		i = j
	}
	// The caller's out label: one pass-through to name it.
	stmts = append(stmts, cur+"null"+out)
	return stmts, nil
}

// spanGraph is the statements that take label in through steps to label
// out: linear chains (colour runs as lut3d, filters as they are,
// format=gbrap once before the first step that needs RGB), broken around
// graph steps, with tail's filters at the very end.
func spanGraph(steps []Step, work string, fps float64, in, out string, label func() string, tail ...string) ([]string, error) {
	var stmts, parts []string
	cur := in
	flush := func(to string) {
		if len(parts) == 0 {
			parts = []string{"null"}
		}
		stmts = append(stmts, cur+strings.Join(parts, ",")+to)
		parts, cur = nil, to
	}
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
			for j < len(steps) && steps[j].Color != nil {
				j++
			}
			luts, err := colorRun(steps[i:j], work, fps)
			if err != nil {
				return nil, err
			}
			toRGB()
			parts = append(parts, luts...)
			i = j
			continue
		}
		if steps[i].RGB {
			toRGB()
		}
		if g := steps[i].Graph; g != nil {
			if len(parts) > 0 {
				flush(label())
			}
			next := label()
			stmts = append(stmts, g(cur, next, label)...)
			cur = next
		} else {
			parts = append(parts, steps[i].Filters...)
		}
		i++
	}
	parts = append(parts, tail...)
	flush(out)
	return stmts, nil
}

// colorRun is the lut3d filters for a run of colour steps: one LUT, or for
// a keyframed run one per distinct frame, each enabled over its frames
// (half-open windows between frame midpoints).
func colorRun(run []Step, work string, fps float64) ([]string, error) {
	lut := func(fns []ColorFn, enable string) (string, error) {
		path, err := writeLut(fns, work)
		if err != nil {
			return "", err
		}
		return "lut3d=file=" + filterValue(filepath.ToSlash(path)) + ":interp=trilinear" + enable, nil
	}
	frames := 0
	for _, s := range run {
		frames = max(frames, len(s.ColorFrames))
	}
	if frames == 0 {
		fns := make([]ColorFn, len(run))
		for k, s := range run {
			fns[k] = s.Color
		}
		f, err := lut(fns, "")
		return []string{f}, err
	}
	keys := make([]string, frames)
	for n := range keys {
		var b strings.Builder
		for _, s := range run {
			if len(s.ColorKeys) > n {
				b.WriteString(s.ColorKeys[n])
			}
			b.WriteByte('|')
		}
		keys[n] = b.String()
	}
	runs := lutWindows(keys)
	var out []string
	for k, r := range runs {
		fns := make([]ColorFn, len(run))
		for x, s := range run {
			fns[x] = s.Color
			if len(s.ColorFrames) > r.sample {
				fns[x] = s.ColorFrames[r.sample]
			}
		}
		var cond []string
		if k > 0 {
			cond = append(cond, "gte(t,"+num((float64(r.first)-0.5)/fps)+")")
		}
		if k < len(runs)-1 {
			cond = append(cond, "lt(t,"+num((float64(r.last)+0.5)/fps)+")")
		}
		enable := ""
		if len(cond) > 0 {
			enable = ":enable=" + quoted(strings.Join(cond, "*"))
		}
		f, err := lut(fns, enable)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
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

// orIdentity is fn, or the colour function that changes nothing.
func orIdentity(fn ColorFn) ColorFn {
	if fn != nil {
		return fn
	}
	return func(c RGB) RGB { return c }
}

// onOff is a timeline enable value.
func onOff(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
