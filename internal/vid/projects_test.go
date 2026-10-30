package vid

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func withProjectsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := projectsDir
	projectsDir = func() (string, error) { return filepath.Join(dir, "projects"), nil }
	t.Cleanup(func() { projectsDir = old })
	return dir
}

func TestProjectsSaveListLoadDelete(t *testing.T) {
	dir := withProjectsDir(t)
	media := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "deleted.mp3")
	esc := func(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }
	doc := `{"version":2,"id":"proj-abc-1","name":"clutch","canvas":{"width":1920,"height":1080,"fps":60},
		"media":[{"id":"m1","path":"` + esc(gone) + `","kind":"audio"},{"id":"m2","path":"` + esc(media) + `","kind":"video"}],
		"tracks":[{"clips":[{"start":0,"in":0,"out":10,"speed":1},{"start":12,"in":0,"out":8,"speed":2}]},
		          {"clips":[{"start":1,"in":0,"out":30,"speed":1}]}],"texts":[],"crop":null}`
	s := &VideoService{}
	if err := s.SaveProject(doc); err != nil {
		t.Fatal(err)
	}
	// A second project, saved later, lists first.
	time.Sleep(20 * time.Millisecond)
	if err := s.SaveProject(`{"version":2,"id":"proj-new","name":"","media":[],"tracks":[]}`); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "proj-new" || list[1].ID != "proj-abc-1" {
		t.Fatalf("list order: %+v", list)
	}
	if list[0].Name != "Untitled" {
		t.Errorf("unnamed project: %q", list[0].Name)
	}
	p := list[1]
	if p.Name != "clutch" || p.Clips != 3 || p.DurationMs != 31000 || p.Missing != 1 || p.FirstMedia != media {
		t.Errorf("summary: %+v", p)
	}
	got, err := s.LoadProject("proj-abc-1")
	if err != nil || got != doc {
		t.Fatalf("load: %v, same=%v", err, got == doc)
	}
	// Saving again replaces the file; no temp file is left.
	if err := s.SaveProject(strings.Replace(doc, `"clutch"`, `"clutch 2"`, 1)); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "projects")); len(entries) != 2 {
		t.Errorf("files after resave: %d", len(entries))
	}
	if err := s.DeleteProject("proj-abc-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LoadProject("proj-abc-1"); err == nil || !strings.Contains(err.Error(), "deleted") {
		t.Errorf("load after delete: %v", err)
	}
	if err := s.DeleteProject("proj-abc-1"); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
}

func TestProjectsRejectBadInput(t *testing.T) {
	withProjectsDir(t)
	s := &VideoService{}
	for _, doc := range []string{
		`not json`,
		`{"version":1,"id":"proj-a"}`,
		`{"version":2,"id":"../../evil"}`,
		`{"version":2,"id":"a/b"}`,
		`{"version":2,"id":""}`,
	} {
		if err := s.SaveProject(doc); err == nil {
			t.Errorf("saved %s", doc)
		}
	}
	for _, id := range []string{"..", `..\x`, "a/b", ""} {
		if _, err := s.LoadProject(id); err == nil {
			t.Errorf("loaded %q", id)
		}
		if err := s.DeleteProject(id); err == nil {
			t.Errorf("deleted %q", id)
		}
	}
	list, err := s.ListProjects()
	if err != nil || len(list) != 0 {
		t.Errorf("list with no folder: %v %v", list, err)
	}
}

func TestRenameProject(t *testing.T) {
	withProjectsDir(t)
	s := &VideoService{}
	doc := `{"version":2,"id":"proj-r","name":"old","media":[],"tracks":[],"crop":null,"extra":{"keep":[1,2]}}`
	if err := s.SaveProject(doc); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameProject("proj-r", "  Montage \"final\"  "); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListProjects()
	if err != nil || len(list) != 1 || list[0].Name != `Montage "final"` {
		t.Fatalf("after rename: %+v %v", list, err)
	}
	got, _ := s.LoadProject("proj-r")
	if !strings.Contains(got, `"extra":{"keep":[1,2]}`) || !strings.Contains(got, `"version":2`) {
		t.Errorf("rename dropped other fields: %s", got)
	}
	for _, bad := range []string{"", "   ", strings.Repeat("x", 121)} {
		if err := s.RenameProject("proj-r", bad); err == nil {
			t.Errorf("RenameProject(%q) should fail", bad)
		}
	}
	if err := s.RenameProject("proj-gone", "x"); err == nil {
		t.Error("renaming a missing project should fail")
	}
	if err := s.RenameProject("../evil", "x"); err == nil {
		t.Error("a bad id should fail")
	}
}

func TestProjectsV3(t *testing.T) {
	withProjectsDir(t)
	s := &VideoService{}
	v3 := `{"version":3,"id":"proj-v3","name":"ramped","media":[],"tracks":[{"clips":[
		{"start":0,"in":5,"out":7,"speed":1,"hold":true,"effects":[{"id":"fx-1","type":"adjust","enabled":true,"params":{"contrast":1.2}}]}]}]}`
	if err := s.SaveProject(v3); err != nil {
		t.Fatalf("v3 save: %v", err)
	}
	list, err := s.ListProjects()
	if err != nil || len(list) != 1 || list[0].Name != "ramped" || list[0].DurationMs != 2000 {
		t.Fatalf("v3 list: %+v %v", list, err)
	}
	if err := s.SaveProject(`{"version":4,"id":"proj-v4","media":[],"tracks":[]}`); err == nil {
		t.Error("a future version should be refused")
	}
}
