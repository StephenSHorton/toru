//go:build windows

package capture

import (
	"os/exec"
	"syscall"
)

// createNoWindow prevents the child from allocating a console.
// (syscall.CREATE_NO_WINDOW is not defined in the stdlib syscall package.)
const createNoWindow = 0x08000000

// configureSysProcAttr hides the console of spawned ffmpeg processes: the
// production app is built with `-H windowsgui`, and without CREATE_NO_WINDOW
// every recording/probe/trim would flash a black console box over the user's
// screen — which would also be CAPTURED by the recording itself.
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
