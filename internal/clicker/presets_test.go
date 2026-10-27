package clicker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)

func baseSettings() Settings {
	s := DefaultSettings()
	s.Presets = FactoryPresets()
	return Normalize(s)
}

func TestSaveApplyUpdate(t *testing.T) {
	s := baseSettings()
	s.Profile.Cadence = Cadence{Mode: CadenceRate, Rate: 7, Unit: UnitSecond}
	s = Normalize(s)
	s, err := savePreset(s, "  Seven  ", t0)
	if err != nil {
		t.Fatal(err)
	}
	p := s.Presets[len(s.Presets)-1]
	if p.Name != "Seven" || s.ActivePresetID != p.ID || p.Profile.Cadence.Rate != 7 || p.CreatedAt != "2026-10-26T12:00:00Z" {
		t.Fatalf("saved = %+v active=%q", p, s.ActivePresetID)
	}
	if PresetModified(s) {
		t.Fatal("fresh save reads as modified")
	}
	s.Profile.DutyPercent = 10
	if !PresetModified(s) {
		t.Fatal("edit not detected as modified")
	}
	s, err = updatePreset(s, p.ID, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if PresetModified(s) || s.Presets[len(s.Presets)-1].UpdatedAt != "2026-10-26T13:00:00Z" {
		t.Fatal("update did not snapshot")
	}
	s, err = applyPreset(s, "factory-rapid")
	if err != nil {
		t.Fatal(err)
	}
	if s.Profile.Cadence.Rate != 100 || s.ActivePresetID != "factory-rapid" || PresetModified(s) {
		t.Fatalf("apply = %+v", s.Profile.Cadence)
	}
}

func TestPresetNameRules(t *testing.T) {
	s := baseSettings()
	for _, bad := range []string{"", "   ", "rapid fire", strings.Repeat("x", 41)} {
		if _, err := savePreset(s, bad, t0); err == nil || !strings.Contains(err.Error(), "clicker: "+ErrPresetName+": ") {
			t.Errorf("save %q: %v", bad, err)
		}
	}
	if _, err := renamePreset(s, "factory-rapid", "Precision MODE", t0); err == nil {
		t.Error("rename onto another preset's name allowed")
	}
	got, err := renamePreset(s, "factory-rapid", "rapid FIRE", t0) // its own name, new case
	if err != nil || got.Presets[1].Name != "rapid FIRE" {
		t.Errorf("case-only rename: %v %q", err, got.Presets[1].Name)
	}
	if _, err := renamePreset(s, "nope", "x", t0); err == nil || !strings.Contains(err.Error(), ErrPresetNotFound) {
		t.Errorf("rename missing: %v", err)
	}
}

func TestDuplicateSuffix(t *testing.T) {
	s := baseSettings()
	s, _ = duplicatePreset(s, "factory-rapid", t0)
	if s.Presets[2].Name != "Rapid Fire (2)" || s.Presets[2].ID == "factory-rapid" {
		t.Fatalf("first copy = %+v", s.Presets[2])
	}
	s, _ = duplicatePreset(s, s.Presets[2].ID, t0) // copy of the copy
	if s.Presets[3].Name != "Rapid Fire (3)" {
		t.Fatalf("second copy = %q", s.Presets[3].Name)
	}
	if s.ActivePresetID != "" {
		t.Fatal("duplicate changed the active preset")
	}
	long := strings.Repeat("y", 40)
	s.Presets = append(s.Presets, Preset{ID: "long", Name: long})
	s = Normalize(s)
	s, _ = duplicatePreset(s, "long", t0)
	name := s.Presets[len(s.Presets)-1].Name
	if len([]rune(name)) > 40 || !strings.HasSuffix(name, " (2)") {
		t.Fatalf("long copy = %q", name)
	}
}

func TestDeletePresetClearsActive(t *testing.T) {
	s := baseSettings()
	s, _ = applyPreset(s, "factory-precision")
	s, err := deletePreset(s, "factory-precision")
	if err != nil || len(s.Presets) != 2 || s.ActivePresetID != "" {
		t.Fatalf("delete: %v %d %q", err, len(s.Presets), s.ActivePresetID)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	s := baseSettings()
	data, err := exportPresetJSON(s.Presets[1])
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if json.Unmarshal(data, &env) != nil || env["type"] != PresetFileMarker || env["version"] != float64(1) {
		t.Fatalf("envelope = %s", data)
	}
	got, id, err := importPresetJSON(s, data, t0)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Presets[len(got.Presets)-1]
	if p.ID != id || p.ID == "factory-rapid" || p.Name != "Rapid Fire (2)" || p.Profile.Cadence.Rate != 100 {
		t.Fatalf("imported = %+v", p)
	}
	if got.ActivePresetID != "" {
		t.Fatal("import made the preset active")
	}
}

func TestImportToruFillsMissingFromCurrent(t *testing.T) {
	s := baseSettings()
	s.Profile.Corners.TL = 77
	s = Normalize(s)
	file := `{"type":"toru-clicker-preset","version":1,"preset":{"name":"Partial","profile":{"cadence":{"mode":"rate","rate":5,"unit":"s"}}}}`
	got, _, err := importPresetJSON(s, []byte(file), t0)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Presets[len(got.Presets)-1].Profile
	if p.Cadence.Rate != 5 || p.Cadence.IntervalMs != 200 || p.Corners.TL != 77 {
		t.Fatalf("partial import = %+v", p)
	}
}

// blurFixture is a Blur AutoClicker export written from the key names in the
// settings spec (blur-settings-ui.md §1a, §3, §5), legacy keys included.
const blurFixture = `{
  "type": "blur-autoclicker-preset",
  "version": 1,
  "preset": {
    "id": "6c1f0e0a-0000-4000-8000-000000000001",
    "name": "Mining",
    "createdAt": "2025-01-02T03:04:05.000Z",
    "updatedAt": "2025-01-02T03:04:05.000Z",
    "clickSpeed": 12,
    "clickInterval": "m",
    "inputType": "keyboard",
    "keyboardKey": "keye",
    "keyboardKeyCase": "upper",
    "mouseButton": "Right",
    "mode": "Hold",
    "hotkey": "ctrl+y",
    "clickDurationMode": "Click",
    "dutyCycleEnabled": true,
    "dutyCycle": 30,
    "speedVariationEnabled": false,
    "speedRandomization": 180,
    "doubleClickEnabled": false,
    "holdIntervalEnabled": false,
    "holdIntervalGapMs": 250,
    "clickLimitEnabled": false,
    "clickLimit": 42,
    "timeLimitEnabled": true,
    "timeLimit": 2,
    "timeLimitUnit": "m",
    "cornerStopEnabled": false,
    "cornerStopTL": 11, "cornerStopTR": 12, "cornerStopBL": 13, "cornerStopBR": 14,
    "edgeStopEnabled": true,
    "edgeStopTop": 21, "edgeStopBottom": 22, "edgeStopLeft": 23, "edgeStopRight": 24,
    "sequenceEnabled": true,
    "stopWhenComplete": true,
    "sequencePoints": [{"id": "p1", "x": 100.7, "y": -20, "clicks": 3, "radius": 5}, {"x": "bad"}],
    "stopZonesEnabled": true,
    "processListEnabled": true,
    "processListMode": "whitelist",
    "processListEntries": ["Minecraft", {"name": "Javaw.exe", "enabled": false}],
    "someFutureKey": {"ignored": true}
  }
}`

func TestImportBlurPreset(t *testing.T) {
	s := baseSettings()
	got, _, err := importPresetJSON(s, []byte(blurFixture), t0)
	if err != nil {
		t.Fatal(err)
	}
	pr := got.Presets[len(got.Presets)-1]
	if pr.Name != "Mining" || pr.ID == "6c1f0e0a-0000-4000-8000-000000000001" || pr.CreatedAt != "2026-10-26T12:00:00Z" {
		t.Fatalf("preset meta = %+v", pr)
	}
	p := pr.Profile
	if c := p.Cadence; c.Mode != CadenceRate || c.Rate != 12 || c.Unit != UnitMinute || c.IntervalMs != 5000 {
		t.Errorf("cadence = %+v", c)
	}
	if p.Input != InputKeyboard || p.Key != "E" || !p.Uppercase || p.Button != ButtonRight {
		t.Errorf("input = %s %q %v %s", p.Input, p.Key, p.Uppercase, p.Button)
	}
	if p.Press != PressClick || p.DutyPercent != 30 || p.HoldGapMs != 250 {
		t.Errorf("press = %s duty %v gap %v", p.Press, p.DutyPercent, p.HoldGapMs)
	}
	if p.Jitter.Enabled || p.Jitter.Percent != 100 {
		t.Errorf("jitter = %+v", p.Jitter)
	}
	if p.Limit.Kind != LimitTime || p.Limit.Seconds != 120 || p.Limit.Clicks != 42 {
		t.Errorf("limit = %+v", p.Limit)
	}
	if p.Corners != (Corners{Enabled: false, TL: 11, TR: 12, BL: 13, BR: 14}) {
		t.Errorf("corners = %+v", p.Corners)
	}
	if e := p.Edges; !e.Enabled || e.Top != 21 || e.Bottom != 22 || e.Left != 23 || e.Right != 24 {
		t.Errorf("edges = %+v", e)
	}
	cp := p.ClickPoints
	if !cp.Enabled || !cp.StopAfterPass || len(cp.Points) != 1 || cp.Points[0] != (ClickPoint{ID: "p1", X: 100, Y: -20, Clicks: 3, Radius: 5}) {
		t.Errorf("points = %+v", cp)
	}
	if !p.ZonesEnabled {
		t.Error("zones switch not imported")
	}
	want := []ProcessEntry{{Exe: "minecraft.exe", Enabled: true}, {Exe: "javaw.exe", Enabled: false}}
	if pl := p.ProcessList; !pl.Enabled || pl.Mode != ListAllow || len(pl.Entries) != 2 || pl.Entries[0] != want[0] || pl.Entries[1] != want[1] {
		t.Errorf("process list = %+v", pl)
	}
	// Settings-level fields are untouched by an import.
	if got.HotkeyMode != HotkeyToggle {
		t.Error("Blur's hotkey mode leaked into settings")
	}
}

func TestImportBlurNestedSnapshotAndHoldInterval(t *testing.T) {
	s := baseSettings()
	file := `{"type":"blur-autoclicker-preset","version":1,"preset":{"name":"Hold","settings":{
	  "clickSpeed": 2, "inputType": "mouse", "holdIntervalEnabled": true, "dutyCycleEnabled": false,
	  "clickLimitEnabled": true, "timeLimitEnabled": true, "keyboardKey": "numpad5"}}}`
	got, _, err := importPresetJSON(s, []byte(file), t0)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Presets[len(got.Presets)-1].Profile
	if p.Press != PressHold || p.DutyPercent != 0 || p.Limit.Kind != LimitClicks || p.Key != "Num5" {
		t.Fatalf("nested import = press %s duty %v limit %s key %q", p.Press, p.DutyPercent, p.Limit.Kind, p.Key)
	}
	if p.Cadence.Rate != 2 || p.Cadence.Unit != UnitSecond {
		t.Fatalf("cadence = %+v", p.Cadence)
	}
}

func TestImportRejectsBadFiles(t *testing.T) {
	s := baseSettings()
	for name, body := range map[string]string{
		"not json":   "{",
		"no marker":  `{"preset":{"name":"x"}}`,
		"other type": `{"type":"something-else","preset":{"name":"x"}}`,
		"empty blur": `{"type":"blur-autoclicker-preset","preset":{"name":"x","unrelated":1}}`,
	} {
		if _, _, err := importPresetJSON(s, []byte(body), t0); err == nil || !strings.Contains(err.Error(), "clicker: "+ErrImportFormat+": ") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestBlurKeyNames(t *testing.T) {
	cases := map[string]string{
		"a": "A", "keyz": "Z", "digit7": "7", "f5": "F5", "spacebar": "Space",
		"pgup": "PageUp", "arrowleft": "Left", "numpad0": "Num0", "numpadadd": "Num+",
		"slash": "/", "grave": "`", "del": "Delete",
	}
	for in, want := range cases {
		if got := blurKeyName(in); got != want {
			t.Errorf("blurKeyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExportFileName(t *testing.T) {
	if got := exportFileName(`a/b:c*d?. `); got != "a_b_c_d_.json" {
		t.Fatalf("got %q", got)
	}
	if got := exportFileName("  "); got != "preset.json" {
		t.Fatalf("got %q", got)
	}
}

// Through the service: export writes where the dialog says; import reads a file
// the dialog picked; cancel changes nothing.
func TestServiceExportImport(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "rapid.json")
	var suggested string
	r.s.d.saveDialog = func(_, name string) (string, error) { suggested = name; return out, nil }
	path, err := r.s.ExportPreset("factory-rapid")
	if err != nil || path != out || suggested != "Rapid Fire.json" {
		t.Fatalf("export: %q %v suggested %q", path, err, suggested)
	}
	blur := filepath.Join(dir, "blur.json")
	if err := os.WriteFile(blur, []byte(blurFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	r.s.d.openDialog = func(string) (string, error) { return blur, nil }
	got, err := r.s.ImportPreset()
	if err != nil || len(got.Presets) != 4 || got.Presets[3].Name != "Mining" {
		t.Fatalf("import: %v %+v", err, got.Presets)
	}
	r.s.d.openDialog = func(string) (string, error) { return "", nil }
	got, err = r.s.ImportPreset()
	if err != nil || len(got.Presets) != 4 {
		t.Fatalf("cancelled import: %v %d", err, len(got.Presets))
	}
	if _, err := r.s.ExportPreset("nope"); err == nil {
		t.Fatal("export of a missing preset succeeded")
	}
}
