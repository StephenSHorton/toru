//go:build !windows

package capture

// ListTopLevelWindows is Windows-only; stub for cross-compile / tests.
func ListTopLevelWindows() []WindowInfo { return nil }

// WindowIsMaximized is Windows-only; stub always false.
func WindowIsMaximized(hwnd uint64) bool { return false }

// LookupWindow is Windows-only; stub always missing.
func LookupWindow(hwnd uint64) (WindowInfo, bool) { return WindowInfo{}, false }
