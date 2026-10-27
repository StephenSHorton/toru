package overlay

import (
	"hash/fnv"
	"math"
	"time"
)

// Scene renderers. Each one paints into a canvas whose top-left is a given
// screen point, so the same code draws a monitor-sized layer or a small
// sprite. They are pure (no Win32) and unit-tested; the Windows side only
// allocates DIBs and shows them. scale is the monitor's DPI scale (1 = 96 dpi)
// and sizes the decoration (borders, dots, labels); geometry the user set
// (corner sizes, zones, point radius) is physical px and never scaled.

// Palette.
var (
	colOutline = Color{255, 255, 255, 72}     // monitor outline, 28 %
	colCorner  = Color{239, 68, 68, 255}      // red
	colEdge    = Color{34, 211, 238, 255}     // cyan
	colStop    = Color{245, 158, 11, 255}     // amber
	colPause   = Color{59, 130, 246, 255}     // blue
	colStart   = Color{34, 197, 94, 255}      // green
	colOff     = Color{156, 163, 175, 255}    // grey: zones turned off
	colShadow  = Color{0, 0, 0, 90}           // point dot shadow
	colWhite   = Color{255, 255, 255, 255}    // point dot border
	colCard    = Color{22, 23, 28, 236}       // bubble / pill / badge background
	colCardRim = Color{255, 255, 255, 26}     // 1 px rim on dark cards
	colText    = Color{242, 243, 245, 255}    // card text
	colAccent  = Color{0x4a, 0xa8, 0xff, 255} // Toru blue
)

func px(v, scale float64) int { return max(int(v*scale+0.5), 1) }

// renderSafety paints the failsafe preview for the area of c whose top-left is
// screen point (ox, oy): monitor outlines, corner squares, edge bands and
// zones. Monitors and zones are in screen px; anything outside c is clipped.
func renderSafety(c *Canvas, ox, oy int32, s SafetyScene, scale float64) {
	bw := px(2, scale)
	for _, m := range s.Monitors {
		l, t, r, b := int(m.L-ox), int(m.T-oy), int(m.R-ox), int(m.B-oy)
		c.StrokeRect(l, t, r, b, bw, colOutline)
	}
	for _, m := range s.Monitors {
		renderEdges(c, ox, oy, m, s, bw)
		renderCorners(c, ox, oy, m, s, bw)
	}
	for _, z := range s.Zones {
		renderZone(c, ox, oy, z, s.ZonesOn, scale)
	}
}

// cornerSizes returns the TL, TR, BL, BR squares clamped to the monitor, all 0
// when corners are off.
func cornerSizes(m Monitor, s SafetyScene) [4]int32 {
	var out [4]int32
	if !s.CornersOn {
		return out
	}
	lim := min(m.R-m.L, m.B-m.T)
	for i, n := range s.Corners {
		out[i] = max(min(n, lim), 0)
	}
	return out
}

func renderCorners(c *Canvas, ox, oy int32, m Monitor, s SafetyScene, bw int) {
	cs := cornerSizes(m, s)
	sq := func(l, t, n int32) {
		if n <= 0 {
			return
		}
		x0, y0 := int(l-ox), int(t-oy)
		x1, y1 := x0+int(n), y0+int(n)
		c.FillRect(x0, y0, x1, y1, colCorner.Fade(0.40))
		c.StrokeRect(x0, y0, x1, y1, min(bw, int(n)), colCorner.Fade(0.90))
	}
	sq(m.L, m.T, cs[0])
	sq(m.R-cs[1], m.T, cs[1])
	sq(m.L, m.B-cs[2], cs[2])
	sq(m.R-cs[3], m.B-cs[3], cs[3])
}

// span is a half-open interval [A, B) along one axis.
type span struct{ A, B int32 }

// edgeSpans returns the parts of [a, b) not covered by cut.
func edgeSpans(a, b int32, cut []span) []span {
	out := []span{{a, b}}
	for _, k := range cut {
		var next []span
		for _, s := range out {
			if k.B <= s.A || k.A >= s.B {
				next = append(next, s)
				continue
			}
			if k.A > s.A {
				next = append(next, span{s.A, k.A})
			}
			if k.B < s.B {
				next = append(next, span{k.B, s.B})
			}
		}
		out = next
	}
	return out
}

