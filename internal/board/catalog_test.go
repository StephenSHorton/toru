package board

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The types the block catalog card (whiteboard#6) set out to build.
var wantTypes = []string{"card", "checklist", "frame", "image", "poll", "rating", "sticky", "table", "tag-list", "text"}

func TestCatalogTypes(t *testing.T) {
	var got []string
	seen := map[string]bool{}
	for _, bt := range Catalog() {
		if seen[bt.Type] {
			t.Errorf("type %q listed twice", bt.Type)
		}
		seen[bt.Type] = true
		got = append(got, bt.Type)
		if !typePattern.MatchString(bt.Type) {
			t.Errorf("type %q isn't a valid block type name", bt.Type)
		}
		if bt.Label == "" || bt.Description == "" {
			t.Errorf("%s: needs a label and a description", bt.Type)
		}
		if bt.W <= 0 || bt.H <= 0 {
			t.Errorf("%s: default size %vx%v", bt.Type, bt.W, bt.H)
		}
		var s map[string]any
		if err := json.Unmarshal(bt.Props, &s); err != nil || s["type"] != "object" || s["additionalProperties"] != false {
			t.Errorf("%s: props schema must be a closed object (%v)", bt.Type, err)
		}
	}
	if strings.Join(got, ",") != strings.Join(wantTypes, ",") {
		t.Fatalf("catalog types = %v, want %v", got, wantTypes)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i] < got[j] }) {
		t.Fatal("Catalog() isn't sorted by type")
	}
}

// checkSchema implements only some JSON Schema keywords; any other keyword
// in catalog.json would be silently ignored in Go while the window and
// agents read it as a rule.
var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "minLength": true, "maxLength": true, "minItems": true, "maxItems": true,
	"minimum": true, "maximum": true, "description": true,
}

func walkSchema(t *testing.T, at string, s map[string]any) {
	t.Helper()
	for k, v := range s {
		if !schemaKeywords[k] {
			t.Errorf("%s: keyword %q isn't checked by checkSchema", at, k)
		}
		switch k {
		case "properties":
			for name, sub := range v.(map[string]any) {
				walkSchema(t, at+"."+name, sub.(map[string]any))
			}
		case "items":
			walkSchema(t, at+"[]", v.(map[string]any))
		}
	}
	switch s["type"] {
	case "object", "array", "string", "boolean", "integer", "number":
	default:
		t.Errorf("%s: type %v", at, s["type"])
	}
}

func TestCatalogSchemaKeywords(t *testing.T) {
	for _, bt := range Catalog() {
		var s map[string]any
		if err := json.Unmarshal(bt.Props, &s); err != nil {
			t.Fatal(err)
		}
		walkSchema(t, bt.Type, s)
	}
}

