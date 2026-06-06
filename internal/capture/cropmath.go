package capture

import "math"

// rnd rounds a float64 to the nearest int using round-half-up (math.Round),
// NEVER truncation. This is the SINGLE rounding helper used by all DPI math so
// the emitted contract Rect, the frozen-still sub-rect, and the on-screen badge
// are byte-identical.
//
// Parity note vs the TS mirror (Overlay.tsx Math.round): Go math.Round rounds
// half AWAY from zero while JS Math.round rounds half toward +Inf — they ONLY
// differ on an exact .5 of a NEGATIVE product. CropToPhysical's rounded operands
// (cl*s, ct*s, (cl+cw)*s, (ct+ch)*s) are all non-negative because cl,ct,cw,ch
// are >= 0 (the crop is clamped to its monitor; cross-monitor crop is deferred),
// so the two implementations agree exactly. bx/by are added as ints, never
// rounded. Revisit this if a negative coordinate is ever rounded.
func rnd(v float64) int { return int(math.Round(v)) }

// CropToPhysical converts a CSS-px crop rectangle (within one monitor's overlay
// window viewport) into BOTH consumers of the locked DPI formula:
//
//   - emit: the CaptureRequest.Rect in VIRTUAL-DESKTOP PHYSICAL px (origin =
//     primary top-left; monitors left/above carry NEGATIVE X/Y). This is the
//     frozen contract space (== kbinani GetDisplayBounds / contract.go).
//   - sub:  the MONITOR-LOCAL PHYSICAL px crop region inside that monitor's
//     frozen still PNG (whose origin is the monitor's own top-left).
//
// Inputs:
//
//	cl,ct,cw,ch = crop rect in CSS px within the monitor's overlay viewport.
//	s           = that monitor's DPI scale (CSS px * s = physical px; may be !=1).
//	bx,by       = that monitor's virtual-desktop PHYSICAL origin (ScreenInfo.X/Y;
//	              MAY be negative for monitors left/above the primary).
//	monW,monH   = that monitor's PHYSICAL width/height (ScreenInfo.W/H). The
//	              rounded right/bottom edges are CLAMPED to these so the result
//	              can never exceed the native monitor.
//
// EDGE-based rounding (not width-based): the CSS extent is the Wails DIP Bounds,
// which Wails derives as ceil(physical/scale). Re-multiplying a ceil'd DIP width
// by scale and rounding can land 1px PAST the true native width (e.g. 2560@150%:
// DIP 1707, round(1707*1.5)=2561 > 2560). Rounding the left and right EDGES
// independently and clamping the right edge to monW yields a width that always
// fits the frozen still and the monitor — keeping the badge, the saved PNG, and
// the emitted/recorded Rect identical.
func CropToPhysical(cl, ct, cw, ch, s float64, bx, by, monW, monH int) (emit Rect, sub Rect) {
	rl := rnd(cl * s)
	rt := rnd(ct * s)
	// Round the far edges independently, then clamp to the physical monitor size.
	rr := rnd((cl + cw) * s)
	rb := rnd((ct + ch) * s)
	if monW > 0 && rr > monW {
		rr = monW
	}
	if monH > 0 && rb > monH {
		rb = monH
	}
	rw := rr - rl
	rh := rb - rt

	sub = Rect{X: rl, Y: rt, W: rw, H: rh}
	emit = Rect{X: bx + rl, Y: by + rt, W: rw, H: rh}
	return emit, sub
}
