package overlay

import (
	"math"
	"strconv"
)

// Color is a straight (not premultiplied) 8-bit colour.
type Color struct{ R, G, B, A uint8 }

// Fade returns c with its alpha scaled by f (0..1).
func (c Color) Fade(f float64) Color {
	c.A = uint8(clampF(float64(c.A)*f, 0, 255) + 0.5)
	return c
}

// Canvas is a premultiplied BGRA pixel buffer: the byte order a 32-bit
// top-down DIB uses, so a layered window can show it without conversion.
// Stride is the row length in bytes and may exceed W*4 when the canvas is a
// window onto a larger DIB. Drawing is clipped to W x H.
type Canvas struct {
	W, H   int
	Stride int
	Pix    []byte
}

// NewCanvas returns a transparent w x h canvas.
func NewCanvas(w, h int) *Canvas {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Canvas{W: w, H: h, Stride: w * 4, Pix: make([]byte, w*h*4)}
}

// At returns the straight colour at (x, y); out of bounds is transparent.
func (c *Canvas) At(x, y int) Color {
	if x < 0 || y < 0 || x >= c.W || y >= c.H {
		return Color{}
	}
	i := y*c.Stride + x*4
	a := c.Pix[i+3]
	if a == 0 {
		return Color{}
	}
	un := func(v byte) uint8 { return uint8((int(v)*255 + int(a)/2) / int(a)) }
	return Color{R: un(c.Pix[i+2]), G: un(c.Pix[i+1]), B: un(c.Pix[i]), A: a}
}

// Clear sets every pixel to col (no blending). It writes one row and copies
// it down, so clearing a full monitor is a few memmoves.
func (c *Canvas) Clear(col Color) {
	if c.W == 0 || c.H == 0 {
		return
	}
	b, g, r, a := premul(col)
	row := c.Pix[:c.W*4]
	for x := 0; x < c.W; x++ {
		row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = b, g, r, a
	}
	for y := 1; y < c.H; y++ {
		copy(c.Pix[y*c.Stride:y*c.Stride+c.W*4], row)
	}
}

// blend composites col at coverage cov (0..1) over the pixel at (x, y).
func (c *Canvas) blend(x, y int, col Color, cov float64) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H || cov <= 0 {
		return
	}
	sa := float64(col.A) * math.Min(cov, 1) / 255
	if sa <= 0 {
		return
	}
	i := y*c.Stride + x*4
	inv := 1 - sa
	p := c.Pix[i : i+4 : i+4]
	p[0] = uint8(float64(col.B)*sa + float64(p[0])*inv + 0.5)
	p[1] = uint8(float64(col.G)*sa + float64(p[1])*inv + 0.5)
	p[2] = uint8(float64(col.R)*sa + float64(p[2])*inv + 0.5)
	p[3] = uint8(255*sa + float64(p[3])*inv + 0.5)
}

// FillRect blends col over [x0,x1) x [y0,y1).
func (c *Canvas) FillRect(x0, y0, x1, y1 int, col Color) {
	x0, y0, x1, y1 = c.clip(x0, y0, x1, y1)
	if x0 >= x1 || y0 >= y1 || col.A == 0 {
		return
	}
	if col.A == 255 { // opaque: plain stores
		b, g, r, a := premul(col)
		for y := y0; y < y1; y++ {
			row := c.Pix[y*c.Stride:]
			for x := x0; x < x1; x++ {
				row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = b, g, r, a
			}
		}
		return
	}
	// Integer src-over: dst = src + dst*(255-a)/255, src premultiplied.
	sb, sg, sr, sa := premul(col)
	inv := 255 - uint32(sa)
	for y := y0; y < y1; y++ {
		row := c.Pix[y*c.Stride:]
		for x := x0; x < x1; x++ {
			p := row[x*4 : x*4+4 : x*4+4]
			p[0] = sb + uint8((uint32(p[0])*inv+127)/255)
			p[1] = sg + uint8((uint32(p[1])*inv+127)/255)
			p[2] = sr + uint8((uint32(p[2])*inv+127)/255)
			p[3] = sa + uint8((uint32(p[3])*inv+127)/255)
		}
	}
}

