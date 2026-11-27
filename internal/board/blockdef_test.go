package board

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sampleDefs are testdata/custom-blocks.json's two block types, as an agent
// sends them (no version or origin).
func sampleDefs(t *testing.T) []BlockDef {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "custom-blocks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Blocks []BlockDef `json:"blocks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Blocks
}

// withLibrary points BlocksDir at a temporary folder.
func withLibrary(t *testing.T) (*Library, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "blocks")
	old := BlocksDir
	BlocksDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { BlocksDir = old })
	return NewLibrary(), dir
}

func TestBlockPartsFile(t *testing.T) {
	kinds := map[string]bool{"text": true, "template": true, "prop": true, "boolean": true, "number": true, "color": true, "colors": true, "icon": true, "columns": true}
	propKinds := map[string]bool{"string": true, "enum": true, "number": true, "integer": true, "boolean": true, "string-list": true, "checklist": true, "poll": true, "rows": true}
	if len(blockParts.Parts) < 15 {
		t.Fatalf("only %d parts", len(blockParts.Parts))
	}
	for name, p := range blockParts.Parts {
		if p.Description == "" {
			t.Errorf("part %s has no description", name)
		}
		for fname, f := range p.Fields {
			if (f.Kind == "") == (len(f.Enum) == 0) {
				t.Errorf("%s.%s needs exactly one of kind and enum", name, fname)
			}
			if f.Kind != "" && !kinds[f.Kind] {
				t.Errorf("%s.%s: unknown kind %q", name, fname, f.Kind)
			}
			if (f.Kind == "prop") != (len(f.Binds) > 0) {
				t.Errorf("%s.%s: binds go with kind prop", name, fname)
			}
			for _, b := range f.Binds {
				if !propKinds[b] {
					t.Errorf("%s.%s binds unknown prop kind %q", name, fname, b)
				}
			}
		}
		for _, k := range p.OneOf {
			if _, ok := p.Fields[k]; !ok {
				t.Errorf("%s: oneOf names %q, not a field", name, k)
			}
		}
	}
	seen := map[string]bool{}
	for _, i := range blockParts.Icons {
		if seen[i] {
			t.Errorf("icon %q twice", i)
		}
		seen[i] = true
	}
}

func TestSampleDefsPass(t *testing.T) {
	for _, d := range sampleDefs(t) {
		if err := d.checkContent(); err != nil {
			t.Errorf("%s: %v", d.Type, err)
		}
	}
}

