package fx

import (
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"path"
	"slices"
	"sort"
	"strings"
)

// registry.go loads the effect definitions in defs/ (embedded; the frontend
// imports the same files, frontend/src/trim/fx/registry.ts) and clamps
// params to them, so both sides apply the same rules.

//go:embed defs/*.json
var defFiles embed.FS

var defs = mustLoadDefs()

func mustLoadDefs() map[string]Def {
	out, err := loadDefs()
	if err != nil {
		panic(err)
	}
	return out
}

func loadDefs() (map[string]Def, error) {
	entries, err := defFiles.ReadDir("defs")
	if err != nil {
		return nil, err
	}
	out := map[string]Def{}
	for _, e := range entries {
		b, err := defFiles.ReadFile("defs/" + e.Name())
		if err != nil {
			return nil, err
		}
		d, err := ParseDef(b)
		if err != nil {
			return nil, fmt.Errorf("defs/%s: %w", e.Name(), err)
		}
		if want := strings.TrimSuffix(e.Name(), path.Ext(e.Name())); d.Type != want {
			return nil, fmt.Errorf("defs/%s: type %q must match the file name", e.Name(), d.Type)
		}
		out[d.Type] = d
	}
	return out, nil
}

// Defs is every definition, sorted by type.
func Defs() []Def {
	out := make([]Def, 0, len(defs))
	for _, d := range defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Lookup is the definition of an effect type.
func Lookup(typ string) (Def, bool) {
	d, ok := defs[typ]
	return d, ok
}

// Param is a definition's param by id.
func (d Def) Param(id string) (ParamDef, bool) {
	i := slices.IndexFunc(d.Params, func(p ParamDef) bool { return p.ID == id })
	if i < 0 {
		return ParamDef{}, false
	}
	return d.Params[i], true
}

// Resolve is an instance's params with every param of the definition
// present: given values clamped to their param (numbers into min..max,
// colours, enums and points checked), and missing or unreadable ones at the
// default. Params the definition doesn't have are dropped. Values are
// float64, bool, string or [2]float64.
func (d Def) Resolve(params map[string]any) map[string]any {
	out := make(map[string]any, len(d.Params))
	for _, p := range d.Params {
		v, ok := p.Clamp(params[p.ID])
		if !ok {
			v = p.DefaultValue()
		}
		out[p.ID] = v
	}
	return out
}

// DefaultValue is the param's default (float64, bool, string or [2]float64).
func (p ParamDef) DefaultValue() any {
	switch p.Type {
	case "number":
		var v float64
		_ = json.Unmarshal(p.Default, &v)
		return v
	case "boolean":
		var v bool
		_ = json.Unmarshal(p.Default, &v)
		return v
	case "point":
		var v [2]float64
		_ = json.Unmarshal(p.Default, &v)
		return v
	default:
		var v string
		_ = json.Unmarshal(p.Default, &v)
		return v
	}
}

// Clamp fits v to the param; ok is false when v can't be read as the
// param's type (then the caller uses the default). Colours come back as
// lower-case "#rrggbb".
func (p ParamDef) Clamp(v any) (any, bool) {
	switch p.Type {
	case "number":
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) {
			return nil, false
		}
		return math.Max(*p.Min, math.Min(*p.Max, f)), true
	case "boolean":
		b, ok := v.(bool)
		return b, ok
	case "color":
		s, ok := v.(string)
		if !ok || !colorHex.MatchString(s) {
			return nil, false
		}
		return strings.ToLower(s), true
	case "enum":
		s, ok := v.(string)
		if !ok || !slices.ContainsFunc(p.Options, func(o Option) bool { return o.ID == s }) {
			return nil, false
		}
		return s, true
	case "point":
		var xy [2]float64
		switch a := v.(type) {
		case [2]float64:
			xy = a
		case []any:
			if len(a) != 2 {
				return nil, false
			}
			for i := range a {
				f, ok := a[i].(float64)
				if !ok || math.IsNaN(f) {
					return nil, false
				}
				xy[i] = f
			}
		default:
			return nil, false
		}
		return [2]float64{math.Max(0, math.Min(1, xy[0])), math.Max(0, math.Min(1, xy[1]))}, true
	case "file":
		s, ok := v.(string)
		return s, ok
	}
	return nil, false
}
