//go:build !windows

package transfer

import (
	"os/exec"
	"path/filepath"
)

// openFolder is the non-Windows stub (Toru is Windows-first; this keeps
// cross-compiles and non-Windows `go vet` honest).
func openFolder(dir string) error {
	return exec.Command("xdg-open", dir).Start()
}

// revealInExplorer has no portable "select this file" form; open the parent.
func revealInExplorer(path string) error {
	return exec.Command("xdg-open", filepath.Dir(path)).Start()
}
