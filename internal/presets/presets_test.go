package presets

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/fx"
)

func setup(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	oldDir, oldLut := Dir, fx.LutDir
	Dir = func() (string, error) { return filepath.Join(root, "presets"), nil }
	fx.LutDir = func() (string, error) { return filepath.Join(root, "luts"), nil }
	t.Cleanup(func() { Dir, fx.LutDir = oldDir, oldLut })
	return NewService(), root
}

const doc = `{"name":"  Night raid ","effects":[
 {"type":"adjust","enabled":true,"params":{"exposure":0.4},"keys":{"contrast":[{"t":-1,"v":1,"ease":"linear"},{"t":3,"v":1.5,"ease":"in"}]}},
 {"type":"look","enabled":true,"params":{"look":"cool"}}]}`

func TestSaveListRenameDelete(t *testing.T) {
	s, _ := setup(t)
	list, err := s.List()
	if err != nil || len(list) != 5 || !list[0].Builtin || list[0].ID != "builtin-bw" {
		t.Fatalf("fresh list = %+v, %v", list, err)
	}
	p, err := s.Save(doc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Night raid" || !strings.HasPrefix(p.ID, "pre-") || p.CreatedAt.IsZero() {
		t.Errorf("saved %+v", p)
	}
	if k := p.Effects[0].Keys["contrast"]; k[0].T != 0 || k[1].T != 1 {
		t.Errorf("key times not clamped to 0..1: %+v", k)
	}
	list, _ = s.List()
	if len(list) != 6 || list[5].ID != p.ID {
		t.Fatalf("list after save = %+v", list)
	}
	if r, err := s.Rename(p.ID, "Day raid"); err != nil || r.Name != "Day raid" {
		t.Errorf("rename = %+v, %v", r, err)
	}
	if got, _ := s.Get(p.ID); got.Name != "Day raid" || len(got.Effects) != 2 {
		t.Errorf("get after rename = %+v", got)
	}
	if _, err := s.Rename(p.ID, "   "); err == nil {
		t.Error("blank name accepted")
	}
	if _, err := s.Rename("builtin-bw", "x"); err == nil {
		t.Error("renamed a built-in")
	}
	if err := s.Delete("builtin-bw"); err == nil {
		t.Error("deleted a built-in")
	}
	if err := s.Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(p.ID); err == nil {
		t.Error("deleted twice")
	}
}

