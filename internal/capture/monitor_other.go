//go:build !windows

package capture

// monitorHandle is Windows-only (gfxcapture); see monitor_windows.go.
func monitorHandle(ScreenInfo) uintptr { return 0 }
