//go:build windows

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/w32"
)

// dropTopmostBehindForeground clears a window's AlwaysOnTop without lifting it
// over the app the user just switched to. Wails' SetAlwaysOnTop(false) uses
// HWND_NOTOPMOST, which re-stacks the window ABOVE every normal window — the
// one just clicked included — so instead it is slotted directly behind the
// foreground window (which also drops its topmost bit). When the foreground
// is itself topmost (the capture overlay), HWND_NOTOPMOST is the right place.
func dropTopmostBehindForeground(win *application.WebviewWindow) {
	if win == nil {
		return
	}
	ptr := win.NativeWindow()
	if ptr == nil {
		return
	}
	hwnd := w32.HWND(uintptr(ptr))
	if uint32(w32.GetWindowLong(hwnd, w32.GWL_EXSTYLE))&w32.WS_EX_TOPMOST == 0 {
		return
	}
	after := w32.GetForegroundWindow()
	if after == 0 || after == hwnd ||
		uint32(w32.GetWindowLong(after, w32.GWL_EXSTYLE))&w32.WS_EX_TOPMOST != 0 {
		after = w32.HWND_NOTOPMOST
	}
	w32.SetWindowPos(hwnd, after, 0, 0, 0, 0, w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOACTIVATE)
}
