//go:build windows

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// makeWindowClickThrough makes win pass ALL mouse input through to whatever is
// behind it, WITHOUT forcing the window opaque.
//
// Wails' WebviewWindowOptions.IgnoreMouseEvents would also do the click-through,
// but on a transparent window it additionally sets WS_EX_LAYERED
// (webview_window_windows.go), and a layered window is composited opaquely — the
// transparent DirectComposition surface whites out, so the recorded-region
// indicator shows a solid white rectangle over what you're recording instead of a
// see-through frame. We keep the window on the transparent DirectComposition path
// (BackgroundTypeTransparent, no IgnoreMouseEvents) and add ONLY WS_EX_TRANSPARENT
// here, which changes hit-testing (input falls through) without touching the
// compositing path.
//
// Must run AFTER the HWND exists. NewWithOptions creates the window synchronously
// on the main thread (InvokeSync) before returning once the app is running, so the
// caller can invoke this immediately after NewWithOptions. A nil/zero handle (the
// window was never realised) is a no-op.
func makeWindowClickThrough(win *application.WebviewWindow) {
	if win == nil {
		return
	}
	ptr := win.NativeWindow()
	if ptr == nil {
		return
	}
	hwnd := w32.HWND(uintptr(ptr))
	ex := uint32(w32.GetWindowLong(hwnd, w32.GWL_EXSTYLE))
	if ex&w32.WS_EX_TRANSPARENT != 0 {
		return // already click-through
	}
	w32.SetWindowLong(hwnd, w32.GWL_EXSTYLE, ex|w32.WS_EX_TRANSPARENT)
	// Flush the ex-style change so the new hit-testing behaviour takes effect.
	w32.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE|w32.SWP_FRAMECHANGED)
}
