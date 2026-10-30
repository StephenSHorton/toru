package fx

import (
	"testing"
)

func TestShippedDefsLoad(t *testing.T) {
	got, err := loadDefs()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"adjust", "look", "chromaKey", "blur"} {
		if _, ok := got[typ]; !ok {
			t.Errorf("defs/%s.json missing", typ)
		}
	}
	if len(Defs()) != len(got) {
		t.Errorf("Defs() has %d, want %d", len(Defs()), len(got))
	}
}

func TestResolveClampsAndDefaults(t *testing.T) {
	adj, _ := Lookup("adjust")
	r := adj.Resolve(map[string]any{"exposure": 9.0, "contrast": "x", "nope": 1.0})
	if r["exposure"] != 3.0 {
		t.Errorf("exposure = %v, want clamped to 3", r["exposure"])
	}
	if r["contrast"] != 1.0 {
		t.Errorf("contrast = %v, want the default 1", r["contrast"])
	}
	if _, ok := r["nope"]; ok {
		t.Error("unknown param kept")
	}
	if len(r) != len(adj.Params) {
		t.Errorf("resolved %d params, want %d", len(r), len(adj.Params))
	}
	key, _ := Lookup("chromaKey")
	r = key.Resolve(map[string]any{"color": "#00FF00", "similarity": 0.0})
	if r["color"] != "#00ff00" || r["similarity"] != 0.01 {
		t.Errorf("chromaKey = %v", r)
	}
	look, _ := Lookup("look")
	if r = look.Resolve(map[string]any{"look": "teal"}); r["look"] != "none" {
		t.Errorf("unknown look = %v, want none", r["look"])
	}
	p := ParamDef{ID: "c", Label: "C", Type: "point", Default: []byte("[0.5,0.5]")}
	if v, _ := p.Clamp([]any{2.0, -1.0}); v != [2]float64{1, 0} {
		t.Errorf("point clamp = %v", v)
	}
}
