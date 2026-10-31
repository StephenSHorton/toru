// Package fx is the v3 editor's effect registry on the Go side
// (docs/video-editor-v3.md, Effect registry): one JSON definition per effect
// type in defs/ (embedded), which the frontend loads too
// (frontend/src/trim/fx/def.ts has the same shape), and per type a filter
// builder for the LGPL ffmpeg export.
package fx

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
)

// Def is one effect type's definition: its params and how the preview and
// export treat it. Field meanings match EffectDef in
// frontend/src/trim/fx/def.ts.
type Def struct {
	Type     string     `json:"type"`     // registry id, lowerCamel, = the file name
	Version  int        `json:"version"`  // bumped when params change meaning
	Name     string     `json:"name"`     // app text
	Category string     `json:"category"` // adjust | color | blur | stylize | distort | key | light
	Kind     string     `json:"kind"`     // pixel | neighbourhood | geometry
	Params   []ParamDef `json:"params"`
	Export   struct {
		Filters []string `json:"filters"` // ffmpeg filters the builder may emit
	} `json:"export"`
	Parity Parity `json:"parity"`
	Amount string `json:"amount"`           // native | blend | none
	Hidden bool   `json:"hidden,omitempty"` // not shown in the effect browser
}

// ParamDef is one parameter. Min/Max/Step/Unit/Display apply to numbers,
// Options to enums, Accept to files. Animate is "cmd", "expr" or false
// (JSON false decodes to "").
type ParamDef struct {
	ID      string          `json:"id"`
	Label   string          `json:"label"`
	Type    string          `json:"type"` // number | boolean | color | enum | point | file
	Default json.RawMessage `json:"default"`
	Animate Animate         `json:"animate"`
	Hint    string          `json:"hint,omitempty"`
	Min     *float64        `json:"min,omitempty"`
	Max     *float64        `json:"max,omitempty"`
	Step    float64         `json:"step,omitempty"`
	Unit    string          `json:"unit,omitempty"`
	Display string          `json:"display,omitempty"` // "percent"
	Options []Option        `json:"options,omitempty"`
	Accept  []string        `json:"accept,omitempty"`
}

// Option is one choice of an enum param.
type Option struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Parity is how the parity check compares preview and export.
type Parity struct {
	Mode   string  `json:"mode"` // pixels | stats | none
	Mean   float64 `json:"mean,omitempty"`
	P99    float64 `json:"p99,omitempty"`
	Reason string  `json:"reason,omitempty"`
	// Border is how far (pixels of a 1080-line frame) the effect reads past
	// a pixel, so the check skips that much of the frame's edge, where the
	// two sides treat outside pixels differently; 0 = the default for its
	// kind. T is the clip-local time (s) generated cases are drawn at, for
	// effects that only show inside a time window; 0 = the clip's start.
	Border float64 `json:"border,omitempty"`
	T      float64 `json:"t,omitempty"`
	// Random lists number params whose output is random on both sides (a
	// noise amount): generated cases set them to 0 to compare pixels, and a
	// case with one on is compared by statistics.
	Random []string `json:"random,omitempty"`
}

// Animate is how a param is keyframed in the export: "cmd" (sendcmd),
// "expr" (an expression in t) or "" (not animatable; JSON false).
type Animate string

// UnmarshalJSON takes "cmd", "expr" or false.
func (a *Animate) UnmarshalJSON(b []byte) error {
	switch string(b) {
	case "false", "null":
		*a = ""
		return nil
	case `"cmd"`, `"expr"`:
		*a = Animate(b[1 : len(b)-1])
		return nil
	}
	return fmt.Errorf(`animate must be "cmd", "expr" or false, not %s`, b)
}

// MarshalJSON writes "" back as false.
func (a Animate) MarshalJSON() ([]byte, error) {
	if a == "" {
		return []byte("false"), nil
	}
	return json.Marshal(string(a))
}

