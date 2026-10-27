package safety

import "testing"

var (
	allCorners = func(n int32) Corners { return Corners{Enabled: true, TL: n, TR: n, BL: n, BR: n} }
	allEdges   = func(n int32, outer bool) Edges {
		return Edges{Enabled: true, Top: n, Right: n, Bottom: n, Left: n, OuterOnly: outer}
	}
)

func TestCornersSingleMonitor(t *testing.T) {
	mons := []Rect{{0, 0, 1920, 1080}}
	c := allCorners(50)
	cases := []struct {
		x, y int32
		want string
	}{
		{0, 0, "Top-left corner failsafe"},
		{49, 49, "Top-left corner failsafe"},
		{50, 0, ""}, // exactly N px: column 50 is outside
		{0, 50, ""},
		{1870, 0, "Top-right corner failsafe"},
		{1869, 0, ""},
		{1919, 49, "Top-right corner failsafe"},
		{0, 1030, "Bottom-left corner failsafe"},
		{0, 1029, ""},
		{1919, 1079, "Bottom-right corner failsafe"},
		{1870, 1030, "Bottom-right corner failsafe"},
		{960, 540, ""},
		{-1, 0, ""}, // off every monitor
		{1920, 0, ""},
	}
	for _, tc := range cases {
		if got := cornerHit(c, mons, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
}

func TestCornerZeroIsOff(t *testing.T) {
	mons := []Rect{{0, 0, 1920, 1080}}
	c := Corners{Enabled: true, TL: 0, TR: 10, BL: 0, BR: 0}
	for _, p := range [][2]int32{{0, 0}, {0, 1079}, {1919, 1079}} {
		if got := cornerHit(c, mons, p[0], p[1]); got != "" {
			t.Errorf("(%d,%d) = %q with that corner at 0", p[0], p[1], got)
		}
	}
	if got := cornerHit(c, mons, 1919, 0); got != "Top-right corner failsafe" {
		t.Errorf("TR = %q", got)
	}
	c.Enabled = false
	if got := cornerHit(c, mons, 1919, 0); got != "" {
		t.Errorf("disabled corners still hit: %q", got)
	}
}

func TestEdgesSingleMonitor(t *testing.T) {
	mons := []Rect{{0, 0, 1920, 1080}}
	e := allEdges(40, false)
	cases := []struct {
		x, y int32
		want string
	}{
		{960, 0, "Top edge failsafe"},
		{960, 39, "Top edge failsafe"},
		{960, 40, ""},
		{1880, 540, "Right edge failsafe"},
		{1879, 540, ""},
		{960, 1040, "Bottom edge failsafe"},
		{960, 1039, ""},
		{0, 540, "Left edge failsafe"},
		{39, 540, "Left edge failsafe"},
		{40, 540, ""},
		{0, 0, "Top edge failsafe"}, // top is checked before left
	}
	for _, tc := range cases {
		if got := edgeHit(e, mons, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
	zero := Edges{Enabled: true, Top: 0, Right: 40, Bottom: 0, Left: 0}
	for _, p := range [][2]int32{{960, 0}, {960, 1079}, {0, 540}} {
		if got := edgeHit(zero, mons, p[0], p[1]); got != "" {
			t.Errorf("(%d,%d) = %q with that edge at 0", p[0], p[1], got)
		}
	}
}

func TestSizeLargerThanMonitorCoversIt(t *testing.T) {
	mons := []Rect{{0, 0, 100, 100}}
	if got := cornerHit(Corners{Enabled: true, BR: 10_000}, mons, 0, 0); got != "Bottom-right corner failsafe" {
		t.Errorf("huge BR at (0,0) = %q", got)
	}
}

// Two monitors: a 2560x1440 primary and a 1920x1080 to its left with a negative
// origin, bottoms aligned.
//
//	(-1920,360)          (0,0)
//	   +-------+---------+
//	   |  L    |  P      |
//	   +-------+---------+ (2560,1440)
var twoMons = []Rect{
	{0, 0, 2560, 1440},
	{-1920, 360, 0, 1440},
}

func TestTwoMonitorsNegativeOrigin(t *testing.T) {
	c := allCorners(50)
	e := allEdges(40, false)
	cases := []struct {
		x, y   int32
		corner string
		edge   string
	}{
		{-1920, 360, "Top-left corner failsafe", "Top edge failsafe"},
		{-1, 360, "Top-right corner failsafe", "Top edge failsafe"},
		{-1, 900, "", "Right edge failsafe"}, // inner border, OuterOnly off
		{0, 900, "", "Left edge failsafe"},
		{0, 0, "Top-left corner failsafe", "Top edge failsafe"},
		{-960, 1439, "", "Bottom edge failsafe"},
		{-1000, 900, "", ""},
	}
	for _, tc := range cases {
		if got := cornerHit(c, twoMons, tc.x, tc.y); got != tc.corner {
			t.Errorf("corner (%d,%d) = %q, want %q", tc.x, tc.y, got, tc.corner)
		}
		if got := edgeHit(e, twoMons, tc.x, tc.y); got != tc.edge {
			t.Errorf("edge (%d,%d) = %q, want %q", tc.x, tc.y, got, tc.edge)
		}
	}
}

func TestOuterOnlyTwoMonitors(t *testing.T) {
	e := allEdges(40, true)
	cases := []struct {
		x, y int32
		want string
	}{
		{-1, 900, ""},                  // left monitor's right edge, shared
		{0, 900, ""},                   // primary's left edge next to the left monitor
		{0, 200, "Left edge failsafe"}, // primary's left edge above the left monitor: outer
		{0, 359, "Left edge failsafe"}, // last outer row
		{0, 360, ""},                   // first shared row
		{2559, 900, "Right edge failsafe"},
		{-1920, 900, "Left edge failsafe"},
		{-960, 360, "Top edge failsafe"}, // nothing above the left monitor
	}
	for _, tc := range cases {
		if got := edgeHit(e, twoMons, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
}

// Three monitors of mixed sizes: a 1920x1080 primary, a 1280x1024 above it
// offset to the right (shares only part of the primary's top edge) and a
// 1080x1920 portrait monitor to the left with a negative origin that starts
// above the primary.
//
//	                      (1280,-1024)
//	                         +--------+
//	(-1080,-600)             |  T     |
//	  +----+         (0,0)   |        |
//	  |    |   +-------------+--------+--+
//	  | V  |   |  P                      |
//	  |    |   +-------------------------+ (1920,1080)
//	  |    |
//	  +----+ (0,1320)
var threeMons = []Rect{
	{1280, -1024, 2560, 0},
	{-1080, -600, 0, 1320},
	{0, 0, 1920, 1080},
}

func TestOuterOnlyThreeMonitorsPartialOverlap(t *testing.T) {
	e := allEdges(40, true)
	cases := []struct {
		x, y int32
		want string
	}{
		// Primary top edge: only x in [1280, 1920) is shared with T.
		{100, 0, "Top edge failsafe"},
		{1279, 10, "Top edge failsafe"},
		{1280, 10, ""},
		{1800, 39, ""},
		{1919, 39, "Right edge failsafe"}, // P's right edge is outer there
		// T's bottom edge: x in [1280, 1920) sits on P, [1920, 2560) is outer.
		{1500, -1, ""},
		{1919, -1, ""},
		{1920, -1, "Bottom edge failsafe"},
		{2559, -1, "Right edge failsafe"}, // right beats bottom in check order
		// V's right edge: y in [0, 1080) touches P, the rest is outer.
		{-1, -1, "Right edge failsafe"},
		{-1, 0, ""},
		{-1, 1079, ""},
		{-1, 1080, "Right edge failsafe"},
		// P's left edge is fully shared with V.
		{0, 500, ""},
		// Interior.
		{-500, 500, ""},
	}
	for _, tc := range cases {
		if got := edgeHit(e, threeMons, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
	// Without OuterOnly every one of those borders trips.
	e.OuterOnly = false
	for _, p := range [][2]int32{{1280, 10}, {1500, -1}, {-1, 500}, {0, 500}} {
		if got := edgeHit(e, threeMons, p[0], p[1]); got == "" {
			t.Errorf("(%d,%d) did not trip with OuterOnly off", p[0], p[1])
		}
	}
}

func TestCornersThreeMonitors(t *testing.T) {
	c := allCorners(30)
	cases := []struct {
		x, y int32
		want string
	}{
		{1280, -1024, "Top-left corner failsafe"},
		{2559, -1, "Bottom-right corner failsafe"},
		{-1080, 1319, "Bottom-left corner failsafe"},
		{-1, -600, "Top-right corner failsafe"},
		{0, 0, "Top-left corner failsafe"}, // inner corners count (OuterOnly is edges only)
		{1919, 1079, "Bottom-right corner failsafe"},
		{1300, -500, ""},
	}
	for _, tc := range cases {
		if got := cornerHit(c, threeMons, tc.x, tc.y); got != tc.want {
			t.Errorf("(%d,%d) = %q, want %q", tc.x, tc.y, got, tc.want)
		}
	}
}

func TestPointInFailsafe(t *testing.T) {
	c, e := allCorners(50), allEdges(40, true)
	if !PointInFailsafe(c, e, twoMons, 2559, 1439) {
		t.Error("corner point not flagged")
	}
	if !PointInFailsafe(c, e, twoMons, 1000, 2) {
		t.Error("edge point not flagged")
	}
	if PointInFailsafe(c, e, twoMons, -1, 900) {
		t.Error("shared border flagged with OuterOnly")
	}
	if PointInFailsafe(c, e, twoMons, 1000, 700) {
		t.Error("centre flagged")
	}
	if PointInFailsafe(Corners{}, Edges{}, twoMons, 0, 0) {
		t.Error("flagged with everything off")
	}
}

func TestZoneAtPriority(t *testing.T) {
	zones := []Zone{
		{Rect: Rect{0, 0, 100, 100}, Action: ZonePause, Index: 0},
		{Rect: Rect{50, 50, 150, 150}, Action: ZoneStart, Index: 1},
		{Rect: Rect{90, 90, 120, 120}, Action: ZoneStop, Index: 2},
		{Rect: Rect{0, 0, 30, 30}, Action: ZonePause, Index: 3},
		{Rect: Rect{-200, -200, -100, -100}, Action: ZoneStop, Index: 4},
	}
	cases := []struct {
		x, y   int32
		action uint8
		idx    int
	}{
		{10, 10, ZonePause, 0}, // two pauses: first wins
		{60, 60, ZoneStart, 1}, // start beats pause
		{95, 95, ZoneStop, 2},  // stop beats start and pause
		{140, 140, ZoneStart, 1},
		{100, 10, 0, -1}, // R is exclusive
		{-150, -150, ZoneStop, 4},
		{500, 500, 0, -1},
	}
	for _, tc := range cases {
		a, i := ZoneAt(zones, tc.x, tc.y)
		if a != tc.action || i != tc.idx {
			t.Errorf("(%d,%d) = (%d,%d), want (%d,%d)", tc.x, tc.y, a, i, tc.action, tc.idx)
		}
	}
	if a, i := ZoneAt(nil, 0, 0); a != 0 || i != -1 {
		t.Errorf("no zones = (%d,%d)", a, i)
	}
}
