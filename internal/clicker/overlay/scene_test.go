package overlay

import (
	"testing"
	"time"
)

func isRed(c Color) bool  { return c.A > 0 && c.R > 200 && c.G < 100 && c.B < 100 }
func isCyan(c Color) bool { return c.A > 0 && c.R < 100 && c.G > 180 && c.B > 200 }
func isAmber(c Color) bool {
	return c.A > 0 && c.R > 220 && c.G > 130 && c.G < 180 && c.B < 40
}
func isBlue(c Color) bool  { return c.A > 0 && c.B > 200 && c.R < 100 && c.G < 160 }
func isGreen(c Color) bool { return c.A > 0 && c.G > 170 && c.R < 60 && c.B < 120 }
func isGrey(c Color) bool {
	return c.A > 0 && near(int(c.R), int(c.G), 25) && near(int(c.G), int(c.B), 25) && c.R > 120
}

var mon1080 = Monitor{0, 0, 1920, 1080}

func baseScene() SafetyScene {
	return SafetyScene{
		Monitors:  []Monitor{mon1080},
		Corners:   [4]int32{50, 50, 50, 50},
		CornersOn: true,
		Edges:     [4]int32{40, 40, 40, 40},
		EdgesOn:   true,
	}
}

func TestSafetyCornersAndEdges(t *testing.T) {
	c := NewCanvas(1920, 1080)
	renderSafety(c, 0, 0, baseScene(), 1)

	for _, p := range [][2]int{{25, 25}, {1894, 25}, {25, 1054}, {1894, 1054}} {
		if got := c.At(p[0], p[1]); !isRed(got) {
			t.Errorf("corner square at %v: want red, got %+v", p, got)
		}
	}
	// Corner fill is 40 % alpha, its border 90 %.
	if a := c.At(25, 25).A; !near(int(a), 102, 3) {
		t.Errorf("corner fill alpha %d, want ~102", a)
	}
	if got := c.At(0, 25); !isRed(got) || got.A < 220 {
		t.Errorf("corner border: %+v", got)
	}
	for _, p := range [][2]int{{960, 20}, {1900, 540}, {960, 1060}, {20, 540}} {
		if got := c.At(p[0], p[1]); !isCyan(got) {
			t.Errorf("edge band at %v: want cyan, got %+v", p, got)
		}
	}
	// Bands stop at the corner squares: the strip just inside a corner is red.
	if got := c.At(48, 20); !isRed(got) {
		t.Errorf("band overlaps the corner: %+v", got)
	}
	if got := c.At(60, 20); !isCyan(got) {
		t.Errorf("band should start right after the corner: %+v", got)
	}
	if got := c.At(960, 540); got.A != 0 {
		t.Errorf("monitor interior should be clear: %+v", got)
	}
}

func TestSafetyOffMeansOff(t *testing.T) {
	s := baseScene()
	s.CornersOn, s.EdgesOn = false, false
	c := NewCanvas(1920, 1080)
	renderSafety(c, 0, 0, s, 1)
	if got := c.At(25, 25); got.A != 0 {
		t.Errorf("corners off still drew: %+v", got)
	}
	// Only the outline remains: thin white at 28 %.
	if got := c.At(960, 0); got.R != 255 || got.G != 255 || !near(int(got.A), 72, 2) {
		t.Errorf("monitor outline: %+v", got)
	}

	s = baseScene()
	s.Corners = [4]int32{0, 50, 50, 50}
	s.Edges = [4]int32{0, 40, 40, 40}
	c = NewCanvas(1920, 1080)
	renderSafety(c, 0, 0, s, 1)
	if got := c.At(10, 10); isRed(got) {
		t.Errorf("size 0 corner must be off, got %+v", got)
	}
	// With the TL corner off, the left band runs right up to the top.
	if got := c.At(10, 5); !isCyan(got) {
		t.Errorf("left band should reach the top: %+v", got)
	}
	if got := c.At(960, 10); isCyan(got) {
		t.Errorf("top edge 0 still drew: %+v", got)
	}
}

