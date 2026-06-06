package capture

import (
	"math"
	"testing"
)

func TestCropToPhysical(t *testing.T) {
	tests := []struct {
		name              string
		cl, ct, cw, ch, s float64
		bx, by            int
		monW, monH        int
		wantSub           Rect
		wantEmit          Rect
	}{
		{
			name: "s=1.0 primary origin",
			cl:   100, ct: 50, cw: 400, ch: 300, s: 1.0,
			bx: 0, by: 0, monW: 1920, monH: 1080,
			wantSub:  Rect{X: 100, Y: 50, W: 400, H: 300},
			wantEmit: Rect{X: 100, Y: 50, W: 400, H: 300},
		},
		{
			name: "s=1.5 hidpi positive origin",
			cl:   100, ct: 50, cw: 400, ch: 300, s: 1.5,
			bx: 1920, by: 0, monW: 2560, monH: 1440,
			wantSub:  Rect{X: 150, Y: 75, W: 600, H: 450},
			wantEmit: Rect{X: 1920 + 150, Y: 75, W: 600, H: 450},
		},
		{
			name: "s=1.5 hidpi negative origin (worked example)",
			cl:   100, ct: 50, cw: 400, ch: 300, s: 1.5,
			bx: -1920, by: 0, monW: 2560, monH: 1440,
			wantSub:  Rect{X: 150, Y: 75, W: 600, H: 450},
			wantEmit: Rect{X: -1920 + 150, Y: 75, W: 600, H: 450},
		},
		{
			name: "s=2.0 retina negative x and y",
			cl:   10, ct: 20, cw: 300, ch: 200, s: 2.0,
			bx: -2560, by: -100, monW: 3840, monH: 2160,
			wantSub:  Rect{X: 20, Y: 40, W: 600, H: 400},
			wantEmit: Rect{X: -2560 + 20, Y: -100 + 40, W: 600, H: 400},
		},
		{
			name: "round-half-up on fractional scale (edge-based)",
			// EDGE-based: rl=round(0.5*1.5)=1; rr=round((0.5+3)*1.5)=round(5.25)=5 -> w=4.
			//             rt=round(1.5*1.5)=2; rb=round((1.5+5)*1.5)=round(9.75)=10 -> h=8.
			cl: 0.5, ct: 1.5, cw: 3, ch: 5, s: 1.5,
			bx: 0, by: 0, monW: 100, monH: 100,
			wantSub:  Rect{X: 1, Y: 2, W: 4, H: 8},
			wantEmit: Rect{X: 1, Y: 2, W: 4, H: 8},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emit, sub := CropToPhysical(tc.cl, tc.ct, tc.cw, tc.ch, tc.s, tc.bx, tc.by, tc.monW, tc.monH)
			if sub != tc.wantSub {
				t.Errorf("sub = %+v, want %+v", sub, tc.wantSub)
			}
			if emit != tc.wantEmit {
				t.Errorf("emit = %+v, want %+v", emit, tc.wantEmit)
			}
			// Badge is sub.W x sub.H == emit.W x emit.H (same rounded values).
			if sub.W != emit.W || sub.H != emit.H {
				t.Errorf("badge mismatch: sub %dx%d vs emit %dx%d", sub.W, sub.H, emit.W, emit.H)
			}
		})
	}
}

// TestCropToPhysical_CeiledDIPFullExtent reproduces the off-by-one that finding
// #1 warns about: the CSS extent is the Wails DIP Bounds = ceil(physical/scale).
// A full-width crop (left=0, width=DIP) must NEVER round to more than the native
// monitor width, or the badge/emit/recorded Rect overshoot the frozen still by
// 1px. Edge-based rounding + clamp to monW/monH must guarantee rl+rw <= monW.
func TestCropToPhysical_CeiledDIPFullExtent(t *testing.T) {
	cases := []struct {
		scale      float64
		physW      int
		physH      int
	}{
		{1.25, 2560, 1440},
		{1.5, 2560, 1440},
		{1.75, 2560, 1440},
		{1.5, 3000, 2000},
		{1.25, 1366, 768},
	}
	for _, c := range cases {
		// DIP extent Wails reports = ceil(physical/scale).
		dipW := int(math.Ceil(float64(c.physW) / c.scale))
		dipH := int(math.Ceil(float64(c.physH) / c.scale))
		emit, sub := CropToPhysical(0, 0, float64(dipW), float64(dipH), c.scale, 0, 0, c.physW, c.physH)
		if sub.X+sub.W > c.physW {
			t.Errorf("scale %.2f %dx%d: sub right %d exceeds physical width %d", c.scale, c.physW, c.physH, sub.X+sub.W, c.physW)
		}
		if sub.Y+sub.H > c.physH {
			t.Errorf("scale %.2f %dx%d: sub bottom %d exceeds physical height %d", c.scale, c.physW, c.physH, sub.Y+sub.H, c.physH)
		}
		if emit.W != sub.W || emit.H != sub.H {
			t.Errorf("scale %.2f: emit %dx%d != sub %dx%d", c.scale, emit.W, emit.H, sub.W, sub.H)
		}
	}
}

func TestRndHalfUp(t *testing.T) {
	cases := map[float64]int{
		0.4: 0, 0.5: 1, 0.6: 1, 1.5: 2, 2.5: 3, -0.5: -1,
	}
	for in, want := range cases {
		if got := rnd(in); got != want {
			t.Errorf("rnd(%v) = %d, want %d", in, got, want)
		}
	}
}
