//go:build !windows

package vid

// fontFiles is empty off Windows: texts use DefaultTextFont there.
func fontFiles() []string { return nil }
