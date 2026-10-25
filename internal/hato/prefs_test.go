package hato

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolatePrefs points every home/config env the Go stdlib consults at a temp
// dir so the test never touches the real %AppData%\toru\hato.json. Returns the
// fake profile dir.
func isolatePrefs(t *testing.T) string {
	t.Helper()
	profile := t.TempDir()
	appdata := filepath.Join(profile, "AppData", "Roaming")
	if err := os.MkdirAll(appdata, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APPDATA", appdata)         // os.UserConfigDir on Windows
	t.Setenv("USERPROFILE", profile)     // os.UserHomeDir on Windows
	t.Setenv("HOME", profile)            // os.UserHomeDir elsewhere
	t.Setenv("XDG_CONFIG_HOME", appdata) // os.UserConfigDir on Linux
	return profile
}

func TestDefaultReceiveDirPrefersDownloads(t *testing.T) {
	profile := isolatePrefs(t)
	if got := DefaultReceiveDir(); !samePath(got, profile) {
		t.Fatalf("no Downloads yet: default = %q, want home %q", got, profile)
	}
	dl := filepath.Join(profile, "Downloads")
	if err := os.Mkdir(dl, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DefaultReceiveDir(); !samePath(got, dl) {
		t.Fatalf("default = %q, want Downloads %q", got, dl)
	}
}

func TestReceiveDirPrefsRoundTrip(t *testing.T) {
	profile := isolatePrefs(t)
	if err := os.Mkdir(filepath.Join(profile, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	def := DefaultReceiveDir()

	// Nothing saved → default, and the prefs file is not required to exist.
	if got := loadReceiveDir(); !samePath(got, def) {
		t.Fatalf("fresh load = %q, want default %q", got, def)
	}

	custom := filepath.Join(profile, "Drop", "Here") // does not exist yet
	if err := saveReceiveDir(custom); err != nil {
		t.Fatal(err)
	}
	prefs, err := prefsPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(prefs) != "hato.json" || filepath.Base(filepath.Dir(prefs)) != "toru" {
		t.Fatalf("prefs path = %q, want …\\toru\\hato.json", prefs)
	}
	raw, err := os.ReadFile(prefs)
	if err != nil {
		t.Fatalf("prefs not written: %v", err)
	}
	if string(raw) == "" || !containsAll(string(raw), `"receiveDir"`, "Drop") {
		t.Fatalf("unexpected prefs body: %s", raw)
	}
	got := loadReceiveDir()
	if !samePath(got, custom) {
		t.Fatalf("load after save = %q, want %q", got, custom)
	}
	if st, err := os.Stat(custom); err != nil || !st.IsDir() {
		t.Fatalf("load should create the configured folder: %v", err)
	}

	// Saving the default path itself clears the preference.
	if err := saveReceiveDir(def); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(prefs)
	if !containsAll(string(raw), `"receiveDir": ""`) {
		t.Fatalf("saving the default should store empty: %s", raw)
	}

	// Explicit reset.
	if err := saveReceiveDir(custom); err != nil {
		t.Fatal(err)
	}
	if err := saveReceiveDir(""); err != nil {
		t.Fatal(err)
	}
	if got := loadReceiveDir(); !samePath(got, def) {
		t.Fatalf("after reset = %q, want default %q", got, def)
	}
}

func TestLoadReceiveDirIgnoresGarbage(t *testing.T) {
	isolatePrefs(t)
	def := DefaultReceiveDir()
	prefs, err := prefsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prefs, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := loadReceiveDir(); !samePath(got, def) {
		t.Fatalf("garbage prefs → %q, want default %q", got, def)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
