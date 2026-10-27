package clicker

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// FactoryPresets are seeded on the first load (no "presets" array in
// clicker.json yet). Ids are fixed so a re-seed is recognisable.
func FactoryPresets() []Preset {
	stamp := time.Now().UTC().Format(time.RFC3339)
	standard := DefaultProfile()

	rapid := DefaultProfile()
	rapid.Cadence = Cadence{Mode: CadenceRate, Rate: 100, Unit: UnitSecond, IntervalMs: 10}
	rapid.DutyPercent = 20
	rapid.Jitter.Percent = 15
	rapid.Limit = Limit{Kind: LimitClicks, Clicks: 5000, Seconds: 60}

	precision := DefaultProfile()
	precision.Cadence = Cadence{Mode: CadenceRate, Rate: 10, Unit: UnitSecond, IntervalMs: 100}
	precision.DutyPercent = 60
	precision.Jitter.Percent = 10

	mk := func(id, name string, p Profile) Preset {
		return Preset{ID: id, Name: name, CreatedAt: stamp, UpdatedAt: stamp, Profile: p}
	}
	return []Preset{
		mk("factory-standard", "Standard Clicking", standard),
		mk("factory-rapid", "Rapid Fire", rapid),
		mk("factory-precision", "Precision Mode", precision),
	}
}

// PresetModified reports whether the profile differs from the active preset's
// snapshot (the page's "Modified" badge). False when no preset is active.
func PresetModified(s Settings) bool {
	i := presetIndex(s.Presets, s.ActivePresetID)
	if i < 0 {
		return false
	}
	return !reflect.DeepEqual(
		normalizeProfile(s.Presets[i].Profile, s.HighRate),
		normalizeProfile(s.Profile, s.HighRate),
	)
}

// validPresetName checks a user-typed name against the others (exceptID is the
// preset being renamed).
func validPresetName(ps []Preset, name, exceptID string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", clickerErr(ErrPresetName, "Name cannot be empty")
	case len([]rune(name)) > maxPresetName:
		return "", clickerErrf(ErrPresetName, "Names can be at most %d characters", maxPresetName)
	case nameTaken(ps, name, exceptID):
		return "", clickerErr(ErrPresetName, "A preset with this name already exists")
	}
	return name, nil
}

// The preset operations below are pure: they take normalized settings, return
// the changed copy, and leave locking, saving and emitting to the service.

func savePreset(s Settings, name string, now time.Time) (Settings, error) {
	name, err := validPresetName(s.Presets, name, "")
	if err != nil {
		return s, err
	}
	stamp := now.UTC().Format(time.RFC3339)
	p := Preset{ID: newID(), Name: name, CreatedAt: stamp, UpdatedAt: stamp, Profile: s.Profile}
	s.Presets = append(append([]Preset{}, s.Presets...), p)
	s.ActivePresetID = p.ID
	return Normalize(s), nil
}

func applyPreset(s Settings, id string) (Settings, error) {
	i := presetIndex(s.Presets, id)
	if i < 0 {
		return s, clickerErr(ErrPresetNotFound, "That preset no longer exists")
	}
	s.Profile = s.Presets[i].Profile
	s.ActivePresetID = id
	return Normalize(s), nil
}

func updatePreset(s Settings, id string, now time.Time) (Settings, error) {
	i := presetIndex(s.Presets, id)
	if i < 0 {
		return s, clickerErr(ErrPresetNotFound, "That preset no longer exists")
	}
	s.Presets = append([]Preset{}, s.Presets...)
	s.Presets[i].Profile = s.Profile
	s.Presets[i].UpdatedAt = now.UTC().Format(time.RFC3339)
	s.ActivePresetID = id
	return Normalize(s), nil
}

func renamePreset(s Settings, id, name string, now time.Time) (Settings, error) {
	i := presetIndex(s.Presets, id)
	if i < 0 {
		return s, clickerErr(ErrPresetNotFound, "That preset no longer exists")
	}
	name, err := validPresetName(s.Presets, name, id)
	if err != nil {
		return s, err
	}
	s.Presets = append([]Preset{}, s.Presets...)
	s.Presets[i].Name = name
	s.Presets[i].UpdatedAt = now.UTC().Format(time.RFC3339)
	return Normalize(s), nil
}

