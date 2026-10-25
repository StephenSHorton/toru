//go:build !windows

// Package toast draws Toru's small native notification. Toru only ships on
// Windows; this keeps cross-compiles and tests building.
package toast

// Show is a no-op off Windows.
func Show(string, string, bool) {}

// Visible always reports false off Windows.
func Visible() bool { return false }