// StrokeRect draws a w-px border inside [x0,x1) x [y0,y1).
func (c *Canvas) StrokeRect(x0, y0, x1, y1, w int, col Color) {
	if w <= 0 || x1 <= x0 || y1 <= y0 {
		return
	}
	if 2*w >= x1-x0 || 2*w >= y1-y0 {
		c.FillRect(x0, y0, x1, y1, col)
		return
	}
	c.FillRect(x0, y0, x1, y0+w, col)
	c.FillRect(x0, y1-w, x1, y1, col)
	c.FillRect(x0, y0+w, x0+w, y1-w, col)
	c.FillRect(x1-w, y0+w, x1, y1-w, col)
}

// DashedRect draws a w-px dashed border inside [x0,x1) x [y0,y1). Each side
// starts with a dash at its corner so the corners always read as solid.
func (c *Canvas) DashedRect(x0, y0, x1, y1, w, dash, gap int, col Color) {
	if w <= 0 || x1 <= x0 || y1 <= y0 {
		return
	}
	if dash <= 0 || gap <= 0 {
		c.StrokeRect(x0, y0, x1, y1, w, col)
		return
	}
	period := dash + gap
	for x := x0; x < x1; x += period {
		e := min(x+dash, x1)
		c.FillRect(x, y0, e, min(y0+w, y1), col)
		c.FillRect(x, max(y1-w, y0+w), e, y1, col)
	}
	for y := y0 + w; y < y1-w; y += period {
		e := min(y+dash, y1-w)
		c.FillRect(x0, y, min(x0+w, x1), e, col)
		c.FillRect(max(x1-w, x0+w), y, x1, e, col)
	}
}

// FillDisc draws an anti-aliased filled circle.
func (c *Canvas) FillDisc(cx, cy, r float64, col Color) {
	if r <= 0 {
		return
	}
	x0, y0, x1, y1 := c.clip(int(math.Floor(cx-r-1)), int(math.Floor(cy-r-1)), int(math.Ceil(cx+r+1)), int(math.Ceil(cy+r+1)))
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			c.blend(x, y, col, clampF(r+0.5-d, 0, 1))
		}
	}
}

// Ring draws an anti-aliased circle outline of width w centred on radius r.
// dash > 0 makes it dashed, dash px on and dash px off along the circumference.
func (c *Canvas) Ring(cx, cy, r, w, dash float64, col Color) {
	if r <= 0 || w <= 0 {
		return
	}
	o := r + w/2 + 1
	x0, y0, x1, y1 := c.clip(int(math.Floor(cx-o)), int(math.Floor(cy-o)), int(math.Ceil(cx+o)), int(math.Ceil(cy+o)))
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			cov := clampF(w/2+0.5-math.Abs(math.Hypot(dx, dy)-r), 0, 1)
			if cov == 0 {
				continue
			}
			if dash > 0 {
				arc := (math.Atan2(dy, dx) + math.Pi) * r
				if math.Mod(arc, 2*dash) >= dash {
					continue
				}
			}
			c.blend(x, y, col, cov)
		}
	}
}

// FillRoundRect blends col over [x0,x1) x [y0,y1) with anti-aliased corners of
// radius rad (clamped to half the shorter side).
func (c *Canvas) FillRoundRect(x0, y0, x1, y1 int, rad float64, col Color) {
	if x1 <= x0 || y1 <= y0 {
		return
	}
	rad = math.Min(rad, float64(min(x1-x0, y1-y0))/2)
	if rad <= 0.5 {
		c.FillRect(x0, y0, x1, y1, col)
		return
	}
	// Straight middle bands, then the four rounded corners pixel by pixel.
	ri := int(math.Ceil(rad))
	c.FillRect(x0, y0+ri, x1, y1-ri, col)
	c.FillRect(x0+ri, y0, x1-ri, y0+ri, col)
	c.FillRect(x0+ri, y1-ri, x1-ri, y1, col)
	corner := func(px0, py0 int, ccx, ccy float64) {
		for y := py0; y < py0+ri; y++ {
			for x := px0; x < px0+ri; x++ {
				d := math.Hypot(float64(x)+0.5-ccx, float64(y)+0.5-ccy)
				c.blend(x, y, col, clampF(rad+0.5-d, 0, 1))
			}
		}
	}
	fx0, fy0, fx1, fy1 := float64(x0), float64(y0), float64(x1), float64(y1)
	corner(x0, y0, fx0+rad, fy0+rad)
	corner(x1-ri, y0, fx1-rad, fy0+rad)
	corner(x0, y1-ri, fx0+rad, fy1-rad)
	corner(x1-ri, y1-ri, fx1-rad, fy1-rad)
}

