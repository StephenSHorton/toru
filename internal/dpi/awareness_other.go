//go:build !windows

package dpi

// EnsurePerMonitorV2 is a no-op on non-Windows platforms (Windows-first app;
// this stub exists only so the module cross-compiles for tooling/CI).
func EnsurePerMonitorV2() {}
