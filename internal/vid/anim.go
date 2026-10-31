package vid

import (
	"fmt"
	"math"
	"strconv"

	effectfx "github.com/StephenSHorton/toru/internal/fx"
)

// anim.go places a v3 clip on the canvas with its keyframed transform
// (clip.anim, docs/video-editor-v3.md "Keyframes in the export"), the way
// the preview's Compositor draws it (fx/ops.ts animAt): x/y are the centre
// in 0..1 of the canvas, scale 1 fits the picture inside the canvas,
// rotation is degrees clockwise about the centre, opacity multiplies alpha.
// Keyframe times are clip-local timeline ms.
//
//   scale     scale with eval=frame and the size in t (even, as ClipSize)
//   rotation  rotate with the angle in t, into a fixed box that holds the
//             largest size at any angle, transparent outside the picture
//   opacity   colorchannelmixer aa, set per frame by sendcmd
//   x/y       overlay x/y expressions in t (canvas time minus the start)
//
// A clip without keyframes or rotation gets exactly the static chain.

// animKeys is a clip's keyframes for one target, as effectfx keys.
func animKeys(c ClipJSON, target string) []effectfx.Key {
	ks := c.Anim[target]
	if len(ks) == 0 {
		return nil
	}
	out := make([]effectfx.Key, len(ks))
	for i, k := range ks {
		out[i] = effectfx.Key{T: float64(k.T), V: k.V, Ease: k.Ease}
	}
	return out
}

// animRange is the smallest and largest value a target takes: its keys,
// or base without keys.
func animRange(keys []effectfx.Key, base float64) (float64, float64) {
	if len(keys) == 0 {
		return base, base
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, k := range keys {
		v, ok := k.V.(float64)
		if !ok {
			v = base
		}
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	return lo, hi
}

// clipMaxOpacity is the most opaque a clip gets (a clip keyed up from 0
// still exports).
func clipMaxOpacity(c ClipJSON) float64 {
	_, hi := animRange(animKeys(c, "opacity"), clipOpacity(c))
	return clampF(hi, 0, 1)
}

// clipPlacement is the end of a clip's video chain (size, alpha, rotation)
// and where the overlay puts it: x/y as overlay arguments. srcW/srcH is the
// picture's size, w/h the canvas's.
func clipPlacement(c ClipJSON, srcW, srcH, w, h int, fps float64, work string, layer int) ([]string, string, string, error) {
	tr := clipTransform(c)
	sk, rk, ok, xk, yk := animKeys(c, "scale"), animKeys(c, "rotation"), animKeys(c, "opacity"), animKeys(c, "x"), animKeys(c, "y")
	var parts []string

	cw, ch := ClipSize(srcW, srcH, w, h, tr.Scale)
	maxW, maxH := cw, ch
	if len(sk) > 0 && srcW > 0 && srcH > 0 {
		fit := math.Min(float64(w)/float64(srcW), float64(h)/float64(srcH))
		s := effectfx.KeysExpr(sk, tr.Scale, "t")
		size := func(n int) string {
			// evenDim(round(n·fit·scale)), as ClipSize.
			return "'max(2,2*trunc(round(" + num(float64(n)*fit) + "*max(0," + s + "))/2))'"
		}
		parts = append(parts, "scale=w="+size(srcW)+":h="+size(srcH)+":eval=frame")
		_, hi := animRange(sk, tr.Scale)
		maxW, maxH = ClipSize(srcW, srcH, w, h, hi)
	} else {
		parts = append(parts, fmt.Sprintf("scale=%d:%d", cw, ch))
	}

	rotated := len(rk) > 0 || tr.Rotation != 0
	if rotated || len(ok) > 0 || clipOpacity(c) < 1 {
		parts = append(parts, "format=rgba")
	} else {
		parts = append(parts, "format=yuva420p")
	}
	if len(ok) > 0 {
		frames := effectfx.Frames(float64(ClipLengthMs(c)), fps)
		target := fmt.Sprintf("colorchannelmixer@op%d", layer)
		cmds := effectfx.NumberCommands(target, "aa", frames, func(n int) float64 {
			v, _ := effectfx.ValueAt(ok, effectfx.FrameMs(n, fps), clipOpacity(c)).(float64)
			return clampF(v, 0, 1)
		})
		send, err := effectfx.SendcmdFilter(cmds, fps, work)
		if err != nil {
			return nil, "", "", err
		}
		parts = append(parts, send, target+"=aa="+cmds[0].Value)
	} else if op := clipOpacity(c); op < 1 {
		parts = append(parts, "colorchannelmixer=aa="+num(op))
	}
	box := 0
	if rotated {
		box = evenDim(int(math.Ceil(math.Hypot(float64(maxW), float64(maxH))))) + 2
		angle := num(tr.Rotation * math.Pi / 180)
		if len(rk) > 0 {
			angle = "'(" + effectfx.KeysExpr(rk, tr.Rotation, "t") + ")*PI/180'"
		}
		parts = append(parts, fmt.Sprintf("rotate=a=%s:ow=%d:oh=%d:fillcolor=black@0", angle, box, box))
	}

	// Where the top-left goes: a whole pixel when nothing moves, else
	// expressions (overlay_w/h follow an animated size).
	if len(xk) == 0 && len(yk) == 0 && len(sk) == 0 {
		bw, bh := cw, ch
		if rotated {
			bw, bh = box, box
		}
		x := int(math.Round(tr.X*float64(w) - float64(bw)/2))
		y := int(math.Round(tr.Y*float64(h) - float64(bh)/2))
		return parts, strconv.Itoa(x), strconv.Itoa(y), nil
	}
	lt := "(t-" + msSeconds(c.Start) + ")"
	pos := func(keys []effectfx.Key, base float64, n int, size string) string {
		return "'round((" + effectfx.KeysExpr(keys, base, lt) + ")*" + strconv.Itoa(n) + "-" + size + "/2)'"
	}
	return parts, pos(xk, tr.X, w, "overlay_w"), pos(yk, tr.Y, h, "overlay_h"), nil
}
