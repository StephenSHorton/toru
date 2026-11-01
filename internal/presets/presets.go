// Package presets keeps the video editor's saved filters ("My filters") and
// imported LUTs (docs/video-editor-v3.md, User presets).
//
// A preset is a named effect stack, one JSON file per preset in
// %AppData%\toru\presets\<id>.json. The editor saves a clip's enabled
// effects as one and applies it back as a group with an intensity. Export
// and import use a .torufx file (the same JSON), so presets can be shared.
// v2's looks (bw, sepia, vintage, cool, warm) are read-only built-in presets.
//
// Saved text styles (the Text library's "My styles") are presets too, of
// kind "text": a text overlay's style fields instead of effects. They share
// the folder and the .torufx format; List is filters only and
// ListTextStyles is text styles only.
//
// Imported .cube LUTs are copied into %AppData%\toru\luts (fx.LutDir) and
// used by the lut effect, which names one by its file name.
package presets

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/StephenSHorton/toru/internal/fx"
)

// Version is the preset file format's version.
const Version = 1

// Keyframe is one key of a preset effect's param. T is a FRACTION (0..1) of
// the clip's length, not seconds, so a preset fits a clip of any length.
type Keyframe struct {
	T    float64 `json:"t"`
	V    any     `json:"v"`
	Ease string  `json:"ease"`
}

// Effect is one effect of a preset: an EffectInstance without its id (the
// editor gives each a new one when applying).
type Effect struct {
	Type    string                `json:"type"`
	Enabled bool                  `json:"enabled"`
	Params  map[string]any        `json:"params"`
	Keys    map[string][]Keyframe `json:"keys,omitempty"`
}

// Kinds of preset.
const (
	KindFilter = ""     // an effect stack (Effects)
	KindText   = "text" // a text overlay's style (Style)
)

