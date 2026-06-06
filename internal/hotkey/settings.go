package hotkey

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"unicode"
)

// settingsMu serializes the load-modify-save of settings.json so concurrent
// SetShortcut/ResetShortcut calls can't interleave and drop an update. The atomic
// tmp+rename in saveSettings already prevents a corrupt file; this prevents a
// lost update on the read-modify-write. Mirrors overlay/persist.go's cropFileMu.
var settingsMu sync.Mutex

// persistedBinding is the on-disk shape of one shortcut. Key is a single
// uppercase char ("S") so the JSON is human-readable; an empty/invalid Key means
// "fall back to the default" on load.
type persistedBinding struct {
	Ctrl  bool   `json:"ctrl"`
	Shift bool   `json:"shift"`
	Alt   bool   `json:"alt"`
	Win   bool   `json:"win"`
	Key   string `json:"key"`
}

// settingsStore is the on-disk shape of %AppData%\toru\settings.json. Shortcuts
// are keyed by action id ("overlay").
type settingsStore struct {
	Shortcuts map[string]persistedBinding `json:"shortcuts"`
}

// settingsStorePath returns %AppData%\toru\settings.json (os.UserConfigDir on
// Windows is %AppData%). It also ensures the parent directory exists. Mirrors
// overlay/persist.go's overlayStorePath so both files share the toru config dir.
func settingsStorePath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "settings.json"), nil
}

// loadSettings reads the persisted settings store. A missing or unparseable file
// yields an empty (non-nil-map) store so callers can index/assign safely.
func loadSettings() settingsStore {
	st := settingsStore{Shortcuts: map[string]persistedBinding{}}
	path, err := settingsStorePath()
	if err != nil {
		return st
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var parsed settingsStore
	if err := json.Unmarshal(b, &parsed); err != nil {
		return st
	}
	if parsed.Shortcuts == nil {
		parsed.Shortcuts = map[string]persistedBinding{}
	}
	return parsed
}

// saveSettings writes the store atomically (tmp + rename) so a crash mid-write
// never corrupts settings.json. The temp prefix is "settings-*.json.tmp"
// (distinct from overlay's "overlay-*.json.tmp") to avoid a name collision in the
// shared toru config dir.
func saveSettings(st settingsStore) error {
	if st.Shortcuts == nil {
		st.Shortcuts = map[string]persistedBinding{}
	}
	path, err := settingsStorePath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "settings-*.json.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// validKeyRune reports whether r is a supported trigger key (A-Z or 0-9 after
// upper-casing). Shared by the loader and the service validator.
func validKeyRune(r rune) bool {
	r = unicode.ToUpper(r)
	return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// LoadBinding returns the persisted binding for action, or def when the file/key
// is absent or the persisted Key is invalid. Used by main.go to seed the live
// binding at startup.
func LoadBinding(action string, def Binding) Binding {
	st := loadSettings()
	pb, ok := st.Shortcuts[action]
	if !ok {
		return def
	}
	rs := []rune(pb.Key)
	if len(rs) != 1 || !validKeyRune(rs[0]) {
		return def
	}
	return Binding{
		Ctrl:  pb.Ctrl,
		Shift: pb.Shift,
		Alt:   pb.Alt,
		Win:   pb.Win,
		Key:   unicode.ToUpper(rs[0]),
	}
}

// persistBinding writes b for action into settings.json under settingsMu
// (read-modify-write so other actions are preserved).
func persistBinding(action string, b Binding) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	st := loadSettings()
	if st.Shortcuts == nil {
		st.Shortcuts = map[string]persistedBinding{}
	}
	st.Shortcuts[action] = persistedBinding{
		Ctrl:  b.Ctrl,
		Shift: b.Shift,
		Alt:   b.Alt,
		Win:   b.Win,
		Key:   string(unicode.ToUpper(b.Key)),
	}
	return saveSettings(st)
}
