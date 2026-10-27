package safety

// Failsafe geometry. Every rule is half-open, like Rect itself: a corner of size
// N is exactly the N x N square at that monitor corner, an edge of N is exactly
// an N px band along that side, and N = 0 is an empty area (that corner or edge
// is off). Rules apply to every monitor the cursor is on, so the corners and
// edges of inner monitors count too, except for edge segments OuterOnly skips.

// Codes the guard reports (clicker.StopCode values; strings here to avoid an
// import cycle, like engine's).
const (
	codeCorner       = "corner"
	codeEdge         = "edge"
	codeInZone       = "zoneStop"
	codeZoneLeft     = "zoneLeft"
	codeTaskSwitcher = "taskSwitcher"
	codeProcessList  = "processList"
	codeHoldRelease  = "holdRelease"
)

func (r Rect) contains(x, y int32) bool {
	return x >= r.L && x < r.R && y >= r.T && y < r.B
}

// clampSize keeps a corner/edge size inside [0, span] so huge values cover the
// monitor instead of wrapping around int32.
func clampSize(n, span int32) int32 {
	if n <= 0 {
		return 0
	}
	if n > span {
		return span
	}
	return n
}

// cornerHit returns the reason for the first corner (TL, TR, BL, BR) of any
// monitor that holds (x, y), or "".
func cornerHit(c Corners, mons []Rect, x, y int32) string {
	if !c.Enabled {
		return ""
	}
	for _, m := range mons {
		if !m.contains(x, y) {
			continue
		}
		w, h := m.R-m.L, m.B-m.T
		inTop := func(n int32) bool { return y < m.T+clampSize(n, h) }
		inBottom := func(n int32) bool { return y >= m.B-clampSize(n, h) }
		inLeft := func(n int32) bool { return x < m.L+clampSize(n, w) }
		inRight := func(n int32) bool { return x >= m.R-clampSize(n, w) }
		switch {
		case inTop(c.TL) && inLeft(c.TL):
			return "Top-left corner failsafe"
		case inTop(c.TR) && inRight(c.TR):
			return "Top-right corner failsafe"
		case inBottom(c.BL) && inLeft(c.BL):
			return "Bottom-left corner failsafe"
		case inBottom(c.BR) && inRight(c.BR):
			return "Bottom-right corner failsafe"
		}
	}
	return ""
}

// edgeHit returns the reason for the first edge band (top, right, bottom, left)
// of any monitor that holds (x, y), or "". With OuterOnly an edge counts only
// where the pixel just across it belongs to no other monitor, so only the part
// of an edge actually shared with a neighbour is skipped (monitors of different
// sizes or offsets share only part of an edge).
func edgeHit(e Edges, mons []Rect, x, y int32) string {
	if !e.Enabled {
		return ""
	}
	for i, m := range mons {
		if !m.contains(x, y) {
			continue
		}
		w, h := m.R-m.L, m.B-m.T
		outer := func(ax, ay int32) bool { return !e.OuterOnly || !onOtherMonitor(mons, i, ax, ay) }
		switch {
		case y < m.T+clampSize(e.Top, h) && outer(x, m.T-1):
			return "Top edge failsafe"
		case x >= m.R-clampSize(e.Right, w) && outer(m.R, y):
			return "Right edge failsafe"
		case y >= m.B-clampSize(e.Bottom, h) && outer(x, m.B):
			return "Bottom edge failsafe"
		case x < m.L+clampSize(e.Left, w) && outer(m.L-1, y):
			return "Left edge failsafe"
		}
	}
	return ""
}

// onOtherMonitor reports whether (x, y) is on any monitor but mons[self].
func onOtherMonitor(mons []Rect, self int, x, y int32) bool {
	for j, o := range mons {
		if j != self && o.contains(x, y) {
			return true
		}
	}
	return false
}

// PointInFailsafe reports whether (x, y) falls in a corner or edge band, for the
// click-point warnings in the UI.
func PointInFailsafe(c Corners, e Edges, mons []Rect, x, y int32) bool {
	return cornerHit(c, mons, x, y) != "" || edgeHit(e, mons, x, y) != ""
}

// ZoneAt returns the winning zone action under (x, y) (stop > start > pause)
// and its index into zones, or (0, -1). Among zones with the same action the
// first in the list wins.
func ZoneAt(zones []Zone, x, y int32) (action uint8, idx int) {
	start, pause := -1, -1
	for i, z := range zones {
		if !z.Rect.contains(x, y) {
			continue
		}
		switch z.Action {
		case ZoneStop:
			return ZoneStop, i
		case ZoneStart:
			if start < 0 {
				start = i
			}
		case ZonePause:
			if pause < 0 {
				pause = i
			}
		}
	}
	if start >= 0 {
		return ZoneStart, start
	}
	if pause >= 0 {
		return ZonePause, pause
	}
	return 0, -1
}
