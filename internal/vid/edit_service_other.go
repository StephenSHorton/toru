//go:build !windows

package vid

import "os/exec"

func hideConsole(*exec.Cmd) {}
