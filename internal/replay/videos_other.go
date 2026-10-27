//go:build !windows

package replay

import (
	"os"
	"path/filepath"
)

// videosDir is ~/Videos off Windows (Toru only ships on Windows; this keeps
// cross-compiles and tests building).
func videosDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Videos")
}
