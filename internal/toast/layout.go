package toast

// Card geometry, in dip. Toru's look is sharp-edged (frontend index.css sets
// --radius to 0 everywhere), so cards are plain rectangles: the card color, a
// 1px border, a kind-colored stripe and icon on the left.
const (
	cardWidthDip  = 340
	cardGapDip    = 8
	cardMarginDip = 20 // from the work area's edges

	stripeDip    = 3
	iconLeftDip  = 15
	iconSizeDip  = 15
	textLeftDip  = 40
	textRightDip = 14
	padTopDip    = 11
	padBottomDip = 12
	titleDip     = 21   // title row height (Segoe UI Semibold 14)
	titleSizeDip = 14   // title font size
	bodyGapDip   = 1    // between title and body
	bodyLineDip  = 17   // body line height (Segoe UI 12.5)
	bodySizeDip  = 12.5 // body font size
	maxBodyLines = 2

	closeSizeDip  = 24 // the x's hit box, in the top-right corner
	closeInsetDip = 5
	titleRightDip = closeSizeDip + closeInsetDip + 4 // title keeps clear of the x

	buttonGapDip    = 10 // above the button row
	buttonDip       = 26 // button height
	buttonPadDip    = 12 // text inset on each side
	buttonMinDip    = 64
	buttonSpaceDip  = 8 // between buttons
	buttonSizeDip   = 12.5
	buttonsRightDip = textRightDip
)

// cardHeightDip is the height of a card with bodyLines lines of body text and,
// when actions, a button row. A title-only card is 44 dip; a one-line body
// makes it 62, the size Toru's toast always was.
func cardHeightDip(bodyLines int, actions bool) float64 {
	h := float64(padTopDip + titleDip + padBottomDip)
	if bodyLines > maxBodyLines {
		bodyLines = maxBodyLines
	}
	if bodyLines > 0 {
		h += bodyGapDip + float64(bodyLines*bodyLineDip)
	}
	if actions {
		h += buttonGapDip + buttonDip
	}
	return h
}

// rect is a pixel rectangle, right/bottom exclusive.
type rect struct{ L, T, R, B int }

func (r rect) has(x, y int) bool { return x >= r.L && x < r.R && y >= r.T && y < r.B }

// point is a pixel position.
type point struct{ X, Y int }

// px converts dip to pixels at scale.
func px(dip, scale float64) int { return int(dip*scale + 0.5) }

// stackOrigins places cards of width w and the given heights (oldest first)
// in corner of the work area, newest nearest the corner, and returns each
// card's top-left in the same order.
func stackOrigins(work rect, corner string, w int, heights []int, gap, margin int) []point {
	out := make([]point, len(heights))
	x := work.R - margin - w
	if corner == CornerTopLeft || corner == CornerBottomLeft {
		x = work.L + margin
	}
	top := corner == CornerTopRight || corner == CornerTopLeft
	y := work.T + margin
	if !top {
		y = work.B - margin
	}
	for i := len(heights) - 1; i >= 0; i-- {
		h := heights[i]
		if top {
			out[i] = point{x, y}
			y += h + gap
		} else {
			y -= h
			out[i] = point{x, y}
			y -= gap
		}
	}
	return out
}

// part is what a click lands on: a button index (0, 1), or partBody/partClose.
type part int

const (
	partNone  part = -3
	partClose part = -2
	partBody  part = -1
)

// closeRect is the x's hit box on a card w px wide.
func closeRect(w int, scale float64) rect {
	s, in := px(closeSizeDip, scale), px(closeInsetDip, scale)
	return rect{L: w - in - s, T: in, R: w - in, B: in + s}
}

// buttonRects lays the button row out along the bottom of a card w x h px,
// given each label's measured text width in px. Buttons read left to right in
// Actions order (the primary first) and the row ends at the right padding.
func buttonRects(w, h int, textW []int, scale float64) []rect {
	if len(textW) == 0 {
		return nil
	}
	widths := make([]int, len(textW))
	total := 0
	for i, tw := range textW {
		bw := tw + 2*px(buttonPadDip, scale)
		if m := px(buttonMinDip, scale); bw < m {
			bw = m
		}
		widths[i] = bw
		total += bw
	}
	total += (len(widths) - 1) * px(buttonSpaceDip, scale)
	bottom := h - px(padBottomDip, scale)
	top := bottom - px(buttonDip, scale)
	x := w - px(buttonsRightDip, scale) - total
	if l := px(textLeftDip, scale); x < l {
		x = l
	}
	out := make([]rect, len(widths))
	for i, bw := range widths {
		out[i] = rect{L: x, T: top, R: x + bw, B: bottom}
		x += bw + px(buttonSpaceDip, scale)
	}
	return out
}

// hitPart reports what (x, y) on a card lands on.
func hitPart(x, y int, close rect, buttons []rect) part {
	if close.has(x, y) {
		return partClose
	}
	for i, b := range buttons {
		if b.has(x, y) {
			return part(i)
		}
	}
	return partBody
}
