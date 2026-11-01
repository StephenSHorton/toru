package presets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const textDoc = `{"kind":"text","id":"text-mh8x2k1a-ab12","name":" Neon sign ","createdAt":"2026-10-31T20:00:00Z",
 "style":{"color":"#e6fffb","outlineWidth":0.05,"bold":true,"opacityKeys":[{"t":0,"v":1}]}}`

func TestTextStyles(t *testing.T) {
	s, root := setup(t)
	if l, err := s.ListTextStyles(); err != nil || l == nil || len(l) != 0 {
		t.Fatalf("fresh text list = %#v, %v", l, err)
	}
	filter, err := s.Save(doc)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Save(textDoc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != KindText || p.ID != "text-mh8x2k1a-ab12" || p.Name != "Neon sign" || p.Style["bold"] != true {
		t.Errorf("saved %+v", p)
	}
	if _, ok := p.Style["opacityKeys"]; ok {
		t.Error("opacity keys kept in a style")
	}
	// Same id again overwrites (the localStorage migration relies on it).
	if _, err := s.Save(textDoc); err != nil {
		t.Fatal(err)
	}
	texts, _ := s.ListTextStyles()
	if len(texts) != 1 || texts[0].ID != p.ID {
		t.Fatalf("text list = %+v", texts)
	}
	filters, _ := s.List()
	for _, f := range filters {
		if f.Kind == KindText {
			t.Fatalf("List has a text style: %+v", f)
		}
	}
	if len(filters) != 6 || filters[5].ID != filter.ID {
		t.Errorf("filters = %+v", filters)
	}
	if r, err := s.Rename(p.ID, "Neon"); err != nil || r.Name != "Neon" || r.Kind != KindText || r.Style["color"] != "#e6fffb" {
		t.Errorf("rename = %+v, %v", r, err)
	}

	// Export, then import: the same style under a new id.
	out := filepath.Join(root, "out", "Neon")
	_ = os.MkdirAll(filepath.Dir(out), 0o755)
	path, err := s.Export(p.ID, out)
	if err != nil || !strings.HasSuffix(path, ".torufx") {
		t.Fatalf("export = %q, %v", path, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), `"effects"`) {
		t.Errorf("text export has effects: %s", b)
	}
	in, err := s.ImportTextStyle(path)
	if err != nil {
		t.Fatal(err)
	}
	if in.ID == p.ID || in.Kind != KindText || in.Name != "Neon" || in.Style["outlineWidth"] != 0.05 || in.Style["bold"] != true {
		t.Errorf("import = %+v", in)
	}
	if texts, _ := s.ListTextStyles(); len(texts) != 2 || texts[0].ID != p.ID || texts[1].ID != in.ID {
		t.Errorf("text list after import = %+v", texts)
	}
	// Each library refuses the other's files.
	if _, err := s.importAs(path, ptr(KindFilter)); err == nil || !strings.Contains(err.Error(), "Text library") {
		t.Errorf("filter import of a text style: %v", err)
	}
	fpath, err := s.Export(filter.ID, filepath.Join(root, "out", "f.torufx"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ImportTextStyle(fpath); err == nil || !strings.Contains(err.Error(), "Filters tab") {
		t.Errorf("text import of a filter: %v", err)
	}
	// The generic Import keeps the file's kind.
	if g, err := s.Import(path); err != nil || g.Kind != KindText {
		t.Errorf("generic import = %+v, %v", g, err)
	}

	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if texts, _ := s.ListTextStyles(); len(texts) != 2 {
		t.Errorf("after delete = %+v", texts)
	}
}

func TestTextStyleChecks(t *testing.T) {
	s, _ := setup(t)
	for name, d := range map[string]string{
		"effects":  `{"kind":"text","name":"x","style":{},"effects":[{"type":"look","enabled":true,"params":{"look":"bw"}}]}`,
		"no name":  `{"kind":"text","name":"  ","style":{}}`,
		"kind":     `{"kind":"sticker","name":"x"}`,
		"too big":  `{"kind":"text","name":"x","style":{"pad":"` + strings.Repeat("a", maxStyleBytes) + `"}}`,
		"bad id":   `{"kind":"text","id":"builtin:classic","name":"x","style":{}}`,
		"style !{": `{"kind":"text","name":"x","style":[1]}`,
	} {
		if _, err := s.Save(d); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	// An empty style is the default look: fine.
	if p, err := s.Save(`{"kind":"text","name":"Plain"}`); err != nil || p.Style == nil {
		t.Errorf("empty style = %+v, %v", p, err)
	}
	// A filter loses any style it was sent.
	if p, err := s.Save(`{"name":"f","style":{"bold":true},"effects":[{"type":"look","enabled":true,"params":{"look":"bw"}}]}`); err != nil || p.Style != nil {
		t.Errorf("filter with style = %+v, %v", p, err)
	}
}

func ptr(s string) *string { return &s }