func TestSafetyNegativeOrigin(t *testing.T) {
	left := Monitor{-1920, -200, 0, 880}
	s := baseScene()
	s.Monitors = []Monitor{left, mon1080}
	c := NewCanvas(1920, 1080)
	renderSafety(c, left.L, left.T, s, 1)
	if got := c.At(10, 10); !isRed(got) {
		t.Errorf("TL corner of a negative-origin monitor: %+v", got)
	}
	if got := c.At(1919-10, 1079-10); !isRed(got) {
		t.Errorf("BR corner of a negative-origin monitor: %+v", got)
	}
}

func TestSafetyOuterOnly(t *testing.T) {
	right := Monitor{1920, 0, 3840, 1080}
	s := baseScene()
	s.Monitors = []Monitor{mon1080, right}
	s.OuterOnly = true
	c := NewCanvas(3840, 1080)
	renderSafety(c, 0, 0, s, 1)
	if got := c.At(1900, 540); isCyan(got) {
		t.Errorf("shared right edge drawn with OuterOnly: %+v", got)
	}
	if got := c.At(1940, 540); isCyan(got) {
		t.Errorf("shared left edge of the second monitor drawn: %+v", got)
	}
	if got := c.At(3820, 540); !isCyan(got) {
		t.Errorf("outer right edge missing: %+v", got)
	}
	if got := c.At(1800, 20); !isCyan(got) {
		t.Errorf("top edge must stay: %+v", got)
	}

	s.OuterOnly = false
	c = NewCanvas(3840, 1080)
	renderSafety(c, 0, 0, s, 1)
	if got := c.At(1900, 540); !isCyan(got) {
		t.Errorf("inner edge missing without OuterOnly: %+v", got)
	}
}

func TestEdgeSpansPartialShare(t *testing.T) {
	// A smaller monitor below covers part of the bottom edge.
	m := Monitor{0, 0, 1920, 1080}
	below := Monitor{500, 1080, 1500, 1800}
	sh := sharedEdges(m, []Monitor{m, below})
	got := edgeSpans(0, 1920, sh[2])
	want := []span{{0, 500}, {1500, 1920}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("spans %v, want %v", got, want)
	}
	if len(sh[0]) != 0 || len(sh[1]) != 0 || len(sh[3]) != 0 {
		t.Fatalf("unexpected shares %v", sh)
	}
}

func zoneScene(action uint8, on bool) SafetyScene {
	s := baseScene()
	s.CornersOn, s.EdgesOn = false, false
	s.ZonesOn = on
	s.Zones = []SceneZone{{L: 300, T: 300, R: 700, B: 600, Action: action, Index: 0}}
	return s
}

func TestZoneColours(t *testing.T) {
	cases := []struct {
		name   string
		action uint8
		on     bool
		is     func(Color) bool
		dashed bool
	}{
		{"stop", ZoneStop, true, isAmber, true},
		{"pause", ZonePause, true, isBlue, false},
		{"start", ZoneStart, true, isGreen, false},
		{"off", ZoneStart, false, isGrey, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewCanvas(1920, 1080)
			renderSafety(c, 0, 0, zoneScene(tc.action, tc.on), 1)
			if got := c.At(303, 300); !tc.is(got) || got.A < 200 {
				t.Errorf("border: %+v", got)
			}
			fill := c.At(330, 450)
			if !tc.is(fill) || !near(int(fill.A), 46, 3) {
				t.Errorf("fill (18 %%): %+v", fill)
			}
			// 8 px dash, 6 px gap: x = 300+10 is in the first gap.
			gap := c.At(310, 300)
			if tc.dashed && gap.A > 100 {
				t.Errorf("dashed border has no gap: %+v", gap)
			}
			if !tc.dashed && gap.A < 200 {
				t.Errorf("solid border has a gap: %+v", gap)
			}
		})
	}
}

func TestZoneBigIndex(t *testing.T) {
	c := NewCanvas(1920, 1080)
	s := zoneScene(ZoneStart, true)
	renderSafety(c, 0, 0, s, 1)
	// The zone is 400x300: the number is 65 % of 300 = 195, capped at 160.
	h := 160
	cx, cy := 500, 450
	dw, th := digitW(h), max(h/7, 1)
	seg := c.At(cx-dw/2+dw-th/2-1, cy-h/4) // segment b of "1"
	fill := c.At(cx-dw/2+th/2, cy-h/4)     // segment f: unlit for "1"
	if !isGreen(seg) || seg.A <= fill.A+60 {
		t.Errorf("index 1 not drawn big and green: seg %+v fill %+v", seg, fill)
	}

	// Index is 0-based in the scene and shown 1-based: Index 1 draws "2",
	// whose segment f is unlit but e is lit.
	s.Zones[0].Index = 1
	c2 := NewCanvas(1920, 1080)
	renderSafety(c2, 0, 0, s, 1)
	if e := c2.At(cx-dw/2+th/2, cy+h/4); e.A <= fill.A+60 {
		t.Errorf("index 2 segment e missing: %+v", e)
	}
}

