package vid

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFontURL(t *testing.T) {
	for _, name := range []string{"Arial", "Segoe UI Semibold", "微軟正黑體 Light", "A/B %20 C"} {
		u := FontURL(name)
		got, ok := FontNameFromURLPath(u)
		if !ok || got != name {
			t.Errorf("FontNameFromURLPath(%q) = %q, %v; want %q", u, got, ok, name)
		}
	}
	for _, p := range []string{"/__media/font/", "/__media/thumb/x.jpg", "/__media/font/%zz"} {
		if n, ok := FontNameFromURLPath(p); ok {
			t.Errorf("FontNameFromURLPath(%q) = %q, want no name", p, n)
		}
	}
}

func TestTextPx(t *testing.T) {
	cases := []struct {
		t    TextOverlay
		h    int
		want int
	}{
		{TextOverlay{Size: "m"}, 1080, 65},
		{TextOverlay{Size: "l", FontPx: 120}, 1080, 120}, // FontPx wins over Size
		{TextOverlay{FontPx: 5000}, 1080, MaxTextFontPx},
	}
	for _, c := range cases {
		if got := textPx(c.t, c.h); got != c.want {
			t.Errorf("textPx(%+v, %d) = %d, want %d", c.t, c.h, got, c.want)
		}
	}
}

// TestTextFilterFontPx: a text with FontPx and no (or an unknown) Size is
// valid and draws at FontPx; a font that isn't installed falls back.
func TestTextFilterFontPx(t *testing.T) {
	font := testFont(t)
	texts := []TextOverlay{{Text: "hi", X: 0.5, Y: 0.5, FontPx: 77, Font: "No Such Font 123", EndMs: 1000}}
	filter, _, err := TextFilter(texts, 1920, 1080, font, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filter, "fontsize=77:") {
		t.Errorf("filter has no fontsize=77: %s", filter)
	}
	if !strings.Contains(filter, "fontfile="+filterValue(filepath.ToSlash(font))+":") {
		t.Errorf("a missing font should fall back to the default file: %s", filter)
	}
}

// TestInstalledFonts reads this PC's fonts (Windows only): Arial is listed
// with its own file and Segoe UI's metrics, and texts in it draw from that
// file.
func TestInstalledFonts(t *testing.T) {
	if _, err := os.Stat(`C:\Windows\Fonts\arial.ttf`); err != nil {
		t.Skip("no Arial on this machine")
	}
	fs := InstalledFonts(true)
	if len(fs) == 0 {
		t.Fatal("no installed fonts")
	}
	for i := 1; i < len(fs); i++ {
		if fs[i-1].Name >= fs[i].Name {
			t.Fatalf("not sorted or repeated: %q, %q", fs[i-1].Name, fs[i].Name)
		}
	}
	byName := map[string]Font{}
	for _, f := range fs {
		byName[f.Name] = f
	}
	arial, ok := byName["Arial"]
	if !ok {
		t.Fatal("Arial isn't listed")
	}
	if !strings.EqualFold(filepath.Base(arial.path), "arial.ttf") || FontFile("Arial") != arial.path {
		t.Errorf("Arial's file is %q (FontFile %q)", arial.path, FontFile("Arial"))
	}
	if arial.URL != "/__media/font/Arial" || arial.Family != "Arial" {
		t.Errorf("Arial = %+v", arial)
	}
	// Segoe UI Semibold: hhea 2210/-514/0, win 2210/514 at 2048 units.
	if s, ok := byName["Segoe UI Semibold"]; ok {
		if s.UnitsPerEm != 2048 || s.Ascent != 2210 || s.CSSAscent != 2210 || s.CSSDescent != 514 || s.LineHeight < 1.33 || s.LineHeight > 1.331 {
			t.Errorf("Segoe UI Semibold metrics = %+v", s)
		}
	}
	// Icon and symbol fonts have no "A" and aren't offered.
	for _, n := range []string{"Webdings", "Wingdings", "Marlett", "Segoe MDL2 Assets"} {
		if _, ok := byName[n]; ok {
			t.Errorf("%s is listed", n)
		}
	}
	filter, _, err := TextFilter([]TextOverlay{{Text: "x", FontPx: 40, Font: "Arial", EndMs: 1}}, 64, 64, DefaultTextFont, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(filter, "fontfile="+filterValue(filepath.ToSlash(arial.path))+":") {
		t.Errorf("Arial text doesn't draw from %s: %s", arial.path, filter)
	}
}

// TestCheckFont draws with real ffmpeg: Arial works, Mongolian Baiti (which
// loads but fails in drawtext) and an unknown name don't.
func TestCheckFont(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg; skipped with -short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	if FontFile("Arial") == "" {
		t.Skip("no Arial on this machine")
	}
	ctx := context.Background()
	if err := CheckFont(ctx, "Arial"); err != nil {
		t.Errorf("Arial: %v", err)
	}
	if err := CheckFont(ctx, "No Such Font 123"); err == nil {
		t.Error("an unknown font passed")
	}
	if FontFile("Mongolian Baiti") != "" {
		if err := CheckFont(ctx, "Mongolian Baiti"); err == nil {
			t.Error("Mongolian Baiti passed; drawtext fails on it")
		}
	}
}
