//go:build windows

package overlay

import (
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// textMask renders one line of Segoe UI into a coverage mask. GDI can't write
// alpha, so the text is drawn white on black with grayscale anti-aliasing (not
// ClearType, whose colour fringes are wrong on a translucent surface) and the
// green channel becomes the coverage. Canvas.DrawMask then tints and
// composites it, which is the "alpha fixup".
func textMask(text string, sizeDip float64, weight int32, scale float64) *Mask {
	if text == "" {
		return &Mask{}
	}
	dc := w32.CreateCompatibleDC(0)
	if dc == 0 {
		return &Mask{}
	}
	defer w32.DeleteDC(dc)
	lf := w32.LOGFONT{Height: -int32(sizeDip*scale + 0.5), Weight: weight, Quality: w32.ANTIALIASED_QUALITY}
	copy(lf.FaceName[:], windows.StringToUTF16("Segoe UI"))
	font := w32.CreateFontIndirect(&lf)
	oldFont := w32.SelectObject(dc, w32.HGDIOBJ(font))
	defer func() {
		w32.SelectObject(dc, oldFont)
		w32.DeleteObject(w32.HGDIOBJ(font))
	}()

	u, err := windows.UTF16FromString(text) // fails on an embedded NUL
	if err != nil {
		return &Mask{}
	}
	u = u[:len(u)-1]
	const format = w32.DT_SINGLELINE | w32.DT_NOPREFIX
	var rc w32.RECT
	w32.DrawText(dc, u, len(u), &rc, format|w32.DT_CALCRECT)
	w, h := int(rc.Right-rc.Left), int(rc.Bottom-rc.Top)
	if w <= 0 || h <= 0 {
		return &Mask{}
	}

	var s sprite // reuse the DIB helper; it never gets a window
	defer s.destroy()
	if !s.allocDIB(w, h) {
		return &Mask{}
	}
	tmpFont := w32.SelectObject(s.dc, w32.HGDIOBJ(font))
	w32.SetBkMode(s.dc, w32.TRANSPARENT)
	w32.SetTextColor(s.dc, w32.COLORREF(w32.RGB(255, 255, 255)))
	rc = w32.RECT{Right: int32(w), Bottom: int32(h)}
	w32.DrawText(s.dc, u, len(u), &rc, format)
	_, _, _ = procGdiFlush.Call() // GDI batches; finish before reading the bits
	w32.SelectObject(s.dc, tmpFont)

	px := unsafe.Slice((*byte)(s.bits), w*h*4)
	m := &Mask{W: w, H: h, A: make([]uint8, w*h)}
	for i := range m.A {
		m.A[i] = px[i*4+1]
	}
	return m
}