var (
	idPattern  = regexp.MustCompile(`^[a-z][A-Za-z0-9]{0,39}$`)
	categories = []string{"adjust", "color", "blur", "stylize", "distort", "key", "light"}
	kinds      = []string{"pixel", "neighbourhood", "geometry"}
	amounts    = []string{"native", "blend", "none"}
	colorHex   = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// ParseDef reads and checks one definition file.
func ParseDef(b []byte) (Def, error) {
	var d Def
	if err := json.Unmarshal(b, &d); err != nil {
		return d, err
	}
	return d, d.Validate()
}

// Validate checks a definition: ids, enums, number ranges and that every
// default fits its param.
func (d Def) Validate() error {
	switch {
	case !idPattern.MatchString(d.Type):
		return fmt.Errorf("type %q: want lowerCamel letters and digits", d.Type)
	case d.Version < 1:
		return fmt.Errorf("%s: version must be at least 1", d.Type)
	case d.Name == "":
		return fmt.Errorf("%s: no name", d.Type)
	case !slices.Contains(categories, d.Category):
		return fmt.Errorf("%s: unknown category %q", d.Type, d.Category)
	case !slices.Contains(kinds, d.Kind):
		return fmt.Errorf("%s: unknown kind %q", d.Type, d.Kind)
	case !slices.Contains(amounts, d.Amount):
		return fmt.Errorf("%s: amount must be native, blend or none", d.Type)
	case len(d.Export.Filters) == 0:
		return fmt.Errorf("%s: export.filters is empty", d.Type)
	}
	switch d.Parity.Mode {
	case "pixels":
		if d.Parity.Mean <= 0 || d.Parity.P99 <= 0 {
			return fmt.Errorf("%s: pixel parity needs mean and p99", d.Type)
		}
	case "stats":
	case "none":
		if d.Parity.Reason == "" {
			return fmt.Errorf("%s: parity none needs a reason", d.Type)
		}
	default:
		return fmt.Errorf("%s: parity mode must be pixels, stats or none", d.Type)
	}
	seen := map[string]bool{}
	for _, p := range d.Params {
		if !idPattern.MatchString(p.ID) || seen[p.ID] {
			return fmt.Errorf("%s: bad or repeated param id %q", d.Type, p.ID)
		}
		seen[p.ID] = true
		if p.Label == "" {
			return fmt.Errorf("%s.%s: no label", d.Type, p.ID)
		}
		if err := p.checkDefault(); err != nil {
			return fmt.Errorf("%s.%s: %w", d.Type, p.ID, err)
		}
	}
	return nil
}

func (p ParamDef) checkDefault() error {
	switch p.Type {
	case "number":
		var v float64
		if p.Min == nil || p.Max == nil || *p.Min >= *p.Max {
			return fmt.Errorf("number needs min < max")
		}
		if err := json.Unmarshal(p.Default, &v); err != nil || v < *p.Min || v > *p.Max {
			return fmt.Errorf("default %s isn't a number in %g..%g", p.Default, *p.Min, *p.Max)
		}
	case "boolean":
		var v bool
		if err := json.Unmarshal(p.Default, &v); err != nil {
			return fmt.Errorf("default %s isn't a boolean", p.Default)
		}
	case "color":
		var v string
		if err := json.Unmarshal(p.Default, &v); err != nil || !colorHex.MatchString(v) {
			return fmt.Errorf(`default %s isn't "#rrggbb"`, p.Default)
		}
	case "enum":
		var v string
		ok := json.Unmarshal(p.Default, &v) == nil && slices.ContainsFunc(p.Options, func(o Option) bool { return o.ID == v })
		if len(p.Options) == 0 || !ok {
			return fmt.Errorf("enum default %s isn't one of its options", p.Default)
		}
	case "point":
		var v [2]float64
		if err := json.Unmarshal(p.Default, &v); err != nil || v[0] < 0 || v[0] > 1 || v[1] < 0 || v[1] > 1 {
			return fmt.Errorf("default %s isn't [x, y] in 0..1", p.Default)
		}
	case "file":
		var v string
		if err := json.Unmarshal(p.Default, &v); err != nil || len(p.Accept) == 0 {
			return fmt.Errorf("file needs a string default and accept")
		}
	default:
		return fmt.Errorf("unknown param type %q", p.Type)
	}
	if p.Animate != "" && p.Type != "number" && p.Type != "color" && p.Type != "point" {
		return fmt.Errorf("only number, color and point params can be keyframed")
	}
	return nil
}
