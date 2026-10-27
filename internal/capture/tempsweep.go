package capture

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tempsweep.go clears stale working files out of %TEMP%\toru. Every manual
// recording, frozen backdrop and screenshot crop is written there first; the
// library keeps its OWN copy (history.Add copies), so the temp original is
// only needed while its editor window is open. Nothing else ever deleted them,
// so the folder grew by every recording ever made.
//
// Edits are the exception: Trim / crop / For Discord write <src>-trimmed,
// -cropped and -discord next to the source and never reach the library, so
// for a fresh recording that temp file is the only copy. Those are kept.
//
// Age, not "is it referenced", is the rule: a Copy puts the temp path on the
// clipboard as a file drop and an editor can stay open for hours, so anything
// touched within TempMaxAge survives.

// TempMaxAge is how old a temp artifact must be before the sweep removes it.
const TempMaxAge = 24 * time.Hour

// TempDir is the app's working folder for session-only artifacts.
func TempDir() string { return filepath.Join(os.TempDir(), "toru") }

// SweepTemp removes entries in dir last modified more than maxAge before now:
// files and leftover share-*/ffpass-* folders alike. Best-effort; a file still
// open (a playing editor) fails to delete and is retried next sweep. Returns
// how many entries were removed and the bytes freed.
func SweepTemp(dir string, maxAge time.Duration, now time.Time) (removed int, freed int64) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0
	}
	cutoff := now.Add(-maxAge)
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if e.IsDir() {
			// Only folders Toru makes; never recurse into something unexpected.
			if !strings.HasPrefix(e.Name(), "share-") && !strings.HasPrefix(e.Name(), "ffpass-") {
				continue
			}
			if os.RemoveAll(p) == nil {
				removed++
			}
			continue
		}
		if isEditOutput(e.Name()) {
			continue
		}
		if os.Remove(p) == nil {
			removed++
			freed += info.Size()
		}
	}
	return removed, freed
}

// isEditOutput reports whether name is a Trim / crop / For Discord result
// (internal/vid names them <src>-trimmed, -cropped, -discord).
func isEditOutput(name string) bool {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	for _, s := range []string{"-trimmed", "-cropped", "-discord"} {
		if strings.Contains(stem, s) {
			return true
		}
	}
	return false
}
