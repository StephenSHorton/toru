//go:build windows

package media

import (
	"os/exec"
	"syscall"
)

// hideConsole keeps a spawned ffprobe from flashing a console window (the
// app is built with -H windowsgui).
func hideConsole(cmd *exec.Cmd) {
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