// Every way a definition can go wrong is refused with a reason, and nothing
// that could be markup or code gets through.
func TestBadDefinitions(t *testing.T) {
	base := sampleDefs(t)[0]
	edit := func(f func(d *BlockDef)) BlockDef {
		d := base
		d.Props = append(json.RawMessage(nil), base.Props...)
		d.Layout = append(json.RawMessage(nil), base.Layout...)
		d.Defaults = append(json.RawMessage(nil), base.Defaults...)
		f(&d)
		return d
	}
	layout := func(s string) func(d *BlockDef) { return func(d *BlockDef) { d.Layout = json.RawMessage(s) } }
	propsSchema := func(s string) func(d *BlockDef) { return func(d *BlockDef) { d.Props = json.RawMessage(s) } }
	for _, c := range []struct {
		name, want string
		f          func(d *BlockDef)
	}{
		{"built-in name", "built-in", func(d *BlockDef) { d.Type = "sticky" }},
		{"bad name", "bad block type name", func(d *BlockDef) { d.Type = "Feature Idea" }},
		{"no label", "label", func(d *BlockDef) { d.Label = " " }},
		{"unknown icon", "icon", func(d *BlockDef) { d.Icon = "https://example.com/x.svg" }},
		{"tiny", "w and h", func(d *BlockDef) { d.W = 5 }},
		{"html part", `no part "html"`, layout(`{"part":"html","html":"<script>alert(1)</script>"}`)},
		{"script field", `no field "onClick"`, layout(`{"part":"text","text":"hi","onClick":"alert(1)"}`)},
		{"style field", `no field "style"`, layout(`{"part":"stack","style":"background:url(x)"}`)},
		{"not a part", "must be a part", layout(`"<b>hi</b>"`)},
		{"children on a leaf", "has no children", layout(`{"part":"text","text":"a","children":[]}`)},
		{"missing prop", `needs "prop"`, layout(`{"part":"field","label":"x"}`)},
		{"unknown prop", `no prop "nope"`, layout(`{"part":"field","prop":"nope"}`)},
		{"wrong kind", "this part takes", layout(`{"part":"checkbox","prop":"title","label":"x"}`)},
		{"bad enum value", "must be one of", layout(`{"part":"stack","direction":"diagonal"}`)},
		{"template of a list", "{tags}", layout(`{"part":"text","text":"Tags: {tags}"}`)},
		{"badge both", "exactly one", layout(`{"part":"badge","text":"a","prop":"status"}`)},
		{"bad colour", "must be one of", layout(`{"part":"badge","prop":"status","colors":{"idea":"#ff0000"}}`)},
		{"no columns", "column", layout(`{"part":"text","text":"x"}` + ``)},
		{"too deep", "too deep", layout(strings.Repeat(`{"part":"stack","children":[`, 14) + `{"part":"divider"}` + strings.Repeat(`]}`, 14))},
		{"too many parts", "more than", layout(`{"part":"stack","children":[` + strings.TrimSuffix(strings.Repeat(`{"part":"stack","children":[{"part":"divider"},{"part":"divider"},{"part":"divider"},{"part":"divider"}]},`, 45), ",") + `]}`)},
		{"open schema", "closed object", propsSchema(`{"type":"object","properties":{"title":{"type":"string"}}}`)},
		{"unchecked keyword", `keyword "pattern"`, propsSchema(`{"type":"object","additionalProperties":false,"properties":{"title":{"type":"string","pattern":".*"}}}`)},
		{"no type", "needs a type", propsSchema(`{"type":"object","additionalProperties":false,"properties":{"title":{}}}`)},
		{"required missing", "required names", propsSchema(`{"type":"object","additionalProperties":false,"required":["x"],"properties":{"title":{"type":"string"}}}`)},
		{"bad prop name", "bad prop name", propsSchema(`{"type":"object","additionalProperties":false,"properties":{"Title Here":{"type":"string"}}}`)},
		{"defaults break schema", "defaults", func(d *BlockDef) { d.Defaults = json.RawMessage(`{"title":"x","votes":-1}`) }},
		{"defaults not an object", "defaults must be a JSON object", func(d *BlockDef) { d.Defaults = json.RawMessage(`[1]`) }},
		{"name prop not text", "nameProp", func(d *BlockDef) { d.NameProp = "votes" }},
	} {
		if c.name == "no columns" {
			c.f = layout(`{"part":"table","prop":"tags"}`)
			c.want = "this part takes"
		}
		d := edit(c.f)
		err := d.checkContent()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want an error with %q", c.name, err, c.want)
		}
	}
}

func TestPropKinds(t *testing.T) {
	for schema, want := range map[string]string{
		`{"type":"string"}`:                          "string",
		`{"type":"string","enum":["a"]}`:             "enum",
		`{"type":"integer"}`:                         "integer",
		`{"type":"array","items":{"type":"string"}}`: "string-list",
		`{"type":"array","items":{"type":"array","items":{"type":"string"}}}`:                                            "rows",
		`{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"done":{"type":"boolean"}}}}`:   "checklist",
		`{"type":"array","items":{"type":"object","properties":{"label":{"type":"string"},"votes":{"type":"integer"}}}}`: "poll",
		`{"type":"array","items":{"type":"object","properties":{"text":{"type":"string"},"extra":{"type":"string"}}}}`:   "",
		`{"type":"object","properties":{}}`: "",
	} {
		var s map[string]any
		_ = json.Unmarshal([]byte(schema), &s)
		if got := PropKind(s); got != want {
			t.Errorf("PropKind(%s) = %q, want %q", schema, got, want)
		}
	}
}

