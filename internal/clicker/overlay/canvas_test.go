package overlay

import "testing"

func near(a, b, tol int) bool { return a-b <= tol && b-a <= tol }

func TestCanvasFillAndAt(t *testing.T) {
	c := NewCanvas(10, 10)
	c.FillRect(2, 2, 5, 5, Color{255, 0, 0, 255})
	if got := c.At(3, 3); got != (Color{255, 0, 0, 255}) {
		t.Fatalf("opaque fill: got %+v", got)
	}
	if got := c.At(5, 5); got.A != 0 {
		t.Fatalf("fill is exclusive at x1/y1: got %+v", got)
	}
	// Half-transparent blue over the red: premultiplied src-over.
	c.FillRect(0, 0, 10, 10, Color{0, 0, 255, 128})
	got := c.At(3, 3)
	if got.A != 255 || !near(int(got.R), 127, 2) || !near(int(got.B), 128, 2) {
		t.Fatalf("blend: got %+v", got)
	}
	if got := c.At(0, 0); !near(int(got.A), 128, 1) || !near(int(got.B), 255, 2) || got.R != 0 {
		t.Fatalf("over transparent keeps the straight colour: got %+v", got)
	}
	if got := c.At(-1, 99); got.A != 0 {
		t.Fatalf("out of bounds should be transparent: %+v", got)
	}
}

func TestCanvasPixelOrderIsBGRA(t *testing.T) {
	c := NewCanvas(1, 1)
	c.Clear(Color{10, 20, 30, 255})
	if p := c.Pix; p[0] != 30 || p[1] != 20 || p[2] != 10 || p[3] != 255 {
		t.Fatalf("want B,G,R,A = 30,20,10,255, got %v", p)
	}
	c.Clear(Color{200, 100, 0, 128})
	if p := c.Pix; !near(int(p[2]), 100, 1) || !near(int(p[1]), 50, 1) || p[3] != 128 {
		t.Fatalf("Clear must premultiply: %v", p)
	}
}

func TestCanvasStride(t *testing.T) {
	// A 3x2 view onto a 5-px-wide buffer must not write past column 3.
	c := &Canvas{W: 3, H: 2, Stride: 5 * 4, Pix: make([]byte, 5*2*4)}
	c.Clear(Color{255, 255, 255, 255})
	if c.Pix[3*4+3] != 0 || c.Pix[5*4+3*4+3] != 0 {
		t.Fatal("Clear wrote outside the view")
	}
	if c.Pix[5*4+2*4+3] != 255 {
		t.Fatal("second row not cleared")
	}
}

func TestStrokeAndDashedRect(t *testing.T) {
	c := NewCanvas(100, 50)
	c.StrokeRect(0, 0, 100, 50, 2, Color{0, 255, 0, 255})
	for _, p := range [][2]int{{0, 0}, {50, 1}, {99, 49}, {1, 25}, {98, 25}} {
		if c.At(p[0], p[1]).A != 255 {
			t.Errorf("border pixel %v not drawn", p)
		}
	}
	if c.At(50, 25).A != 0 || c.At(2, 2).A != 0 {
		t.Error("stroke filled the inside")
	}

	d := NewCanvas(100, 50)
	d.DashedRect(0, 0, 100, 50, 2, 8, 6, Color{255, 0, 0, 255})
	if d.At(0, 0).A == 0 || d.At(7, 0).A == 0 {
		t.Error("first dash missing")
	}
	if d.At(10, 0).A != 0 {
		t.Error("gap after the first dash should be empty")
	}
	if d.At(14, 1).A == 0 {
		t.Error("second dash missing")
	}
	if d.At(0, 2).A == 0 {
		t.Error("left side should start with a dash below the top band")
	}
}

func TestDiscRingRoundRect(t *testing.T) {
	c := NewCanvas(40, 40)
	c.FillDisc(20, 20, 6, Color{255, 255, 255, 255})
	if c.At(19, 19).A != 255 {
		t.Error("disc centre not opaque")
	}
	if c.At(20+8, 20).A != 0 {
		t.Error("disc drawn outside its radius")
	}

	r := NewCanvas(60, 60)
	r.Ring(30, 30, 20, 2, 0, Color{255, 255, 255, 255})
	if r.At(30, 30).A != 0 {
		t.Error("ring filled its centre")
	}
	if r.At(49, 29).A == 0 && r.At(50, 29).A == 0 {
		t.Error("ring missing at radius")
	}

	b := NewCanvas(40, 20)
	b.FillRoundRect(0, 0, 40, 20, 8, Color{255, 255, 255, 255})
	if b.At(0, 0).A > 40 {
		t.Errorf("rounded corner should be (nearly) empty, alpha %d", b.At(0, 0).A)
	}
	if b.At(20, 10).A != 255 || b.At(20, 0).A != 255 {
		t.Error("round rect body not filled")
	}
}

func TestDrawMask(t *testing.T) {
	c := NewCanvas(4, 4)
	m := &Mask{W: 2, H: 1, A: []uint8{255, 0}}
	c.DrawMask(1, 1, m, Color{0, 0, 255, 255})
	if c.At(1, 1) != (Color{0, 0, 255, 255}) {
		t.Errorf("mask pixel: %+v", c.At(1, 1))
	}
	if c.At(2, 1).A != 0 {
		t.Error("zero coverage drew")
	}
}

func TestDrawNumber(t *testing.T) {
	c := NewCanvas(200, 100)
	h := 70
	c.DrawNumber(100, 50, h, 1, Color{255, 255, 255, 255})
	// "1" is segments b and c: the right column of the digit cell.
	dw, th := digitW(h), max(h/7, 1)
	x0 := 100 - dw/2
	if c.At(x0+dw-th/2-1, 50-h/4).A == 0 || c.At(x0+dw-th/2-1, 50+h/4).A == 0 {
		t.Error("segments b/c of 1 missing")
	}
	if c.At(x0+th/2, 50-h/4).A != 0 {
		t.Error("segment f lit for 1")
	}
	if w := numberSize(12, h); w <= numberSize(1, h) {
		t.Errorf("two digits should be wider than one: %d", w)
	}
}
