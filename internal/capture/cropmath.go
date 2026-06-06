package capture

import "math"

// rnd rounds a float64 to the nearest int using round-half-up (math.Round),
// NEVER truncation. This is the SINGLE rounding helper used by all DPI math so
// the emitted contract Rect, the frozen-still sub-rect, and the on-screen badge
// are byte-identical.
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
//
// Rounding happens ONCE (rl/rt/rw/rh) and is reused for both rects and the
// badge, so the saved PNG's pixel dimensions match the displayed number exactly.
func CropToPhysical(cl, ct, cw, ch, s float64, bx, by int) (emit Rect, sub Rect) {
	rl := rnd(cl * s)
	rt := rnd(ct * s)
	rw := rnd(cw * s)
	rh := rnd(ch * s)

	sub = Rect{X: rl, Y: rt, W: rw, H: rh}
	emit = Rect{X: bx + rl, Y: by + rt, W: rw, H: rh}
	return emit, sub
}