func TestLibrary(t *testing.T) {
	lib, dir := withLibrary(t)
	defs := sampleDefs(t)
	fi, err := lib.Define(defs[0])
	if err != nil {
		t.Fatal(err)
	}
	if fi.Version != 1 || !strings.HasPrefix(fi.Origin, "blt-") {
		t.Fatalf("defined %+v", fi)
	}
	if _, err := os.Stat(filepath.Join(dir, "feature-idea.json")); err != nil {
		t.Fatalf("not saved in %%AppData%%\\toru\\blocks: %v", err)
	}
	if _, err := lib.Define(defs[0]); !errors.Is(err, ErrBlockExists) {
		t.Fatalf("defining it twice: %v", err)
	}
	if _, err := lib.Define(defs[1]); err != nil {
		t.Fatal(err)
	}
	up, err := lib.Update("feature-idea", func(d *BlockDef) error { d.Label = "Feature request"; d.Origin = "blt-hijack"; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if up.Version != 2 || up.Origin != fi.Origin || up.Label != "Feature request" {
		t.Fatalf("updated %+v", up)
	}
	if _, err := lib.Update("feature-idea", func(d *BlockDef) error { d.Layout = json.RawMessage(`{"part":"html"}`); return nil }); err == nil {
		t.Fatal("a bad update was saved")
	}
	if got, _ := lib.Get("feature-idea"); got.Version != 2 {
		t.Fatalf("a refused update changed the file: v%d", got.Version)
	}

	// A broken file is listed, not lost or fatal.
	if err := os.WriteFile(filepath.Join(dir, "junk.json"), []byte(`{"type":"junk"`), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := lib.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Blocks) != 2 || l.Blocks[0].Label != "Bug report" || len(l.Broken) != 1 || l.Broken[0].File != "junk.json" {
		t.Fatalf("list = %+v", l)
	}

	// Saving a board's copy: the same block, a newer one, a clash.
	other := defs[0]
	other.Version, other.Origin = 1, "blt-someoneelse"
	if _, _, err := lib.Save(other, ""); !errors.Is(err, ErrBlockExists) {
		t.Fatalf("a different block with the same name: %v", err)
	}
	saved, changed, err := lib.Save(other, "feature-idea-2")
	if err != nil || !changed || saved.Type != "feature-idea-2" || saved.Version != 1 || saved.Origin == other.Origin {
		t.Fatalf("save as: %+v %v %v", saved, changed, err)
	}
	mine, _ := lib.Get("bug-report")
	if _, changed, err := lib.Save(mine, ""); err != nil || changed {
		t.Fatalf("saving the same version: %v %v", changed, err)
	}
	newer := mine
	newer.Version = 5
	if got, changed, err := lib.Save(newer, ""); err != nil || !changed || got.Version != 5 {
		t.Fatalf("saving a newer version: %+v %v %v", got, changed, err)
	}
	fresh := defs[1]
	fresh.Type, fresh.Version, fresh.Origin = "imported-bug", 3, "blt-friend"
	if got, changed, err := lib.Save(fresh, ""); err != nil || !changed || got.Origin != "blt-friend" || got.Version != 3 {
		t.Fatalf("saving a new block from a board keeps its origin and version: %+v %v %v", got, changed, err)
	}

	if err := lib.Remove("feature-idea"); err != nil {
		t.Fatal(err)
	}
	if err := lib.Remove("feature-idea"); err == nil {
		t.Fatal("removed twice")
	}
}

// libDef is a sample block type as the library saves it.
func libDef(t *testing.T, i int) BlockDef {
	t.Helper()
	d := sampleDefs(t)[i]
	d.Version, d.Origin = 1, "blt-test"+string(rune('a'+i))
	if err := d.Check(); err != nil {
		t.Fatal(err)
	}
	return d
}

// A board carries the custom types its blocks use: in ops, undo and redo,
// its file and a .toruboard export.
func TestBoardCarriesTypes(t *testing.T) {
	s, _ := setup(t)
	b, err := s.Create("Ideas")
	if err != nil {
		t.Fatal(err)
	}
	fi := libDef(t, 0)
	op, err := s.Apply(b.ID, Op{Source: Agent("Claude"), Types: []BlockDef{fi}, Blocks: []Block{{ID: "i1", Type: "feature-idea", W: 320, H: 380, Props: fi.Defaults}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(op.Types) != 1 || op.Types[0].Type != "feature-idea" {
		t.Fatalf("as applied: %+v", op.Types)
	}
	got, _ := s.Load(b.ID)
	if got.BoardType("feature-idea") == nil {
		t.Fatal("the board doesn't carry the type")
	}

	// Undo takes the block and the type; redo puts both back.
	if _, err := s.Undo(b.ID, Person); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(b.ID); got.BoardType("feature-idea") != nil || len(got.Blocks) != 0 {
		t.Fatalf("after undo: %+v", got.Types)
	}
	if _, err := s.Redo(b.ID, Person); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(b.ID); got.BoardType("feature-idea") == nil {
		t.Fatal("redo didn't put the type back")
	}

	// A different block with the same name, an older version, or dropping
	// a type in use are refused.
	other := fi
	other.Origin = "blt-other"
	other.Version = 2
	if _, err := s.Apply(b.ID, Op{Source: Person, Types: []BlockDef{other}}); err == nil || !strings.Contains(err.Error(), "different block type") {
		t.Fatalf("clash: %v", err)
	}
	if _, err := s.Apply(b.ID, Op{Source: Person, DropTypes: []string{"feature-idea"}}); err == nil || !strings.Contains(err.Error(), "still used") {
		t.Fatalf("drop in use: %v", err)
	}
	newer := fi
	newer.Version = 2
	newer.Label = "Feature request"
	if _, err := s.Apply(b.ID, Op{Source: Person, Types: []BlockDef{newer}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(b.ID, Op{Source: Person, Types: []BlockDef{fi}}); err == nil || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("downgrade: %v", err)
	}
	if _, err := s.Apply(b.ID, Op{Source: Person, Types: []BlockDef{newer}}); err == nil {
		t.Fatal("putting the same version again changed something")
	}
	bad := fi
	bad.Layout = json.RawMessage(`{"part":"html"}`)
	bad.Type = "evil"
	if _, err := s.Apply(b.ID, Op{Source: Person, Types: []BlockDef{bad}}); err == nil {
		t.Fatal("a bad definition went onto the board")
	}

	// The file and a portable export carry it; a fresh store reads it back.
	s2 := NewStore()
	again, err := s2.Load(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d := again.BoardType("feature-idea"); d == nil || d.Version != 2 || d.Label != "Feature request" {
		t.Fatalf("from the file: %+v", d)
	}
	data, err := EncodePortable(again, ReadImage)
	if err != nil {
		t.Fatal(err)
	}
	imp, err := s2.Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if imp.ID == again.ID || imp.BoardType("feature-idea") == nil || !imp.BoardType("feature-idea").Same(again.BoardType("feature-idea")) {
		t.Fatalf("import lost the type: %+v", imp.Types)
	}
	if md := Markdown(again); !strings.Contains(md, "- Feature request: New idea") || !strings.Contains(md, "  - votes: 0") {
		t.Fatalf("outline:\n%s", md)
	}
}

// A board file whose custom type is broken still opens: the type is left
// out and its blocks show as placeholders. A board with an unknown type
// opens too.
func TestBrokenTypeStillOpens(t *testing.T) {
	fi := libDef(t, 0)
	b := New("brd-x", "Broken", now())
	bad := fi
	bad.Layout = json.RawMessage(`{"part":"script","src":"x.js"}`)
	b.Types = []BlockDef{bad, libDef(t, 1)}
	b.Blocks = []Block{
		{ID: "a", Type: "feature-idea", W: 10, H: 10, Props: json.RawMessage(`{"title":"x"}`)},
		{ID: "b", Type: "from-the-future", W: 10, H: 10, Props: json.RawMessage(`{}`)},
	}
	// Encode doesn't check; Decode does.
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(data)
	if err != nil {
		t.Fatalf("a board with a broken type didn't open: %v", err)
	}
	if got.BoardType("feature-idea") != nil || got.BoardType("bug-report") == nil || len(got.Blocks) != 2 {
		t.Fatalf("decoded types %+v, blocks %d", got.Types, len(got.Blocks))
	}
}

func TestCheckBlockProps(t *testing.T) {
	b := New("brd-y", "Y", now())
	b.Types = []BlockDef{libDef(t, 1)}
	if err := CheckBlockProps(&b, "bug-report", json.RawMessage(`{"title":"Crash","severity":"high","steps":[{"text":"Open it"}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckBlockProps(&b, "bug-report", json.RawMessage(`{"title":"Crash","severity":"apocalyptic"}`)); err == nil {
		t.Fatal("a bad enum value passed")
	}
	if err := CheckBlockProps(&b, "sticky", json.RawMessage(`{"text":"hi"}`)); err != nil {
		t.Fatal(err)
	}
	if err := CheckBlockProps(&b, "nope", json.RawMessage(`{}`)); err == nil {
		t.Fatal("an unknown type passed")
	}
}

// A custom block's image part keeps its picture in exports.
func TestCustomImageAssets(t *testing.T) {
	d := libDef(t, 0)
	d.Type = "shot-note"
	d.Props = json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"title":{"type":"string"},"shot":{"type":"string"}}}`)
	d.Defaults = json.RawMessage(`{"title":"x"}`)
	d.Layout = json.RawMessage(`{"part":"stack","children":[{"part":"field","prop":"title"},{"part":"image","prop":"shot"}]}`)
	if err := d.Check(); err != nil {
		t.Fatal(err)
	}
	asset := strings.Repeat("c", 64) + ".png"
	b := New("brd-i", "Pictures", now())
	b.Types = []BlockDef{d}
	b.Blocks = []Block{{ID: "s", Type: "shot-note", W: 10, H: 10, Props: json.RawMessage(`{"title":"x","shot":"` + asset + `"}`)}}
	if got := Assets(b); len(got) != 1 || got[0] != asset {
		t.Fatalf("Assets = %v", got)
	}
}