func TestTinyZoneHasNoIndex(t *testing.T) {
	s := baseScene()
	s.CornersOn, s.EdgesOn, s.ZonesOn = false, false, true
	s.Zones = []SceneZone{{L: 100, T: 100, R: 110, B: 110, Action: ZonePause}}
	c := NewCanvas(400, 400)
	renderSafety(c, 0, 0, s, 1)
	if got := c.At(105, 105); !isBlue(got) {
		t.Errorf("tiny zone not drawn: %+v", got)
	}
}

func TestPointsScene(t *testing.T) {
	pts := []ScenePoint{
		{ID: "a", X: 200, Y: 200, Radius: 40, Index: 0},
		{ID: "b", X: 600, Y: 200, Index: 4},
	}
	c := NewCanvas(800, 400)
	renderPoints(c, 0, 0, pts, 1)
	if got := c.At(200, 200); got != pointColor("a") {
		t.Errorf("dot centre %+v, want %+v", got, pointColor("a"))
	}
	if got := c.At(204, 200); got.R < 240 || got.G < 240 || got.B < 240 {
		t.Errorf("dot rim should be white: %+v", got)
	}
	ringPx := 0
	for x := 150; x < 250; x++ {
		for y := 150; y < 250; y++ {
			dx, dy := x-200, y-200
			if d2 := dx*dx + dy*dy; d2 > 38*38 && d2 < 42*42 && c.At(x, y).A > 0 {
				ringPx++
			}
		}
	}
	if ringPx < 50 {
		t.Errorf("scatter ring barely drawn: %d px", ringPx)
	}
	if got := c.At(600+20, 200+20); got.A != 0 {
		t.Errorf("radius 0 drew a ring: %+v", got)
	}
	// The index badge sits up and right of the dot.
	badge := 0
	for x := 608; x < 640; x++ {
		for y := 170; y < 192; y++ {
			if c.At(x, y).A > 200 {
				badge++
			}
		}
	}
	if badge == 0 {
		t.Error("index badge missing")
	}
}

func TestPointColourStable(t *testing.T) {
	id := "p-1"
	if a, b := pointColor(id), pointColor("p-"+"1"); a != b {
		t.Fatal("not stable")
	}
	if pointColor("p-1") == pointColor("p-2") {
		t.Fatal("different ids should usually differ")
	}
	if got := hsl(0, 1, 0.5); got != (Color{255, 0, 0, 255}) {
		t.Errorf("hsl red: %+v", got)
	}
	if got := hsl(120, 1, 0.5); got != (Color{0, 255, 0, 255}) {
		t.Errorf("hsl green: %+v", got)
	}
}

func TestDraftAndCrosshair(t *testing.T) {
	c := NewCanvas(120, 80)
	renderDraft(c, 1)
	if got := c.At(0, 0); !isGreen(got) || got.A < 230 {
		t.Errorf("draft border: %+v", got)
	}
	if got := c.At(60, 40); !isGreen(got) || !near(int(got.A), 36, 2) {
		t.Errorf("draft fill (14 %%): %+v", got)
	}

	h := NewCanvas(300, crosshairThick(1))
	renderCrosshair(h, 1)
	if h.At(10, 0).R > 10 || h.At(10, 1).R < 250 || h.At(10, 2).R > 10 {
		t.Errorf("crosshair should be dark/light/dark: %+v %+v %+v", h.At(10, 0), h.At(10, 1), h.At(10, 2))
	}
	v := NewCanvas(crosshairThick(2), 300)
	renderCrosshair(v, 2)
	if v.At(2, 50).R < 250 || v.At(0, 50).R > 10 {
		t.Errorf("vertical crosshair at 200 %%: %+v %+v", v.At(0, 50), v.At(2, 50))
	}
}

