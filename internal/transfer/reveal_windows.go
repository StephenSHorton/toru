//go:build windows

package transfer

import (
	"os/exec"
	"syscall"
)

// openFolder opens dir in Explorer. Start, not Run: explorer.exe returns exit
// code 1 even on success, so waiting on it would report a phantom failure.
func openFolder(dir string) error {
	return exec.Command("explorer.exe", dir).Start()
}

// revealInExplorer opens Explorer with path selected. The whole command line is
// built by hand via SysProcAttr.CmdLine because Go's default quoting turns the
// argument into "/select,C:\a b\x" (quotes around the entire token), which
// Explorer mis-parses when the path has a space — it opens the wrong folder.
// Explorer wants the literal form  /select,"C:\a b\x".
func revealInExplorer(path string) error {
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: `explorer.exe /select,"` + path + `"`,
	}
	return cmd.Start()
}