func TestSaveRejects(t *testing.T) {
	s, _ := setup(t)
	for name, d := range map[string]string{
		"json":     `{`,
		"no name":  `{"effects":[{"type":"look","enabled":true}]}`,
		"empty":    `{"name":"x","effects":[]}`,
		"unknown":  `{"name":"x","effects":[{"type":"teleport","enabled":true}]}`,
		"newer":    `{"version":9,"name":"x","effects":[{"type":"look","enabled":true}]}`,
		"bad id":   `{"id":"../x","name":"x","effects":[{"type":"look","enabled":true}]}`,
		"builtin":  `{"id":"builtin-bw","name":"x","effects":[{"type":"look","enabled":true}]}`,
		"too long": `{"name":"` + strings.Repeat("a", 81) + `","effects":[{"type":"look","enabled":true}]}`,
	} {
		if _, err := s.Save(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestListSkipsUnreadable(t *testing.T) {
	s, root := setup(t)
	if _, err := s.Save(doc); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "presets")
	_ = os.WriteFile(filepath.Join(dir, "broken.json"), []byte("{"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "other.json"), []byte(`{"version":1,"id":"mismatch","name":"x","effects":[{"type":"look","enabled":true}]}`), 0o644)
	list, err := s.List()
	if err != nil || len(list) != 6 {
		t.Errorf("list = %d items, %v", len(list), err)
	}
}

func TestExportImport(t *testing.T) {
	s, root := setup(t)
	p, err := s.Save(doc)
	if err != nil {
		t.Fatal(err)
	}
	out, err := s.Export(p.ID, filepath.Join(root, "share", "night"))
	if err != nil || !strings.HasSuffix(out, "night.torufx") {
		t.Fatalf("export = %q, %v", out, err)
	}
	if _, err := s.Export(p.ID, out); err == nil {
		t.Error("overwrote an existing file")
	}
	if _, err := s.Export(p.ID, "relative.torufx"); err == nil {
		t.Error("relative path accepted")
	}
	q, err := s.Import(out)
	if err != nil {
		t.Fatal(err)
	}
	if q.ID == p.ID || q.Name != p.Name || len(q.Effects) != 2 {
		t.Errorf("import = %+v", q)
	}
	b, _ := s.Export("builtin-sepia", filepath.Join(root, "share", "sepia.torufx"))
	if r, err := s.Import(b); err != nil || r.Builtin || r.Effects[0].Params["look"] != "sepia" {
		t.Errorf("built-in round trip = %+v, %v", r, err)
	}
	_ = os.WriteFile(filepath.Join(root, "junk.torufx"), []byte("hello"), 0o644)
	if _, err := s.Import(filepath.Join(root, "junk.torufx")); err == nil {
		t.Error("imported junk")
	}
}

func cube(n int, title string) string {
	var b strings.Builder
	if title != "" {
		fmt.Fprintf(&b, "TITLE \"%s\"\n", title)
	}
	fmt.Fprintf(&b, "LUT_3D_SIZE %d\n", n)
	for i := 0; i < n*n*n; i++ {
		b.WriteString("0.5 0.5 0.5\n")
	}
	return b.String()
}

func TestLuts(t *testing.T) {
	s, root := setup(t)
	src := filepath.Join(root, "in")
	_ = os.MkdirAll(src, 0o755)
	a := filepath.Join(src, "Teal & Orange.cube")
	_ = os.WriteFile(a, []byte(cube(3, "")), 0o644)
	l, err := s.ImportLut(a)
	if err != nil {
		t.Fatal(err)
	}
	if l.ID != "Teal Orange.cube" || l.Name != "Teal Orange" || l.Size != 3 {
		t.Errorf("import = %+v", l)
	}
	// Same file again: the same LUT, not a copy.
	if again, _ := s.ImportLut(a); again.ID != l.ID {
		t.Errorf("re-import = %+v", again)
	}
	// Same name, different table: a numbered copy, named by its TITLE.
	b := filepath.Join(root, "Teal & Orange.cube")
	_ = os.WriteFile(b, []byte(cube(2, "Warm film")), 0o644)
	l2, err := s.ImportLut(b)
	if err != nil || l2.ID != "Teal Orange (2).cube" || l2.Name != "Warm film" {
		t.Errorf("second = %+v, %v", l2, err)
	}
	list, _ := s.ListLuts()
	if len(list) != 2 || list[0].ID != l.ID {
		t.Errorf("list = %+v", list)
	}
	if txt, err := s.ReadLut(l.ID); err != nil || !strings.HasPrefix(txt, "LUT_3D_SIZE 3") {
		t.Errorf("read = %.20q, %v", txt, err)
	}
	if _, err := s.ReadLut("../presets/x.cube"); err == nil {
		t.Error("read outside the folder")
	}
	dots := filepath.Join(src, "my...lut.cube")
	_ = os.WriteFile(dots, []byte(cube(2, "")), 0o644)
	if d, err := s.ImportLut(dots); err != nil || d.ID != "my.lut.cube" {
		t.Errorf("dots = %+v, %v", d, err)
	} else if _, err := s.ReadLut(d.ID); err != nil {
		t.Errorf("read dots: %v", err)
	}
	bad := filepath.Join(src, "bad.cube")
	_ = os.WriteFile(bad, []byte("LUT_1D_SIZE 2\n0 0 0\n1 1 1\n"), 0o644)
	if _, err := s.ImportLut(bad); err == nil || !strings.Contains(err.Error(), "1D") {
		t.Errorf("1D import err = %v", err)
	}
	if _, err := s.ImportLut(filepath.Join(src, "x.png")); err == nil {
		t.Error("imported a non-.cube")
	}
	if err := s.DeleteLut(l.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListLuts(); len(list) != 2 {
		t.Errorf("after delete = %+v", list)
	}
}
