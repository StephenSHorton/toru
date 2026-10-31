package fx

import (
	"fmt"
	"strings"
)

// keyed.go exports an effect whose params have keyframes
// (docs/video-editor-v3.md, Keyframes in the export). Its builder runs once
// per output frame with the params at that frame (ctx.Live set, so it
// always returns the same steps). Then:
//
//   filters  each filter is named (name@instance), and every option whose
//            value changes from one frame to the next becomes a sendcmd
//            command at that frame
//   colour   the step keeps its colour function per frame; the colour run's
//            LUT is generated per distinct frame and each LUT is enabled for
//            its frames (lut3d can't load a new file at run time)
//
// Only params whose definition allows keyframes ("cmd") animate; keys on
// others are ignored, as the editor never writes them.

// MaxLutsPerRun caps the LUTs a keyframed colour run writes (each is ~1 MB
// and loads at the start); past it, frames share LUTs in even steps.
const MaxLutsPerRun = 240

// keyedParams is e's params at frame n, keyframes applied and clamped, and
// whether any animatable param has keys at all.
func keyedParams(d Def, e Effect) (func(n int, fps float64) map[string]any, bool) {
	base := d.Resolve(e.Params)
	var live []ParamDef
	for _, p := range d.Params {
		if len(e.Keys[p.ID]) > 0 && p.Animate != "" {
			live = append(live, p)
		}
	}
	at := func(n int, fps float64) map[string]any {
		if len(live) == 0 {
			return base
		}
		out := make(map[string]any, len(base))
		for k, v := range base {
			out[k] = v
		}
		for _, p := range live {
			if v, ok := p.Clamp(ValueAt(e.Keys[p.ID], FrameMs(n, fps), base[p.ID])); ok {
				out[p.ID] = v
			}
		}
		return out
	}
	return at, len(live) > 0
}

// keyedSteps is the steps of a keyframed effect over ctx.Frames frames and
// the commands that animate them; inst names its filter instances.
func keyedSteps(d Def, e Effect, b Builder, ctx Ctx, inst string) ([]Step, []Command, error) {
	at, _ := keyedParams(d, e)
	live := ctx
	live.Live = true
	frames := make([][]Step, ctx.Frames)
	keys := make([]string, ctx.Frames)
	for n := range frames {
		p := at(n, ctx.FPS)
		frames[n] = b(p, live)
		keys[n] = fmt.Sprint(p)
	}
	first := frames[0]
	for n, s := range frames {
		if len(s) != len(first) {
			return nil, nil, fmt.Errorf("effects: %s changes shape between frames 0 and %d, so it can't be keyframed", d.Type, n)
		}
	}
	out := make([]Step, len(first))
	var cmds []Command
	for i, s0 := range first {
		switch {
		case s0.Color != nil:
			s := s0
			s.ColorFrames = make([]ColorFn, len(frames))
			s.ColorKeys = keys
			for n := range frames {
				s.ColorFrames[n] = frames[n][i].Color
			}
			out[i] = s
		case s0.Graph != nil:
			out[i] = s0 // graph steps don't animate
		default:
			s := s0
			s.Filters = make([]string, len(s0.Filters))
			for j, f := range s0.Filters {
				name, args, _ := strings.Cut(f, "=")
				target := fmt.Sprintf("%s@%s_%d_%d", name, inst, i, j)
				s.Filters[j] = target
				if args != "" {
					s.Filters[j] += "=" + args
				}
				prev := filterOptions(args)
				for n := 1; n < len(frames); n++ {
					fs := frames[n][i].Filters
					if len(fs) != len(s0.Filters) || !strings.HasPrefix(fs[j], name+"=") && fs[j] != name {
						return nil, nil, fmt.Errorf("effects: %s changes its filters between frames, so it can't be keyframed", d.Type)
					}
					_, a, _ := strings.Cut(fs[j], "=")
					cur := filterOptions(a)
					for _, kv := range cur {
						if v, ok := lookupOpt(prev, kv[0]); !ok || v != kv[1] {
							cmds = append(cmds, Command{Frame: n, Target: target, Option: kv[0], Value: kv[1]})
						}
					}
					prev = cur
				}
			}
			out[i] = s
		}
	}
	return out, cmds, nil
}

// filterOptions splits a filter's "k=v:k=v" arguments (colons inside '…'
// stay in their value).
func filterOptions(args string) [][2]string {
	var out [][2]string
	quoted := false
	start := 0
	for i := 0; i <= len(args); i++ {
		if i < len(args) && args[i] == '\'' {
			quoted = !quoted
		}
		if i == len(args) || (args[i] == ':' && !quoted) {
			if part := args[start:i]; part != "" {
				k, v, _ := strings.Cut(part, "=")
				out = append(out, [2]string{k, v})
			}
			start = i + 1
		}
	}
	return out
}

func lookupOpt(opts [][2]string, k string) (string, bool) {
	for _, kv := range opts {
		if kv[0] == k {
			return kv[1], true
		}
	}
	return "", false
}

// lutRun is frames first..last sharing the LUT of frame sample.
type lutRun struct{ first, last, sample int }

// lutWindows splits frames 0..len(keys)-1 into runs that share a LUT: runs
// of equal keys, at most MaxLutsPerRun (past that, frames are sampled in
// even steps of 2, 4, … frames).
func lutWindows(keys []string) []lutRun {
	for step := 1; ; step *= 2 {
		var out []lutRun
		for n := range keys {
			s := n - n%step
			if len(out) > 0 && keys[out[len(out)-1].sample] == keys[s] {
				out[len(out)-1].last = n
				continue
			}
			out = append(out, lutRun{n, n, s})
		}
		if len(out) <= MaxLutsPerRun {
			return out
		}
	}
}
