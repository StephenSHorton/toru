package capture

import "testing"

func TestCropToPhysical(t *testing.T) {
	tests := []struct {
		name              string
		cl, ct, cw, ch, s float64
		bx, by            int
		wantSub           Rect
		wantEmit          Rect
	}{
		{
			name: "s=1.0 primary origin",
			cl:   100, ct: 50, cw: 400, ch: 300, s: 1.0,
			bx: 0, by: 0,
			wantSub:  Rect{X: 100, Y: 50, W: 400, H: 300},
			wantEmit: Rect{X: 100, Y: 50, W: 400, H: 300},
		},
		{
			name: "s=1.5 hidpi positive origin",
			cl:   100, ct: 50, cw: 400, ch: 300, s: 1.5,
			bx: 1920, by: 0,
			wantSub:  Rect{X: 150, Y: 75, W: 600, H: 450},
			wantEmit: Rect{X: 1920 + 150, Y: 75, W: 600, H: 450},
		},
		{
			name: "s=1.5 hidpi negative origin (worked example)",
			cl:   100, ct: 50, cw: 400, ch: 300, s: 1.5,
			bx: -1920, by: 0,
			wantSub:  Rect{X: 150, Y: 75, W: 600, H: 450},
			wantEmit: Rect{X: -1920 + 150, Y: 75, W: 600, H: 450},
		},
		{
			name: "s=2.0 retina negative x and y",
			cl:   10, ct: 20, cw: 300, ch: 200, s: 2.0,
			bx: -2560, by: -100,
			wantSub:  Rect{X: 20, Y: 40, W: 600, H: 400},
			wantEmit: Rect{X: -2560 + 20, Y: -100 + 40, W: 600, H: 400},
		},
		{
			name: "round-half-up on fractional scale",
			// 0.5*1.5 = 0.75 -> 1 ; 1.5*1.5 = 2.25 -> 2 ; 3*1.5=4.5 -> 5(round half up); 5*1.5=7.5->8
			cl: 0.5, ct: 1.5, cw: 3, ch: 5, s: 1.5,
			bx: 0, by: 0,
			wantSub:  Rect{X: 1, Y: 2, W: 5, H: 8},
			wantEmit: Rect{X: 1, Y: 2, W: 5, H: 8},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			emit, sub := CropToPhysical(tc.cl, tc.ct, tc.cw, tc.ch, tc.s, tc.bx, tc.by)
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