// sharedEdges lists, for each side of m (top, right, bottom, left), the spans
// another monitor touches. OuterOnly leaves those parts out, so only the
// desktop's outer border carries an edge band.
func sharedEdges(m Monitor, mons []Monitor) [4][]span {
	var out [4][]span
	for _, o := range mons {
		if o == m {
			continue
		}
		if a, b := max(m.L, o.L), min(m.R, o.R); a < b {
			if o.B == m.T {
				out[0] = append(out[0], span{a, b})
			}
			if o.T == m.B {
				out[2] = append(out[2], span{a, b})
			}
		}
		if a, b := max(m.T, o.T), min(m.B, o.B); a < b {
			if o.L == m.R {
				out[1] = append(out[1], span{a, b})
			}
			if o.R == m.L {
				out[3] = append(out[3], span{a, b})
			}
		}
	}
	return out
}

// edgeBands returns the edge-band rects (screen px, exclusive) for m. Bands
// stop short of the corner squares so the two never overlap.
func edgeBands(m Monitor, s SafetyScene) []Monitor {
	if !s.EdgesOn {
		return nil
	}
	cs := cornerSizes(m, s)
	var shared [4][]span
	if s.OuterOnly {
		shared = sharedEdges(m, s.Monitors)
	}
	var out []Monitor
	top, right, bottom, left := s.Edges[0], s.Edges[1], s.Edges[2], s.Edges[3]
	if top > 0 {
		for _, sp := range edgeSpans(m.L+cs[0], m.R-cs[1], shared[0]) {
			out = append(out, Monitor{sp.A, m.T, sp.B, min(m.T+top, m.B)})
		}
	}
	if right > 0 {
		for _, sp := range edgeSpans(m.T+cs[1], m.B-cs[3], shared[1]) {
			out = append(out, Monitor{max(m.R-right, m.L), sp.A, m.R, sp.B})
		}
	}
	if bottom > 0 {
		for _, sp := range edgeSpans(m.L+cs[2], m.R-cs[3], shared[2]) {
			out = append(out, Monitor{sp.A, max(m.B-bottom, m.T), sp.B, m.B})
		}
	}
	if left > 0 {
		for _, sp := range edgeSpans(m.T+cs[0], m.B-cs[2], shared[3]) {
			out = append(out, Monitor{m.L, sp.A, min(m.L+left, m.R), sp.B})
		}
	}
	return out
}

func renderEdges(c *Canvas, ox, oy int32, m Monitor, s SafetyScene, bw int) {
	for _, r := range edgeBands(m, s) {
		if r.R <= r.L || r.B <= r.T {
			continue
		}
		x0, y0, x1, y1 := int(r.L-ox), int(r.T-oy), int(r.R-ox), int(r.B-oy)
		c.FillRect(x0, y0, x1, y1, colEdge.Fade(0.30))
		c.StrokeRect(x0, y0, x1, y1, min(bw, x1-x0, y1-y0), colEdge.Fade(0.80))
	}
}

// zoneColor is the zone's colour and whether its border is dashed.
func zoneColor(action uint8, on bool) (Color, bool) {
	if !on {
		return colOff, true
	}
	switch action {
	case ZonePause:
		return colPause, false
	case ZoneStart:
		return colStart, false
	default:
		return colStop, true
	}
}

// renderZone draws one zone with its 1-based index (z.Index + 1) as a big
// number in the middle, sized to ~65 % of the shorter side, at most 160 dip.
func renderZone(c *Canvas, ox, oy int32, z SceneZone, on bool, scale float64) {
	if z.R <= z.L || z.B <= z.T {
		return
	}
	col, dashed := zoneColor(z.Action, on)
	x0, y0, x1, y1 := int(z.L-ox), int(z.T-oy), int(z.R-ox), int(z.B-oy)
	c.FillRect(x0, y0, x1, y1, col.Fade(0.18))
	bw := min(px(2, scale), x1-x0, y1-y0)
	if dashed {
		c.DashedRect(x0, y0, x1, y1, bw, px(8, scale), px(6, scale), col.Fade(0.90))
	} else {
		c.StrokeRect(x0, y0, x1, y1, bw, col.Fade(0.90))
	}
	h := int(float64(min(x1-x0, y1-y0)) * 0.65)
	h = min(h, px(160, scale))
	if h < px(10, scale) {
		return // too small to label legibly
	}
	n := z.Index + 1
	// Keep wide numbers inside the zone.
	if w := numberSize(n, h); w > (x1-x0)*9/10 {
		h = h * (x1 - x0) * 9 / 10 / w
	}
	c.DrawNumber((x0+x1)/2, (y0+y1)/2, h, n, col.Fade(0.55))
}

