package history

import (
	"os"
	"path/filepath"
	"testing"
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