func TestBubbleAndPill(t *testing.T) {
	text := &Mask{W: 40, H: 10, A: make([]uint8, 400)}
	for i := range text.A {
		text.A[i] = 255
	}
	w, h := bubbleSize(text, 1)
	if w != 40+2*bubblePadX || h != 10+2*bubblePadY {
		t.Fatalf("bubble size %dx%d", w, h)
	}
	c := NewCanvas(w, h)
	renderBubble(c, text, 1)
	if got := c.At(bubblePadX+5, bubblePadY+5); got.R < 240 {
		t.Errorf("bubble text: %+v", got)
	}
	if got := c.At(4, h/2); got.R > 40 || got.A < 220 {
		t.Errorf("bubble card: %+v", got)
	}
	if got := c.At(0, 0); got.A > 40 {
		t.Errorf("bubble corner should be rounded: %+v", got)
	}

	pw, ph := pillSize(text, 1)
	p := NewCanvas(pw, ph)
	renderPill(p, text, 1)
	if got := p.At(pillPadX+pillDot/2, ph/2); got.B < 200 || got.R > 120 {
		t.Errorf("pill dot should be Toru blue: %+v", got)
	}
	x, y := pillPos(Monitor{0, 0, 1920, 1040}, pw, 1)
	if x != (1920-int32(pw))/2 || y != pillTop {
		t.Errorf("pill at %d,%d", x, y)
	}
}

func TestBubblePosStaysOnMonitor(t *testing.T) {
	m := Monitor{0, 0, 1920, 1080}
	x, y := bubblePos(100, 100, 300, 40, m, 1)
	if x != 118 || y != 118 {
		t.Errorf("default offset: %d,%d", x, y)
	}
	x, y = bubblePos(1900, 1070, 300, 40, m, 1)
	if x+300 > 1920-bubbleMargin || y+40 > 1080-bubbleMargin || x < 0 || y < 0 {
		t.Errorf("bubble left the monitor: %d,%d", x, y)
	}
	if x >= 1900 || y >= 1070 {
		t.Errorf("bubble should flip to the other side of the cursor: %d,%d", x, y)
	}
}

func TestFadeTimelines(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		f    fade
		t    time.Duration
		want int
		done bool
	}{
		{fadeSafety, 0, 0, false},
		{fadeSafety, 125 * ms, 127, false},
		{fadeSafety, 250 * ms, 255, false},
		{fadeSafety, 1400 * ms, 255, false},
		{fadeSafety, 2250 * ms, 128, false},
		{fadeSafety, 3000 * ms, 0, true},
		{fadePoints, 0, 255, false},
		{fadePoints, 3500 * ms, 255, false},
		{fadePoints, 4250 * ms, 128, false},
		{fadePoints, 5000 * ms, 0, true},
	}
	for _, tc := range cases {
		a, done := tc.f.at(tc.t)
		if !near(int(a), tc.want, 2) || done != tc.done {
			t.Errorf("%v at %v: got %d,%v want %d,%v", tc.f, tc.t, a, done, tc.want, tc.done)
		}
	}
}

func TestRectFromDrag(t *testing.T) {
	l, tp, r, b := rectFromDrag(10, 10, 5, 20)
	if l != 5 || tp != 10 || r != 11 || b != 21 {
		t.Errorf("got %d,%d,%d,%d", l, tp, r, b)
	}
	// A click without moving is a 1x1 zone.
	l, tp, r, b = rectFromDrag(-7, -7, -7, -7)
	if r-l != 1 || b-tp != 1 {
		t.Errorf("1x1: %d,%d,%d,%d", l, tp, r, b)
	}
}

func TestMonitorAt(t *testing.T) {
	mons := []Monitor{{-1920, 0, 0, 1080}, mon1080}
	if m, _ := monitorAt(mons, -5, 500); m != mons[0] {
		t.Errorf("inside left: %+v", m)
	}
	if m, _ := monitorAt(mons, 0, 500); m != mon1080 {
		t.Errorf("x=0 belongs to the right monitor: %+v", m)
	}
	if m, _ := monitorAt(mons, 2500, -40); m != mon1080 {
		t.Errorf("nearest: %+v", m)
	}
	if _, ok := monitorAt(nil, 0, 0); ok {
		t.Error("no monitors should report !ok")
	}
}
