//go:build windows

package replay

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionStringReadsExplorer(t *testing.T) {
	exe := filepath.Join(os.Getenv("SystemRoot"), "explorer.exe")
	if got := versionString(exe, "FileDescription"); got == "" {
		t.Errorf("no FileDescription for %s", exe)
	} else {
		t.Logf("%s: %q", exe, got)
	}
}

func TestForegroundDoesNotPanic(t *testing.T) {
	app, ok := foreground()
	t.Logf("foreground ok=%v %+v -> folder %q", ok, app, gameFolderFor(app))
}
