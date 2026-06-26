//go:build windows

package main

import (
	"syscall"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// Region/window-shape calls the bundled wails w32 package doesn't wrap. Loaded
// lazily; only this Windows-only file references them.
var (
	rfUser32 = syscall.NewLazyDLL("user32.dll")
	rfGdi32  = syscall.NewLazyDLL("gdi32.dll")

	rfProcSetWindowRgn  = rfUser32.NewProc("SetWindowRgn")
	rfProcCreateRectRgn = rfGdi32.NewProc("CreateRectRgn")
	rfProcCombineRgn    = rfGdi32.NewProc("CombineRgn")
)

const rfRgnDiff = 4 // RGN_DIFF: dst = src1 − src2

// makeWindowClickThrough makes the recorded-region indicator pass mouse input
// through to whatever is being recorded, WITHOUT forcing the window opaque.
//
// It uses TWO mechanisms, because WS_EX_TRANSPARENT alone proved insufficient:
// the transparent WebView2 child still swallowed clicks inside the recorded rect,
// so you couldn't interact with what you were recording. So we ALSO punch a
// literal hole in the window (SetWindowRgn) over the recorded interior — those
// pixels stop being part of the window, and clicks land on the app underneath
// natively. WS_EX_TRANSPARENT remains for the thin glowing border band (the
// margin), so even a click on the outline itself falls through.
//
// We deliberately avoid Wails' IgnoreMouseEvents: on a transparent window it adds
// WS_EX_LAYERED, which composites opaquely and whites out the see-through frame.
//
// Must run AFTER the HWND exists. NewWithOptions realises the window synchronously
// before returning (the app is running), so the caller invokes this right after.
// A nil/zero handle is a no-op.
func makeWindowClickThrough(win *application.WebviewWindow) {
	if win == nil {
		return
	}
	ptr := win.NativeWindow()
	if ptr == nil {
		return
	}
	hwnd := w32.HWND(uintptr(ptr))

	// 1) WS_EX_TRANSPARENT — hit-test fall-through for the border band.
	ex := uint32(w32.GetWindowLong(hwnd, w32.GWL_EXSTYLE))
	if ex&w32.WS_EX_TRANSPARENT == 0 {
		w32.SetWindowLong(hwnd, w32.GWL_EXSTYLE, ex|w32.WS_EX_TRANSPARENT)
		// Flush the ex-style change so the new hit-testing takes effect.
		w32.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
			w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE|w32.SWP_FRAMECHANGED)
	}

	// 2) Carve out the recorded interior so clicks there reach the recorded app
	//    (the WebView2 child does not honour WS_EX_TRANSPARENT).
	punchRecFrameHole(hwnd)
}

// punchRecFrameHole sets the window region to a frame: the full client rect minus
// an inner rect inset by recFrameMargin (DIP, scaled to this monitor's DPI). The
// inner rect == the recorded region, so it becomes a true hole — not painted, not
// hit-tested. The glowing outline lives in the kept margin band and still renders.
func punchRecFrameHole(hwnd w32.HWND) {
	rc := w32.GetClientRect(hwnd)
	if rc == nil {
		return
	}
	cw := rc.Right - rc.Left
	ch := rc.Bottom - rc.Top
	if cw <= 0 || ch <= 0 {
		return
	}
	// recFrameMargin is DIP; the client rect is physical px → scale the margin.
	scale := float64(w32.GetDpiForWindow(hwnd)) / 96.0
	if scale <= 0 {
		scale = 1
	}
	m := int32(float64(recFrameMargin)*scale + 0.5)
	if m <= 0 || 2*m >= cw || 2*m >= ch {
		return // too small to carve a hole; leave the window solid
	}

	outer, _, _ := rfProcCreateRectRgn.Call(0, 0, uintptr(cw), uintptr(ch))
	inner, _, _ := rfProcCreateRectRgn.Call(uintptr(m), uintptr(m), uintptr(cw-m), uintptr(ch-m))
	if outer == 0 || inner == 0 {
		if outer != 0 {
			w32.DeleteObject(w32.HGDIOBJ(outer))
		}
		if inner != 0 {
			w32.DeleteObject(w32.HGDIOBJ(inner))
		}
		return
	}
	_, _, _ = rfProcCombineRgn.Call(outer, outer, inner, uintptr(rfRgnDiff))
	w32.DeleteObject(w32.HGDIOBJ(inner))

	// SetWindowRgn takes ownership of `outer` on success — don't delete it then.
	ret, _, _ := rfProcSetWindowRgn.Call(uintptr(hwnd), outer, 1 /* bRedraw */)
	if ret == 0 {
		w32.DeleteObject(w32.HGDIOBJ(outer)) // region not consumed; avoid a GDI leak
	}
}