// The four types the agent commands card (whiteboard#8) shipped first keep
// their fields and sizes.
func TestCatalogKeepsFirstTypes(t *testing.T) {
	want := map[string]struct {
		w, h   float64
		fields string
	}{
		"sticky":    {200, 200, "color,tags,text"},
		"text":      {320, 80, "align,size,tags,text"},
		"checklist": {280, 200, "items,tags,title"},
		"poll":      {300, 220, "multi,options,question,tags"},
	}
	for name, w := range want {
		bt, ok := LookupType(name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		var s struct {
			Properties map[string]any `json:"properties"`
		}
		_ = json.Unmarshal(bt.Props, &s)
		var fields []string
		for f := range s.Properties {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		if bt.W != w.w || bt.H != w.h || strings.Join(fields, ",") != w.fields {
			t.Errorf("%s: %vx%v %v, want %vx%v %s", name, bt.W, bt.H, fields, w.w, w.h, w.fields)
		}
	}
}

func TestCheckProps(t *testing.T) {
	for _, c := range []struct {
		typ, props string
		ok         bool
	}{
		{"sticky", `{"text":"hi","color":"pink","tags":["a"]}`, true},
		{"sticky", `{"text":"hi","color":"red"}`, false},
		{"card", `{"title":"Ship it","body":"notes","color":"purple"}`, true},
		{"card", `{"title":""}`, false},
		{"rating", `{"title":"Idea","value":3.5,"max":5}`, true},
		{"rating", `{"title":"Idea","value":11}`, false},
		{"rating", `{"title":"Idea","max":2.5}`, false},
		{"table", `{"columns":["A","B"],"rows":[["1","2"],["3"]]}`, true},
		{"table", `{"columns":[]}`, false},
		{"table", `{"columns":["A"],"rows":[[1]]}`, false},
		{"tag-list", `{"title":"Themes","items":["speed","agents"]}`, true},
		{"tag-list", `{"title":"Themes","items":[""]}`, false},
		{"image", `{}`, true},
		{"image", `{"asset":"x.png","alt":"a","fit":"cover"}`, true},
		{"image", `{"src":"https://example.com/a.png"}`, false},
		{"frame", `{"name":"Ideas"}`, false}, // frames go in the board's frames list
		{"shape", `{}`, false},
	} {
		err := CheckProps(c.typ, json.RawMessage(c.props))
		if (err == nil) != c.ok {
			t.Errorf("CheckProps(%s, %s) = %v, want ok %v", c.typ, c.props, err, c.ok)
		}
	}
	if err := CheckProps("frame", json.RawMessage(`{"name":"Ideas"}`)); err == nil || !strings.Contains(err.Error(), "frames") {
		t.Errorf("frame as a block: %v", err)
	}
	if err := CheckProps("shape", json.RawMessage(`{}`)); err == nil || strings.Contains(err.Error(), "frame") {
		t.Errorf("unknown type should list block types only: %v", err)
	}
}

// The window's sample board (frontend/src/whiteboard/sample-board.json) has
// a block of every type, and every block passes the catalog Go checks with.
func TestSampleBoardMatchesCatalog(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "whiteboard", "sample-board.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Frames []Frame `json:"frames"`
		Blocks []Block `json:"blocks"`
	}
	if err := json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	types := map[string]bool{}
	for _, b := range sample.Blocks {
		types[b.Type] = true
		if err := CheckProps(b.Type, b.Props); err != nil {
			t.Errorf("sample block %s: %v", b.ID, err)
		}
		var p struct {
			Asset string `json:"asset"`
		}
		_ = json.Unmarshal(b.Props, &p)
		if p.Asset != "" {
			if _, err := os.Stat(filepath.Join("testdata", "images", p.Asset)); err != nil {
				t.Errorf("sample block %s: its picture isn't in testdata/images: %v", b.ID, err)
			}
		}
	}
	frame, _ := LookupType("frame")
	for _, f := range sample.Frames {
		types["frame"] = true
		props, _ := json.Marshal(map[string]any{"name": f.Name, "color": f.Color})
		var schema map[string]any
		_ = json.Unmarshal(frame.Props, &schema)
		var v any
		_ = json.Unmarshal(props, &v)
		if err := checkSchema(schema, v, "frame"); err != nil {
			t.Errorf("sample frame %s: %v", f.ID, err)
		}
	}
	for _, want := range wantTypes {
		if !types[want] {
			t.Errorf("the sample board has no %s", want)
		}
	}
}

// board.read's Markdown outline shows what every type holds.
func TestMarkdownShowsEveryType(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "whiteboard", "sample-board.json"))
	if err != nil {
		t.Fatal(err)
	}
	var b Board
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	md := Markdown(b)
	for _, want := range []string{
		"Toru whiteboard: launch plan",
		"- Ship the block catalog\n  Ten block types",
		"  - [x] Write the catalog",
		"  - [ ] Agent-ready tests",
		"- Poll: Which block next?",
		"- Rating: How useful is this board?: 4/5",
		"- Themes: speed, agents, sharing, polish",
		"  | Task | Owner | Due |\n  | --- | --- | --- |\n  | Catalog | Beetle | Fri |",
		"- Image: Test card screenshot (Library screenshot)",
		"- Image: (empty)",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("outline lacks %q:\n%s", want, md)
		}
	}
}
