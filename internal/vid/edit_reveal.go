package vid

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RevealFile shows path selected in Explorer: the export dialog's "Show in
// folder". Only an existing file is revealed; Explorer selects it and runs
// nothing.
func (s *VideoService) RevealFile(path string) error {
	p, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("reveal: %w", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return fmt.Errorf("reveal: %w", err)
	}
	if fi.IsDir() {
		return fmt.Errorf("reveal: %s is a folder", p)
	}
	if runtime.GOOS == "windows" {
		return exec.Command("explorer.exe", "/select,", p).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(p)).Start()
}