// pointColor is the point's stable colour: hsl(hash(id) mod 360, 84 %, 58 %).
func pointColor(id string) Color {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return hsl(float64(h.Sum32()%360), 0.84, 0.58)
}

func hsl(h, s, l float64) Color {
	c := (1 - math.Abs(2*l-1)) * s
	hp := h / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g = c, x
	case hp < 2:
		r, g = x, c
	case hp < 3:
		g, b = c, x
	case hp < 4:
		g, b = x, c
	case hp < 5:
		r, b = x, c
	default:
		r, b = c, x
	}
	m := l - c/2
	to := func(v float64) uint8 { return uint8(clampF((v+m)*255+0.5, 0, 255)) }
	return Color{to(r), to(g), to(b), 255}
}

// renderPoints draws click points: a dashed ring for the scatter radius, a
// 10 dip dot (shadow, white rim, colour fill) and a small 1-based index badge.
func renderPoints(c *Canvas, ox, oy int32, pts []ScenePoint, scale float64) {
	for _, p := range pts {
		col := pointColor(p.ID)
		x, y := float64(p.X-ox)+0.5, float64(p.Y-oy)+0.5
		if p.Radius > 0 {
			c.Ring(x, y, float64(p.Radius), 1.5*scale, 5*scale, col.Fade(0.55))
		}
		c.FillDisc(x, y, 6.5*scale, colShadow)
		c.FillDisc(x, y, 5*scale, colWhite)
		c.FillDisc(x, y, 3*scale, col)
		if p.Index >= 0 {
			n := p.Index + 1
			h := px(9, scale)
			pad := px(3, scale)
			w := numberSize(n, h) + 2*pad
			bx := int(x) + px(8, scale)
			by := int(y) - px(8, scale) - h - 2*pad
			c.FillRoundRect(bx, by, bx+w, by+h+2*pad, float64(pad), colCard)
			c.DrawNumber(bx+w/2, by+pad+h/2, h, n, colText)
		}
	}
}

// renderDraft paints the zone being dragged: green 14 % fill, dashed border.
func renderDraft(c *Canvas, scale float64) {
	c.Clear(colStart.Fade(0.14))
	bw := min(px(2, scale), c.W, c.H)
	c.DashedRect(0, 0, c.W, c.H, bw, px(8, scale), px(6, scale), colStart.Fade(0.95))
}

// crosshairThick is the crosshair line's width: dark, light, dark.
func crosshairThick(scale float64) int { return 3 * px(1, scale) }

// renderCrosshair paints one crosshair line along the canvas' long axis: a
// light core between two dark rims, so it shows on any background.
func renderCrosshair(c *Canvas, scale float64) {
	t := px(1, scale)
	dark, light := Color{0, 0, 0, 110}, Color{255, 255, 255, 190}
	if c.W >= c.H { // horizontal
		c.FillRect(0, 0, c.W, t, dark)
		c.FillRect(0, t, c.W, 2*t, light)
		c.FillRect(0, 2*t, c.W, 3*t, dark)
		return
	}
	c.FillRect(0, 0, t, c.H, dark)
	c.FillRect(t, 0, 2*t, c.H, light)
	c.FillRect(2*t, 0, 3*t, c.H, dark)
}

// Card geometry (dip) shared by the hint bubble and the running pill.
const (
	bubblePadX   = 12
	bubblePadY   = 8
	bubbleRadius = 8
	bubbleOffset = 18 // from the cursor, right and down
	bubbleMargin = 8  // kept inside the monitor
	pillPadX     = 14
	pillHeight   = 30
	pillDot      = 8
	pillGap      = 8
	pillTop      = 12 // below the work area's top edge
)

// bubbleSize is the hint card size for a text mask.
func bubbleSize(text *Mask, scale float64) (w, h int) {
	return text.W + 2*px(bubblePadX, scale), text.H + 2*px(bubblePadY, scale)
}

