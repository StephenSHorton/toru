//go:build !windows

package media

import "os/exec"

func hideConsole(*exec.Cmd) {}
