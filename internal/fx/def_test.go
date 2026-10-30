package fx

import (
	"encoding/json"
	"strings"
	"testing"
)

const goodDef = `{
  "type": "rgbSplit", "version": 1, "name": "RGB split", "category": "stylize", "kind": "neighbourhood",
  "params": [
    {"id": "amount", "label": "Amount", "type": "number", "min": 0, "max": 40, "default": 6, "unit": "px", "animate": "cmd"},
    {"id": "angle", "label": "Angle", "type": "number", "min": -180, "max": 180, "default": 0, "unit": "°", "animate": "cmd"},
    {"id": "edge", "label": "Edges", "type": "enum", "default": "smear", "animate": false,
     "options": [{"id": "smear", "label": "Smear"}, {"id": "wrap", "label": "Wrap"}]},
    {"id": "tint", "label": "Tint", "type": "color", "default": "#ff0000", "animate": "cmd"},
    {"id": "center", "label": "Centre", "type": "point", "default": [0.5, 0.5], "animate": "expr"},
    {"id": "on", "label": "On", "type": "boolean", "default": true, "animate": false},
    {"id": "lut", "label": "LUT", "type": "file", "default": "", "accept": [".cube"], "animate": false}
  ],
  "export": {"filters": ["rgbashift"]},
  "parity": {"mode": "pixels", "mean": 1, "p99": 6},
  "amount": "blend"
}`

func TestParseDef(t *testing.T) {
	d, err := ParseDef([]byte(goodDef))
	if err != nil {
		t.Fatal(err)
	}
	if d.Type != "rgbSplit" || len(d.Params) != 7 || d.Params[0].Animate != "cmd" || d.Params[2].Animate != "" {
		t.Fatalf("parsed: %+v", d)
	}
	// Animate round-trips: "" goes back out as false.
	b, _ := json.Marshal(d.Params[2])
	if !strings.Contains(string(b), `"animate":false`) {
		t.Errorf("animate false didn't round-trip: %s", b)
	}
}

func TestParseDefRejects(t *testing.T) {
	cases := map[string][2]string{
		"bad type":          {`"type": "rgbSplit"`, `"type": "RGB split"`},
		"no version":        {`"version": 1`, `"version": 0`},
		"bad category":      {`"category": "stylize"`, `"category": "fancy"`},
		"bad kind":          {`"kind": "neighbourhood"`, `"kind": "temporal"`},
		"bad amount":        {`"amount": "blend"`, `"amount": "half"`},
		"no filters":        {`"filters": ["rgbashift"]`, `"filters": []`},
		"default too big":   {`"default": 6,`, `"default": 99,`},
		"min not below max": {`"min": 0, "max": 40`, `"min": 40, "max": 40`},
		"enum default":      {`"default": "smear"`, `"default": "melt"`},
		"color default":     {`"default": "#ff0000"`, `"default": "red"`},
		"point default":     {`"default": [0.5, 0.5]`, `"default": [2, 0.5]`},
		"repeated param":    {`"id": "angle"`, `"id": "amount"`},
		"keyed enum": {`"animate": false,
     "options"`, `"animate": "cmd",
     "options"`},
		"bad animate":       {`"animate": "expr"`, `"animate": true`},
		"parity mode":       {`"mode": "pixels", "mean": 1, "p99": 6`, `"mode": "close"`},
		"none needs reason": {`"mode": "pixels", "mean": 1, "p99": 6`, `"mode": "none"`},
	}
	for name, c := range cases {
		if !strings.Contains(goodDef, c[0]) {
			t.Fatalf("%s: fixture doesn't contain %q", name, c[0])
		}
		if _, err := ParseDef([]byte(strings.Replace(goodDef, c[0], c[1], 1))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
