package clicker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, "toru")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMissingSeedsFactoryPresets(t *testing.T) {
	isolateConfig(t)
	s := loadSettings()
	if len(s.Presets) != 3 || s.Presets[0].ID != "factory-standard" || s.Presets[1].Name != "Rapid Fire" {
		t.Fatalf("presets = %+v", s.Presets)
	}
	if r := s.Presets[1].Profile; r.Cadence.Rate != 100 || r.DutyPercent != 20 || r.Limit.Kind != LimitClicks || r.Limit.Clicks != 5000 {
		t.Fatalf("rapid fire = %+v", r)
	}
	if !s.Armed || s.Profile.Cadence.Rate != 25 {
		t.Fatalf("defaults not loaded: %+v", s)
	}
}

func TestLoadGarbageGivesDefaults(t *testing.T) {
	dir := isolateConfig(t)
	writeConfig(t, dir, settingsFile, "{not json")
	s := loadSettings()
	if s.Profile.Cadence.Rate != 25 || len(s.Presets) != 3 {
		t.Fatalf("garbage load = %+v", s)
	}
}

func TestLoadBOMAndPartialFile(t *testing.T) {
	dir := isolateConfig(t)
	writeConfig(t, dir, settingsFile, "\xEF\xBB\xBF"+`{"armed": false, "profile": {"cadence": {"mode": "interval", "intervalMs": 100}}, "presets": []}`)
	s := loadSettings()
	if s.Armed {
		t.Fatal("armed not read through a BOM")
	}
	if c := s.Profile.Cadence; c.Mode != CadenceInterval || c.Rate != 10 || c.Unit != UnitSecond {
		t.Fatalf("cadence = %+v", c)
	}
	// Missing fields keep their defaults (corners, edges, delay).
	if !s.Profile.Corners.Enabled || s.Profile.Corners.TL != 50 || s.StartDelayMs != 3000 {
		t.Fatalf("defaults lost: %+v", s)
	}
	if len(s.Presets) != 0 {
		t.Fatal("an explicit empty preset list must stay empty")
	}
}

func TestLoadNullPresetsSeeds(t *testing.T) {
	dir := isolateConfig(t)
	writeConfig(t, dir, settingsFile, `{"presets": null}`)
	if n := len(loadSettings().Presets); n != 3 {
		t.Fatalf("presets = %d", n)
	}
}

func TestSaveRoundTripAtomic(t *testing.T) {
	dir := isolateConfig(t)
	s := loadSettings()
	s.HighRate = true
	s.Profile.Cadence = Cadence{Mode: CadenceRate, Rate: 800, Unit: UnitSecond}
	s.StopZones = []StopZone{{X: 1, Y: 2, W: 3, H: 4, Action: ZonePause}}
	s = Normalize(s)
	if err := saveSettings(s); err != nil {
		t.Fatal(err)
	}
	got := loadSettings()
	if got.Profile.Cadence.Rate != 800 || len(got.StopZones) != 1 || got.StopZones[0].ID != s.StopZones[0].ID {
		t.Fatalf("round trip = %+v", got)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "toru"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestStatsPersist(t *testing.T) {
	dir := isolateConfig(t)
	if st := loadStats(); st != (Stats{}) {
		t.Fatalf("fresh stats = %+v", st)
	}
	if err := saveStats(Stats{Clicks: 12, Seconds: 3.5, Runs: 2}); err != nil {
		t.Fatal(err)
	}
	if st := loadStats(); st != (Stats{Clicks: 12, Seconds: 3.5, Runs: 2}) {
		t.Fatalf("stats = %+v", st)
	}
	writeConfig(t, dir, statsFile, `{"clicks": -4, "runs": 1}`)
	if st := loadStats(); st != (Stats{}) {
		t.Fatalf("negative stats kept: %+v", st)
	}
	writeConfig(t, dir, statsFile, `nope`)
	if st := loadStats(); st != (Stats{}) {
		t.Fatalf("garbage stats = %+v", st)
	}
}

func TestServiceResetStats(t *testing.T) {
	r := newRig(t)
	if err := saveStats(Stats{Clicks: 5, Runs: 1}); err != nil {
		t.Fatal(err)
	}
	r.s.Init()
	if r.s.Stats().Clicks != 5 {
		t.Fatal("Init did not load stats")
	}
	if st := r.s.ResetStats(); st != (Stats{}) || loadStats() != (Stats{}) {
		t.Fatal("ResetStats did not zero and persist")
	}
}

func TestResetSettingsKeepsPresets(t *testing.T) {
	r := newRig(t)
	if _, err := r.s.SavePreset("Mine"); err != nil {
		t.Fatal(err)
	}
	r.settings(func(s *Settings) { s.Profile.Cadence.Rate = 7; s.StartDelayMs = 0 })
	got, err := r.s.ResetSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.Profile.Cadence.Rate != 25 || got.StartDelayMs != 3000 || len(got.Presets) != 4 || got.ActivePresetID != "" {
		t.Fatalf("reset = %+v", got)
	}
}

func TestSetSettingsIgnoresPresetFields(t *testing.T) {
	r := newRig(t)
	s := r.s.GetSettings()
	s.Presets = nil
	s.ActivePresetID = "factory-rapid"
	got, err := r.s.SetSettings(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Presets) != 3 || got.ActivePresetID != "" {
		t.Fatalf("SetSettings changed presets: %d active=%q", len(got.Presets), got.ActivePresetID)
	}
}