// renderBubble paints the dark hint card with its text.
func renderBubble(c *Canvas, text *Mask, scale float64) {
	c.Clear(Color{})
	r := float64(px(bubbleRadius, scale))
	c.FillRoundRect(0, 0, c.W, c.H, r, colCardRim)
	c.FillRoundRect(1, 1, c.W-1, c.H-1, r-1, colCard)
	c.DrawMask(px(bubblePadX, scale), px(bubblePadY, scale), text, colText)
}

// bubblePos places the hint card bubbleOffset dip right of and below the
// cursor, flipped to the other side when it would leave the monitor, and
// clamped bubbleMargin dip inside it.
func bubblePos(cx, cy int32, w, h int, mon Monitor, scale float64) (x, y int32) {
	off, mar := int32(px(bubbleOffset, scale)), int32(px(bubbleMargin, scale))
	x, y = cx+off, cy+off
	if x+int32(w) > mon.R-mar {
		x = cx - off - int32(w)
	}
	if y+int32(h) > mon.B-mar {
		y = cy - off - int32(h)
	}
	x = max(min(x, mon.R-mar-int32(w)), mon.L+mar)
	y = max(min(y, mon.B-mar-int32(h)), mon.T+mar)
	return x, y
}

// pillSize is the running indicator size for a text mask.
func pillSize(text *Mask, scale float64) (w, h int) {
	h = max(px(pillHeight, scale), text.H+2*px(4, scale))
	return text.W + 2*px(pillPadX, scale) + px(pillDot, scale) + px(pillGap, scale), h
}

// renderPill paints the running indicator: a dark capsule with a blue dot and
// the status text.
func renderPill(c *Canvas, text *Mask, scale float64) {
	c.Clear(Color{})
	r := float64(c.H) / 2
	c.FillRoundRect(0, 0, c.W, c.H, r, colCardRim)
	c.FillRoundRect(1, 1, c.W-1, c.H-1, r-1, colCard)
	dot := float64(px(pillDot, scale))
	dx := float64(px(pillPadX, scale)) + dot/2
	c.FillDisc(dx, float64(c.H)/2, dot/2, colAccent)
	tx := px(pillPadX, scale) + px(pillDot, scale) + px(pillGap, scale)
	c.DrawMask(tx, (c.H-text.H)/2, text, colText)
}

// pillPos centres the pill horizontally near the top of a work area.
func pillPos(work Monitor, w int, scale float64) (x, y int32) {
	return work.L + (work.R-work.L-int32(w))/2, work.T + int32(px(pillTop, scale))
}

// fade is an opacity timeline: fade in over In, hold until Hold, fade out so
// it reaches 0 at End.
type fade struct{ In, Hold, End time.Duration }

var (
	// The safety preview fades in, holds, and is gone by 3 s.
	fadeSafety = fade{In: 250 * time.Millisecond, Hold: 1500 * time.Millisecond, End: 3 * time.Second}
	// The points preview holds for 70 % of 5 s, then fades.
	fadePoints = fade{In: 0, Hold: 3500 * time.Millisecond, End: 5 * time.Second}
)

// at returns the opacity (0..255) t after the start, and whether it is over.
func (f fade) at(t time.Duration) (alpha uint8, done bool) {
	switch {
	case t >= f.End:
		return 0, true
	case t < f.In:
		return uint8(255 * float64(t) / float64(f.In)), false
	case t < f.Hold:
		return 255, false
	default:
		left := float64(f.End-t) / float64(f.End-f.Hold)
		return uint8(255*left + 0.5), false
	}
}

// rectFromDrag turns a drag between two pixels into a zone rect. Both ends are
// inside the zone, so it is inclusive: R and B are one past the larger end.
func rectFromDrag(ax, ay, bx, by int32) (l, t, r, b int32) {
	return min(ax, bx), min(ay, by), max(ax, bx) + 1, max(ay, by) + 1
}

// monitorAt returns the monitor containing (x, y), or the nearest one.
func monitorAt(mons []Monitor, x, y int32) (Monitor, bool) {
	if len(mons) == 0 {
		return Monitor{}, false
	}
	best, bestD := mons[0], int64(math.MaxInt64)
	for _, m := range mons {
		dx := int64(max(m.L-x, 0, x-(m.R-1)))
		dy := int64(max(m.T-y, 0, y-(m.B-1)))
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = m, d
		}
	}
	return best, true
}

// intersects reports whether a and b overlap.
func intersects(a, b Monitor) bool {
	return a.L < b.R && b.L < a.R && a.T < b.B && b.T < a.B
}