// Preset is a saved filter or text style.
type Preset struct {
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Kind      string    `json:"kind,omitempty"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	Effects   []Effect  `json:"effects,omitempty"`
	// Style is a text style's fields (the editor's TextStyle, without its
	// opacity keys). Only for kind "text".
	Style map[string]any `json:"style,omitempty"`
	// Thumb is a small JPEG data URL of the frame it was saved from ("" if
	// none).
	Thumb string `json:"thumb,omitempty"`
	// Builtin marks the shipped looks: they can't be renamed or deleted.
	// Never written to a file.
	Builtin bool `json:"builtin,omitempty"`
}

// Lut is an imported LUT.
type Lut struct {
	// ID is its file name in the LUT folder, what a lut effect's "lut"
	// param holds.
	ID string `json:"id"`
	// Name is the file's TITLE, or its file name without .cube.
	Name string `json:"name"`
	Size int    `json:"size"`
}

// MaxNameLen is the longest preset name, in characters.
const MaxNameLen = 80

// maxFileBytes caps preset and LUT files read from disk (a 65³ LUT is about
// 7 MB of text).
const maxFileBytes = 16 << 20

// presetIDPattern: ids come from the frontend (newId("pre")) or are made
// here: letters, digits and dashes, so an id can never name a path outside
// the folder.
var presetIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)

// Dir is %AppData%\toru\presets. A var so tests can point it elsewhere.
var Dir = func() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru", "presets"), nil
}

// Service is the presets' Wails service.
type Service struct{}

// NewService returns the service.
func NewService() *Service { return &Service{} }

// builtinLooks are v2's looks as presets, in the Looks order.
var builtinLooks = []struct{ look, name string }{
	{"bw", "Black & white"},
	{"sepia", "Sepia"},
	{"vintage", "Vintage"},
	{"cool", "Cool"},
	{"warm", "Warm"},
}

// BuiltinPrefix starts every built-in preset's id.
const BuiltinPrefix = "builtin-"

// Builtins are the shipped, read-only presets: one look effect each.
func Builtins() []Preset {
	out := make([]Preset, 0, len(builtinLooks))
	for _, l := range builtinLooks {
		out = append(out, Preset{
			Version: Version,
			ID:      BuiltinPrefix + l.look,
			Name:    l.name,
			Effects: []Effect{{Type: "look", Enabled: true, Params: map[string]any{"look": l.look}}},
			Builtin: true,
		})
	}
	return out
}

func builtin(id string) (Preset, bool) {
	for _, p := range Builtins() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

func presetPath(id string) (string, error) {
	if !presetIDPattern.MatchString(id) || strings.HasPrefix(id, BuiltinPrefix) {
		return "", fmt.Errorf("bad preset id %q", id)
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// maxStyleBytes caps a text style's JSON (a style is a few dozen fields).
const maxStyleBytes = 16 << 10

// noun is what the person calls a preset of kind k.
func noun(k string) string {
	if k == KindText {
		return "text style"
	}
	return "filter"
}

// normalize checks a preset and fits it to what's stored: a trimmed name,
// known effect types, keyframe times in 0..1 (or, for a text style, a style
// object). It doesn't touch the id.
func normalize(p *Preset) error {
	if p.Kind != KindFilter && p.Kind != KindText {
		return fmt.Errorf("unknown preset kind %q (this file may be from a newer Toru)", p.Kind)
	}
	what := noun(p.Kind)
	if p.Version != Version {
		if p.Version > Version {
			return fmt.Errorf("this %s was made with a newer Toru", what)
		}
		return fmt.Errorf("unsupported %s version %d", what, p.Version)
	}
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return fmt.Errorf("give the %s a name", what)
	}
	if len([]rune(p.Name)) > MaxNameLen {
		return fmt.Errorf("that name is too long (%d characters at most)", MaxNameLen)
	}
	p.Builtin = false
	if p.Kind == KindText {
		return normalizeText(p)
	}
	p.Style = nil
	if len(p.Effects) == 0 {
		return errors.New("a filter needs at least one effect")
	}
	for i := range p.Effects {
		e := &p.Effects[i]
		if _, ok := fx.Lookup(e.Type); !ok {
			return fmt.Errorf("unknown effect %q (this filter may be from a newer Toru)", e.Type)
		}
		if e.Params == nil {
			e.Params = map[string]any{}
		}
		for id, keys := range e.Keys {
			for j := range keys {
				t := keys[j].T
				if t != t || t < 0 {
					t = 0
				} else if t > 1 {
					t = 1
				}
				keys[j].T = t
			}
			if len(keys) == 0 {
				delete(e.Keys, id)
			}
		}
	}
	if !strings.HasPrefix(p.Thumb, "data:image/") || len(p.Thumb) > 256<<10 {
		p.Thumb = ""
	}
	return nil
}

// normalizeText checks a text style preset: a style object (it may be
// empty: the default look), no effects, no thumbnail (the Text library
// draws its own previews), and no opacity keyframes (a style is a look,
// not an animation).
func normalizeText(p *Preset) error {
	if len(p.Effects) > 0 {
		return errors.New("a text style can't have effects")
	}
	if p.Style == nil {
		p.Style = map[string]any{}
	}
	delete(p.Style, "opacityKeys")
	b, err := json.Marshal(p.Style)
	if err != nil {
		return fmt.Errorf("bad text style: %w", err)
	}
	if len(b) > maxStyleBytes {
		return errors.New("that text style is too big")
	}
	p.Effects = nil
	p.Thumb = ""
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readPreset(path string) (Preset, error) {
	var p Preset
	info, err := os.Stat(path)
	if err != nil {
		return p, err
	}
	if info.Size() > maxFileBytes {
		return p, errors.New("that file is too big to be a filter")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("not a Toru preset file: %w", err)
	}
	return p, normalize(&p)
}

// List is the built-in looks, then the saved filters, newest first.
// Unreadable files are skipped. Text styles aren't in it (ListTextStyles).
func (s *Service) List() ([]Preset, error) {
	saved, err := savedOf(KindFilter)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(saved, func(i, j int) bool { return saved[i].CreatedAt.After(saved[j].CreatedAt) })
	return append(Builtins(), saved...), nil
}

// ListTextStyles is the saved text styles, oldest first (the order the Text
// library shows them in). The built-in styles live in the editor.
func (s *Service) ListTextStyles() ([]Preset, error) {
	saved, err := savedOf(KindText)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(saved, func(i, j int) bool {
		if !saved[i].CreatedAt.Equal(saved[j].CreatedAt) {
			return saved[i].CreatedAt.Before(saved[j].CreatedAt)
		}
		return saved[i].ID < saved[j].ID
	})
	if saved == nil {
		saved = []Preset{}
	}
	return saved, nil
}

// savedOf reads the saved presets of one kind. Unreadable files are skipped.
func savedOf(kind string) ([]Preset, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var saved []Preset
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		p, err := readPreset(filepath.Join(dir, name))
		if err != nil || p.ID != strings.TrimSuffix(name, ".json") || p.Kind != kind {
			continue
		}
		saved = append(saved, p)
	}
	return saved, nil
}

// Get is one preset (built-in or saved) by id.
func (s *Service) Get(id string) (Preset, error) {
	if p, ok := builtin(id); ok {
		return p, nil
	}
	path, err := presetPath(id)
	if err != nil {
		return Preset{}, err
	}
	p, err := readPreset(path)
	if errors.Is(err, os.ErrNotExist) {
		return Preset{}, fmt.Errorf("no preset %q", id)
	}
	return p, err
}

// Save writes a preset (the frontend's JSON) and returns it as stored. An
// empty id gets a new one; createdAt defaults to now.
func (s *Service) Save(doc string) (Preset, error) {
	var p Preset
	if err := json.Unmarshal([]byte(doc), &p); err != nil {
		return Preset{}, fmt.Errorf("preset isn't valid JSON: %w", err)
	}
	if p.Version == 0 {
		p.Version = Version
	}
	if p.ID == "" {
		p.ID = newID()
	}
	if err := normalize(&p); err != nil {
		return Preset{}, err
	}
	path, err := presetPath(p.ID)
	if err != nil {
		return Preset{}, err
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	return p, writeJSON(path, p)
}

// Rename gives a saved preset a new name.
func (s *Service) Rename(id, name string) (Preset, error) {
	if _, ok := builtin(id); ok {
		return Preset{}, errors.New("built-in filters can't be renamed")
	}
	p, err := s.Get(id)
	if err != nil {
		return Preset{}, err
	}
	p.Name = name
	if err := normalize(&p); err != nil {
		return Preset{}, err
	}
	path, err := presetPath(id)
	if err != nil {
		return Preset{}, err
	}
	return p, writeJSON(path, p)
}

// Delete removes a saved preset. Clips it was applied to keep their effects.
func (s *Service) Delete(id string) error {
	if _, ok := builtin(id); ok {
		return errors.New("built-in filters can't be deleted")
	}
	path, err := presetPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no preset %q", id)
		}
		return err
	}
	return nil
}

// Import reads a .torufx file and saves it as a new preset (a fresh id, so
// importing the same file twice gives two copies rather than overwriting).
// The file says its kind: a text style lands in the Text library's My
// styles, a filter in My filters.
func (s *Service) Import(path string) (Preset, error) {
	return s.importAs(path, nil)
}

// ImportTextStyle is Import for the Text library: the file must be a text
// style.
func (s *Service) ImportTextStyle(path string) (Preset, error) {
	k := KindText
	return s.importAs(path, &k)
}

// importAs imports path; a non-nil kind must match the file's.
func (s *Service) importAs(path string, kind *string) (Preset, error) {
	p, err := readPreset(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Preset{}, fmt.Errorf("%s doesn't exist", path)
		}
		return Preset{}, err
	}
	if kind != nil && p.Kind != *kind {
		return Preset{}, fmt.Errorf("that file is a %s, not a %s: import it in the %s", noun(p.Kind), noun(*kind), libraryOf(p.Kind))
	}
	p.ID = newID()
	p.CreatedAt = time.Now().UTC()
	dst, err := presetPath(p.ID)
	if err != nil {
		return Preset{}, err
	}
	return p, writeJSON(dst, p)
}

// Export writes a preset to path as a .torufx file (the extension is added
// when missing). An existing file is never overwritten. Returns the path
// written.
func (s *Service) Export(id, path string) (string, error) {
	return s.export(id, path, false)
}

// export writes the .torufx; overwrite is for a path the person already
// confirmed in the Save dialog.
func (s *Service) export(id, path string, overwrite bool) (string, error) {
	p, err := s.Get(id)
	if err != nil {
		return "", err
	}
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("give an absolute path for the .torufx file")
	}
	if !strings.EqualFold(filepath.Ext(path), ".torufx") {
		path += ".torufx"
	}
	if _, err := os.Stat(path); err == nil && !overwrite {
		return "", fmt.Errorf("%s already exists", path)
	}
	p.Builtin = false
	if strings.HasPrefix(p.ID, BuiltinPrefix) {
		p.CreatedAt = time.Now().UTC()
	}
	return path, writeJSON(path, p)
}

// libraryOf is where the editor lists presets of kind k.
func libraryOf(k string) string {
	if k == KindText {
		return "Text library (My styles)"
	}
	return "Filters tab (My filters)"
}

// newID is a preset id: "pre-" and the time in hex, unique within this
// process and sortable.
var lastID string

func newID() string {
	id := "pre-" + strings.ToLower(fmt.Sprintf("%x", time.Now().UnixNano()))
	if id <= lastID {
		id = lastID + "0"
	}
	lastID = id
	return id
}

// --- LUTs -------------------------------------------------------------------

// ListLuts is the imported LUTs, by name. Unreadable files are skipped.
func (s *Service) ListLuts() ([]Lut, error) {
	dir, err := fx.LutDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Lut{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Lut{}
	for _, e := range entries {
		if e.IsDir() || !fx.LutPattern.MatchString(e.Name()) {
			continue
		}
		c, err := fx.LoadLut(e.Name())
		if err != nil {
			continue
		}
		out = append(out, lutOf(e.Name(), c))
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

func lutOf(id string, c *fx.Cube) Lut {
	name := strings.TrimSpace(c.Title)
	if name == "" {
		name = strings.TrimSuffix(id, filepath.Ext(id))
	}
	return Lut{ID: id, Name: name, Size: c.Size}
}

var (
	lutNameJunk  = regexp.MustCompile(`[^A-Za-z0-9 _.()+-]+`)
	lutNameStart = regexp.MustCompile(`^[A-Za-z0-9]`)
	lutDots      = regexp.MustCompile(`\.{2,}`)
)

// ImportLut checks a .cube file and copies it into the LUT folder. A file
// whose name is taken gets " (2)", " (3)"... unless it's the same LUT,
// which is returned as is.
func (s *Service) ImportLut(path string) (Lut, error) {
	if !strings.EqualFold(filepath.Ext(path), ".cube") {
		return Lut{}, errors.New("pick a .cube file")
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Lut{}, fmt.Errorf("%s doesn't exist", path)
		}
		return Lut{}, err
	}
	if info.Size() > maxFileBytes {
		return Lut{}, errors.New("that file is too big for a LUT")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Lut{}, err
	}
	c, err := fx.ParseCube(string(b))
	if err != nil {
		return Lut{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	dir, err := fx.LutDir()
	if err != nil {
		return Lut{}, err
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = strings.Trim(strings.Join(strings.Fields(lutNameJunk.ReplaceAllString(base, " ")), " "), " .")
	base = lutDots.ReplaceAllString(base, ".")
	if r := []rune(base); len(r) > 80 {
		base = strings.TrimSpace(string(r[:80]))
	}
	if base == "" || !lutNameStart.MatchString(base) {
		base = "LUT " + base
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Lut{}, err
	}
	for n := 1; n < 1000; n++ {
		id := base + ".cube"
		if n > 1 {
			id = fmt.Sprintf("%s (%d).cube", base, n)
		}
		dst := filepath.Join(dir, id)
		old, err := os.ReadFile(dst)
		if err == nil {
			if string(old) == string(b) {
				return lutOf(id, c), nil
			}
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return Lut{}, err
		}
		if err := writeFile(dst, b); err != nil {
			return Lut{}, err
		}
		return lutOf(id, c), nil
	}
	return Lut{}, errors.New("too many LUTs with that name")
}

func writeFile(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ReadLut is an imported LUT's .cube text (the preview parses it; the
// folder isn't reachable from the page otherwise).
func (s *Service) ReadLut(id string) (string, error) {
	p, err := fx.LutPath(id)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("the LUT %q isn't imported on this PC", id)
	}
	return string(b), err
}

// DeleteLut removes an imported LUT. Clips that use it show it as missing
// (and draw without it).
func (s *Service) DeleteLut(id string) error {
	p, err := fx.LutPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no LUT %q", id)
		}
		return err
	}
	return nil
}
