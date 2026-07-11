package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// prefsMu serializes library.json load-modify-save (path preference).
var prefsMu sync.Mutex

// libraryPrefs is the on-disk shape of %AppData%/toru/library.json.
// Dir is an absolute path to the captures/library folder; empty means default.
type libraryPrefs struct {
	Dir string `json:"dir"`
}

func libraryPrefsPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "library.json"), nil
}

// DefaultDir returns the built-in library folder (%AppData%/toru/captures),
// creating it if needed.
func DefaultDir() (string, error) {
	return capturesDir()
}

// loadPreferredDir returns the configured library directory (or default).
// Always ensures the directory exists when possible.
func loadPreferredDir() (string, error) {
	def, err := DefaultDir()
	if err != nil {
		return "", err
	}
	prefsMu.Lock()
	defer prefsMu.Unlock()
	path, err := libraryPrefsPath()
	if err != nil {
		return def, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return def, nil
	}
	var p libraryPrefs
	if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Dir) == "" {
		return def, nil
	}
	dir := filepath.Clean(p.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		// Fall back to default if the preferred path is unusable.
		return def, nil
	}
	return dir, nil
}

// savePreferredDir persists dir as the library path. Empty string clears the
// preference (next load uses DefaultDir).
func savePreferredDir(dir string) error {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	path, err := libraryPrefsPath()
	if err != nil {
		return err
	}
	p := libraryPrefs{Dir: strings.TrimSpace(dir)}
	// If the user is on the default path, store empty so resets stay portable
	// across machines that share the config shape.
	if def, err := DefaultDir(); err == nil && samePath(p.Dir, def) {
		p.Dir = ""
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return strings.EqualFold(filepath.Clean(aa), filepath.Clean(bb))
}