// duplicatePreset copies a preset as "Name (2)", "Name (3)", ... placed right
// after the original. The active preset does not change.
func duplicatePreset(s Settings, id string, now time.Time) (Settings, error) {
	i := presetIndex(s.Presets, id)
	if i < 0 {
		return s, clickerErr(ErrPresetNotFound, "That preset no longer exists")
	}
	stamp := now.UTC().Format(time.RFC3339)
	cp := s.Presets[i]
	cp.ID = newID()
	cp.Name = uniqueName(s.Presets, stripCopySuffix(cp.Name), "")
	cp.CreatedAt, cp.UpdatedAt = stamp, stamp
	ps := make([]Preset, 0, len(s.Presets)+1)
	ps = append(ps, s.Presets[:i+1]...)
	ps = append(ps, cp)
	s.Presets = append(ps, s.Presets[i+1:]...)
	return Normalize(s), nil
}

func deletePreset(s Settings, id string) (Settings, error) {
	i := presetIndex(s.Presets, id)
	if i < 0 {
		return s, clickerErr(ErrPresetNotFound, "That preset no longer exists")
	}
	ps := make([]Preset, 0, len(s.Presets)-1)
	ps = append(ps, s.Presets[:i]...)
	s.Presets = append(ps, s.Presets[i+1:]...)
	if s.ActivePresetID == id {
		s.ActivePresetID = ""
	}
	return Normalize(s), nil
}

// presetFile is the export envelope. Type is PresetFileMarker.
type presetFile struct {
	Type    string `json:"type"`
	Version int    `json:"version"`
	Preset  Preset `json:"preset"`
}

// exportPresetJSON renders a preset file.
func exportPresetJSON(p Preset) ([]byte, error) {
	return json.MarshalIndent(presetFile{Type: PresetFileMarker, Version: 1, Preset: p}, "", "  ")
}

// exportFileName is "<name>.json" with characters Windows forbids replaced.
func exportFileName(name string) string {
	clean := strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	clean = strings.TrimRight(clean, ". ")
	if clean == "" {
		clean = "preset"
	}
	return clean + ".json"
}

// importPresetJSON parses a Toru or Blur AutoClicker preset file. Fields the
// file does not carry come from base (the current profile). The result has a
// fresh id and timestamps, a free name, and is not made active.
func importPresetJSON(s Settings, data []byte, now time.Time) (Settings, string, error) {
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	var env struct {
		Type   string          `json:"type"`
		Preset json.RawMessage `json:"preset"`
	}
	if err := json.Unmarshal(data, &env); err != nil || len(env.Preset) == 0 {
		return s, "", clickerErr(ErrImportFormat, "That file is not a preset file")
	}
	var name string
	var prof Profile
	switch env.Type {
	case PresetFileMarker:
		p := Preset{Profile: s.Profile}
		if err := json.Unmarshal(env.Preset, &p); err != nil {
			return s, "", clickerErr(ErrImportFormat, "The preset in that file is damaged")
		}
		name, prof = p.Name, p.Profile
	case BlurPresetMarker:
		var err error
		name, prof, err = blurPreset(env.Preset, s.Profile)
		if err != nil {
			return s, "", err
		}
	default:
		return s, "", clickerErr(ErrImportFormat, "That file is not a Toru or Blur AutoClicker preset")
	}
	name = cleanPresetName(name)
	if name == "" {
		name = "Imported preset"
	}
	stamp := now.UTC().Format(time.RFC3339)
	p := Preset{
		ID:        newID(),
		Name:      uniqueName(s.Presets, name, ""),
		CreatedAt: stamp,
		UpdatedAt: stamp,
		Profile:   prof,
	}
	s.Presets = append(append([]Preset{}, s.Presets...), p)
	return Normalize(s), p.ID, nil
}
