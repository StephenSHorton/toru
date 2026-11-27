package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/boardexport"
)

func useThemeDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := whiteboardThemeDir
	whiteboardThemeDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { whiteboardThemeDir = old })
	return dir
}

func TestWhiteboardThemeDefaultsToLight(t *testing.T) {
	useThemeDir(t)
	if got := loadWhiteboardTheme(); got != themeLight {
		t.Fatalf("no saved theme: got %q, want light", got)
	}
}

func TestWhiteboardThemeIsSaved(t *testing.T) {
	dir := useThemeDir(t)
	var emitted []any
	s := &WhiteboardsService{emit: func(name string, data any) {
		if name == evWhiteboardTheme {
			emitted = append(emitted, data)
		}
	}}
	if err := s.SetTheme("dark"); err != nil {
		t.Fatal(err)
	}
	if got := s.Theme(); got != themeDark {
		t.Fatalf("after SetTheme(dark): got %q", got)
	}
	if len(emitted) != 1 || emitted[0] != "dark" {
		t.Fatalf("windows told %v, want [dark]", emitted)
	}
	if err := s.SetTheme("purple"); err == nil {
		t.Fatal("SetTheme(purple) saved a theme that doesn't exist")
	}
	if got := s.Theme(); got != themeDark {
		t.Fatalf("a refused theme changed it: got %q", got)
	}
	// A file written by hand with a BOM still reads.
	if err := os.WriteFile(filepath.Join(dir, "whiteboard.json"), append(append([]byte{}, utf8BOM...), `{"theme":"light"}`...), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Theme(); got != themeLight {
		t.Fatalf("BOM file: got %q", got)
	}
	// A broken file is light.
	if err := os.WriteFile(filepath.Join(dir, "whiteboard.json"), []byte(`{"theme":`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Theme(); got != themeLight {
		t.Fatalf("broken file: got %q", got)
	}
}

func TestBoardExportCarriesTheTheme(t *testing.T) {
	useThemeDir(t)
	viewer := `<html><head><title>Toru board</title></head><body><script id="toru-board" type="application/json">null</script></body></html>`
	e := newBoardExporter()
	e.Viewer = func() ([]byte, error) { return []byte(viewer), nil }
	b := board.Board{ID: "b1", Name: "Themes"}
	for _, theme := range []string{themeLight, themeDark} {
		if err := saveWhiteboardTheme(theme); err != nil {
			t.Fatal(err)
		}
		page, err := e.Export(t.Context(), b, boardexport.HTML)
		if err != nil {
			t.Fatal(err)
		}
		if want := `"theme":"` + theme + `"`; !strings.Contains(string(page), want) {
			t.Fatalf("theme %s: the page's board payload lacks %s", theme, want)
		}
	}
}
