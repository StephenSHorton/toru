//go:build !windows

package capture

import "os/exec"

// configureSysProcAttr is a no-op off Windows (no console window to suppress).
// See proc_windows.go for the real implementation.
func configureSysProcAttr(*exec.Cmd) {}
