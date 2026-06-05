//go:build windows

// Package dpi makes the process Per-Monitor-V2 DPI aware so screen coordinates
// and gdigrab capture come back in true physical pixels across mixed-DPI
// monitors. This MUST run before any window is created.
package dpi

import "golang.org/x/sys/windows"

// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 == (HANDLE)-4
var perMonitorV2 = ^uintptr(3) // -4 as an unsigned pointer-sized value

// EnsurePerMonitorV2 calls SetProcessDpiAwarenessContext(-4). It is best-effort:
// if the OS already set awareness (e.g. via the app manifest, which we also
// ship), the call is a harmless no-op and any error is ignored.
func EnsurePerMonitorV2() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	proc := user32.NewProc("SetProcessDpiAwarenessContext")
	if proc.Find() != nil {
		return // pre-1703 Windows; the manifest covers us.
	}
	_, _, _ = proc.Call(perMonitorV2)
}
