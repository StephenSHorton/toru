package clicker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// fileMu serializes writes of clicker.json and clicker-stats.json. The atomic
// tmp+rename already prevents a torn file; this keeps two saves from racing on
// the rename order.
var fileMu sync.Mutex

const (
	settingsFile = "clicker.json"
	statsFile    = "clicker-stats.json"
)

// storePath returns %AppData%\toru\<name>, creating the toru folder. On Windows
// os.UserConfigDir reads %AppData%, which is what the tests redirect.
func storePath(name string) (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}

// readStore reads a store file with a UTF-8 BOM stripped (Notepad adds one).
func readStore(name string) ([]byte, error) {
	path, err := storePath(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")), nil
}

// writeStore writes v as indented JSON via a temp file + rename, so a crash
// mid-write never leaves a truncated file. The temp names ("clicker-*.json.tmp")
// cannot collide with the other stores in the shared toru folder.
func writeStore(name string, v any) error {
	fileMu.Lock()
	defer fileMu.Unlock()
	path, err := storePath(name)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "clicker-*.json.tmp")
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

// loadSettings reads clicker.json over the defaults (fields missing from the
// file keep their default) and normalizes it. A missing or unparseable file
// gives the defaults. The factory presets are seeded whenever the file has no
// "presets" array at all; an empty array stays empty, so a user who deleted
// them does not get them back.
func loadSettings() Settings {
	s := DefaultSettings()
	seed := true
	if b, err := readStore(settingsFile); err == nil {
		parsed := DefaultSettings()
		var probe struct {
			Presets *json.RawMessage `json:"presets"`
		}
		if json.Unmarshal(b, &parsed) == nil && json.Unmarshal(b, &probe) == nil {
			s = parsed
			seed = probe.Presets == nil || bytes.Equal(bytes.TrimSpace(*probe.Presets), []byte("null"))
		}
	}
	if seed {
		s.Presets = FactoryPresets()
	}
	return Normalize(s)
}

// saveSettings writes s (already normalized) to clicker.json.
func saveSettings(s Settings) error { return writeStore(settingsFile, s) }

// loadStats reads clicker-stats.json; missing or garbage gives zeros and
// negative totals are reset.
func loadStats() Stats {
	var st Stats
	b, err := readStore(statsFile)
	if err != nil || json.Unmarshal(b, &st) != nil {
		return Stats{}
	}
	if st.Clicks < 0 || st.Runs < 0 || !(st.Seconds >= 0) || !finite(st.Seconds) {
		return Stats{}
	}
	return st
}

func saveStats(st Stats) error { return writeStore(statsFile, st) }
