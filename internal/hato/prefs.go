package hato

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// prefsMu serializes hato.json load-modify-save, mirroring internal/history.
var prefsMu sync.Mutex

// hatoPrefs is the on-disk shape of %AppData%\toru\hato.json. ReceiveDir is an
// absolute path; empty means "use the default" so a fresh install and a reset
// install look identical on disk.
type hatoPrefs struct {
	ReceiveDir string `json:"receiveDir"`
}

// prefsPath returns %AppData%\toru\hato.json, creating the toru folder. On
// Windows os.UserConfigDir reads %AppData%, which is what the tests redirect.
func prefsPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "hato.json"), nil
}

// DefaultReceiveDir is where received files land unless the user picks a
// folder: %USERPROFILE%\Downloads when it exists (where every browser puts
// downloads, so it is where people look), else the home folder, else %TEMP% as
// a last resort so a receive can always complete somewhere.
func DefaultReceiveDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	dl := filepath.Join(home, "Downloads")
	if st, err := os.Stat(dl); err == nil && st.IsDir() {
		return dl
	}
	return home
}

// loadReceiveDir returns the configured receive folder, or the default when
// nothing is saved, the file is unreadable, or the saved folder cannot be
// created (a removed USB drive should not strand every receive).
func loadReceiveDir() string {
	def := DefaultReceiveDir()
	prefsMu.Lock()
	defer prefsMu.Unlock()
	path, err := prefsPath()
	if err != nil {
		return def
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return def
	}
	var p hatoPrefs
	if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.ReceiveDir) == "" {
		return def
	}
	dir := filepath.Clean(p.ReceiveDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return def
	}
	return dir
}

// saveReceiveDir persists dir; "" (or the default path itself) clears the
// preference. Written via a temp file + rename so a crash mid-write can't
// leave a truncated JSON that loadReceiveDir would then ignore.
func saveReceiveDir(dir string) error {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	path, err := prefsPath()
	if err != nil {
		return err
	}
	p := hatoPrefs{ReceiveDir: strings.TrimSpace(dir)}
	if samePath(p.ReceiveDir, DefaultReceiveDir()) {
		p.ReceiveDir = ""
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

// samePath compares two paths the way Windows does: absolute, cleaned,
// case-insensitive.
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
