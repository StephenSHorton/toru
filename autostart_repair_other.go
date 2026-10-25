//go:build !windows

package main

// repairLaunchAtLogin is Windows-only (the Run registry key).
func repairLaunchAtLogin() {}
