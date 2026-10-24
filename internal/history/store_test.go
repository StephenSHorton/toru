package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddAndList(t *testing.T) {
	dir := t.TempDir()
	// Point the store at our temp dir by constructing it manually (New uses
	// UserConfigDir; tests drive the fields directly).
	s := &Store{
		dir:   dir,
		index: filepath.Join(dir, "index.json"),
	}

	src := filepath.Join(dir, "src.png")
	if err := os.WriteFile(src, []byte("png-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	item, err := s.Add(src, KindImage)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if item.Kind != KindImage {
		t.Fatalf("kind = %q", item.Kind)
	}
	if _, err := os.Stat(item.Path); err != nil {
		t.Fatalf("captured file missing: %v", err)
	}

	list := s.List()
	if len(list) != 1 {
		t.Fatalf("len(list) = %d", len(list))
	}
	if list[0].Path != item.Path {
		t.Fatalf("path mismatch")
	}

	// Cap: add MaxItems+2 and ensure only MaxItems remain.
	for i := 0; i < MaxItems+2; i++ {
		p := filepath.Join(dir, "extra.png")
		_ = os.WriteFile(p, []byte{byte(i)}, 0o644)
		if _, err := s.Add(p, KindImage); err != nil {
			t.Fatalf("Add[%d]: %v", i, err)
		}
	}
	if n := len(s.List()); n != MaxItems {
		t.Fatalf("capped len = %d, want %d", n, MaxItems)
	}
}

func TestSetDirReloadsIndex(t *testing.T) {
	// Point UserConfigDir at a temp tree so library.json is not written into the
	// real user profile (os.UserConfigDir on Windows reads %APPDATA%).
	cfgRoot := t.TempDir()
	t.Setenv("APPDATA", cfgRoot)
	t.Setenv("XDG_CONFIG_HOME", cfgRoot)

	a := t.TempDir()
	b := t.TempDir()

	s := &Store{
		dir:   a,
		index: filepath.Join(a, "index.json"),
	}
	src := filepath.Join(a, "src.png")
	if err := os.WriteFile(src, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(src, KindImage); err != nil {
		t.Fatal(err)
	}
	if n := len(s.List()); n != 1 {
		t.Fatalf("seed len = %d", n)
	}

	// Put a separate capture in b so a reload of b is distinguishable.
	srcB := filepath.Join(b, "other.png")
	if err := os.WriteFile(srcB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write index for b as if it were a prior library folder.
	sB := &Store{dir: b, index: filepath.Join(b, "index.json")}
	if _, err := sB.Add(srcB, KindImage); err != nil {
		t.Fatal(err)
	}

	if err := s.SetDir(b); err != nil {
		t.Fatalf("SetDir: %v", err)
	}
	if !samePath(s.Dir(), b) {
		t.Fatalf("Dir = %q, want %q", s.Dir(), b)
	}
	list := s.List()
	if len(list) != 1 {
		t.Fatalf("after SetDir len = %d", len(list))
	}
	if !samePath(filepath.Dir(list[0].Path), b) {
		t.Fatalf("item path dir = %q, want under %q", list[0].Path, b)
	}
}

func TestReplaceKeepsItemInPlace(t *testing.T) {
	dir := t.TempDir()
	s := &Store{dir: dir, index: filepath.Join(dir, "index.json")}

	src := filepath.Join(dir, "src.png")
	if err := os.WriteFile(src, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := s.Add(src, KindImage)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // ids are millisecond timestamps
	second, err := s.Add(src, KindImage)
	if err != nil {
		t.Fatal(err)
	}

	edited := filepath.Join(dir, "edited.png")
	if err := os.WriteFile(edited, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := s.Replace(first.ID, edited)
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}

	list := s.List()
	if len(list) != 2 {
		t.Fatalf("Replace must not add an item: len = %d", len(list))
	}
	if list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("Replace must keep the order: %v", list)
	}
	if got.ID != first.ID || !got.TakenAt.Equal(first.TakenAt) || got.Path == first.Path {
		t.Fatalf("Replace = %+v, want same id/time and a fresh path", got)
	}
	if b, _ := os.ReadFile(got.Path); string(b) != "v2" {
		t.Fatalf("replaced file = %q", b)
	}
	if _, err := os.Stat(first.Path); !os.IsNotExist(err) {
		t.Fatalf("old file should be removed, stat err = %v", err)
	}
	if reloaded := loadIndex(s.index); len(reloaded) != 2 || reloaded[1].Path != got.Path {
		t.Fatalf("index not updated: %+v", reloaded)
	}
	if _, err := s.Replace("nope", edited); err == nil {
		t.Fatal("Replace of an unknown id should fail")
	}
}
