//go:build !windows

package transcript

import "os/exec"

func configureProc(*exec.Cmd) {}

func afterStart(*exec.Cmd) {}
