//go:build !windows

package capture

// ListTopLevelWindows is Windows-only; stub for cross-compile / tests.
func ListTopLevelWindows() []WindowInfo { return nil }
