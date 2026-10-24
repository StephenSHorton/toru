//go:build windows

package replay

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// videosDir resolves the user's Videos known folder (follows OneDrive or a
// moved Videos folder), falling back to %USERPROFILE%\Videos.
func videosDir() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_Videos, 0); err == nil && p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Videos")
}