// Mask is an 8-bit coverage map (0 = none, 255 = full), e.g. rendered text.
type Mask struct {
	W, H int
	A    []uint8
}

// DrawMask blends col over the canvas through m, with m's top-left at (x, y).
func (c *Canvas) DrawMask(x, y int, m *Mask, col Color) {
	if m == nil {
		return
	}
	for my := 0; my < m.H; my++ {
		for mx := 0; mx < m.W; mx++ {
			if a := m.A[my*m.W+mx]; a != 0 {
				c.blend(x+mx, y+my, col, float64(a)/255)
			}
		}
	}
}

// sevenSeg lists the lit segments (a b c d e f g) of each digit.
var sevenSeg = [10]uint8{
	0b1111110, 0b0110000, 0b1101101, 0b1111001, 0b0110011,
	0b1011011, 0b1011111, 0b1110000, 0b1111111, 0b1111011,
}

// numberSize is the width of n drawn h px tall by DrawNumber.
func numberSize(n, h int) (w int) {
	digits := len(strconv.Itoa(n))
	dw, sp := digitW(h), max(h/8, 1)
	return digits*dw + (digits-1)*sp
}

func digitW(h int) int { return max(h*11/20, 3) }

// DrawNumber draws n as seven-segment digits h px tall, centred on (cx, cy).
// It is the overlay's only built-in "font": big zone indexes and point
// badges need digits only, and segments stay crisp at any size without GDI.
func (c *Canvas) DrawNumber(cx, cy, h, n int, col Color) {
	if h < 5 {
		h = 5
	}
	s := strconv.Itoa(n)
	dw, sp := digitW(h), max(h/8, 1)
	t := max(h/7, 1) // stroke
	w := numberSize(n, h)
	// Segments overlap at the joints, so they are drawn into a coverage mask
	// first and blended once; a translucent number then has even colour.
	m := &Mask{W: w, H: h, A: make([]uint8, w*h)}
	fill := func(x0, y0, x1, y1 int) {
		for y := max(y0, 0); y < min(y1, h); y++ {
			for x := max(x0, 0); x < min(x1, w); x++ {
				m.A[y*w+x] = 255
			}
		}
	}
	x := 0
	mid := h/2 - t/2
	for _, ch := range s {
		if ch == '-' {
			fill(x, mid, x+dw, mid+t)
			x += dw + sp
			continue
		}
		seg := sevenSeg[ch-'0']
		if seg&0b1000000 != 0 { // a
			fill(x, 0, x+dw, t)
		}
		if seg&0b0100000 != 0 { // b
			fill(x+dw-t, 0, x+dw, mid+t)
		}
		if seg&0b0010000 != 0 { // c
			fill(x+dw-t, mid, x+dw, h)
		}
		if seg&0b0001000 != 0 { // d
			fill(x, h-t, x+dw, h)
		}
		if seg&0b0000100 != 0 { // e
			fill(x, mid, x+t, h)
		}
		if seg&0b0000010 != 0 { // f
			fill(x, 0, x+t, mid+t)
		}
		if seg&0b0000001 != 0 { // g
			fill(x, mid, x+dw, mid+t)
		}
		x += dw + sp
	}
	c.DrawMask(cx-w/2, cy-h/2, m, col)
}

func (c *Canvas) clip(x0, y0, x1, y1 int) (int, int, int, int) {
	return max(x0, 0), max(y0, 0), min(x1, c.W), min(y1, c.H)
}

// premul returns col premultiplied in BGRA order.
func premul(col Color) (b, g, r, a uint8) {
	m := func(v uint8) uint8 { return uint8((uint32(v)*uint32(col.A) + 127) / 255) }
	return m(col.B), m(col.G), m(col.R), col.A
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
