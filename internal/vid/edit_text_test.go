package vid

import (
	"context"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// testFont stands in for Segoe UI in the pure tests: TextFilter only checks
// that the file exists.
func testFont(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "font.ttf")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTextFilter(t *testing.T) {
	font := testFont(t)
	dir := t.TempDir()
	texts := []TextOverlay{
		{Text: "It's 5:00 — \"go\", [ok]; 100%\r\nline two 🎉👍🏽", X: 0.5, Y: 0.25, Size: "m", StartMs: 1500, EndMs: 4000},
		{Text: "small", X: -1, Y: 2, Size: "s", StartMs: 0, EndMs: 250},
		{Text: " 🎉 \t", X: 0.5, Y: 0.5, Size: "l", StartMs: 0, EndMs: 1000}, // nothing drawable: skipped
		{Text: "big", X: 0.1, Y: 0.9, Size: "l", StartMs: 61_000, EndMs: 62_500},
	}
	filter, files, err := TextFilter(texts, 1920, 1080, font, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("%d text files, want 3", len(files))
	}
	wantBodies := []string{"It's 5:00 — \"go\", [ok]; 100%\nline two", "small", "big"}
	for i, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if string(b) != wantBodies[i] {
			t.Errorf("file %d = %q, want %q", i, b, wantBodies[i])
		}
	}

	parts := strings.Split(filter, ",drawtext=")
	if len(parts) != 3 {
		t.Fatalf("%d drawtexts in %s", len(parts), filter)
	}
	esc := func(p string) string { return filterValue(filepath.ToSlash(p)) }
	want := []string{
		"drawtext=fontfile=" + esc(font) + ":textfile=" + esc(files[0]) +
			`:expansion=none:fontsize=65:fontcolor=white:borderw=4:bordercolor=black@0.8:text_align=C:y_align=font` +
			`:x=960-text_w/2:y=270-text_h/2:enable=between(t\,1.500\,4.000)`,
		"fontfile=" + esc(font) + ":textfile=" + esc(files[1]) +
			`:expansion=none:fontsize=43:fontcolor=white:borderw=3:bordercolor=black@0.8:text_align=C:y_align=font` +
			`:x=0-text_w/2:y=1080-text_h/2:enable=between(t\,0.000\,0.250)`,
		"fontfile=" + esc(font) + ":textfile=" + esc(files[2]) +
			`:expansion=none:fontsize=97:fontcolor=white:borderw=6:bordercolor=black@0.8:text_align=C:y_align=font` +
			`:x=192-text_w/2:y=972-text_h/2:enable=between(t\,61.000\,62.500)`,
	}
	for i := range want {
		if parts[i] != want[i] {
			t.Errorf("drawtext %d\n got: %s\nwant: %s", i, parts[i], want[i])
		}
	}
}

func TestTextFilterEmptyAndErrors(t *testing.T) {
	font := testFont(t)
	dir := t.TempDir()
	if f, files, err := TextFilter(nil, 1920, 1080, font, dir); f != "" || files != nil || err != nil {
		t.Errorf("no texts: %q %v %v", f, files, err)
	}
	if f, files, err := TextFilter([]TextOverlay{{Text: "\n 🎉", Size: "m", EndMs: 1}}, 1920, 1080, font, dir); f != "" || len(files) != 0 || err != nil {
		t.Errorf("only blank texts: %q %v %v", f, files, err)
	}
	bad := []struct {
		name  string
		texts []TextOverlay
		w, h  int
		font  string
	}{
		{"size", []TextOverlay{{Text: "a", Size: "xl", EndMs: 1}}, 100, 100, font},
		{"time", []TextOverlay{{Text: "a", Size: "s", StartMs: 5, EndMs: 5}}, 100, 100, font},
		{"negative start", []TextOverlay{{Text: "a", Size: "s", StartMs: -1, EndMs: 5}}, 100, 100, font},
		{"frame", []TextOverlay{{Text: "a", Size: "s", EndMs: 5}}, 0, 100, font},
		{"font", []TextOverlay{{Text: "a", Size: "s", EndMs: 5}}, 100, 100, filepath.Join(dir, "missing.ttf")},
		// the first is written before the second fails: it must be cleaned up
		{"late failure", []TextOverlay{{Text: "a", Size: "s", EndMs: 5}, {Text: "b", Size: "?", EndMs: 5}}, 100, 100, font},
	}
	for _, c := range bad {
		if _, files, err := TextFilter(c.texts, c.w, c.h, c.font, dir); err == nil || files != nil {
			t.Errorf("%s: err=%v files=%v", c.name, err, files)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "toru-text-*")); len(left) != 0 {
		t.Errorf("text files left behind: %v", left)
	}
}

func TestTextSizes(t *testing.T) {
	cases := []struct {
		size string
		h    int
		want int
	}{
		{"s", 1080, 43}, {"m", 1080, 65}, {"l", 1080, 97},
		{"s", 1440, 58}, {"m", 720, 43}, {"l", 360, 32}, {"s", 10, 1},
	}
	for _, c := range cases {
		if got := TextFontSize(c.size, c.h); got != c.want {
			t.Errorf("TextFontSize(%q, %d) = %d, want %d", c.size, c.h, got, c.want)
		}
	}
}

func TestCleanOverlayText(t *testing.T) {
	cases := map[string]string{
		"plain":                   "plain",
		"a\r\nb\rc\td":            "a\nb\nc d",
		"gg 🎉 ez":                 "gg  ez",
		"👨‍👩‍👧 family":            " family",
		"1️⃣ 🇬🇧 ⭐ ✔️ ♥":           "1",
		"\n\n  top \n\nend  \n\n": "  top\n\nend",
		"   ":                     "",
		"Ünïcödé — “quotes” ©®™":  "Ünïcödé — “quotes” ©®™",
		"日本語":                     "日本語",
	}
	for in, want := range cases {
		if got := CleanOverlayText(in); got != want {
			t.Errorf("CleanOverlayText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterValue(t *testing.T) {
	cases := map[string]string{
		"C:/Windows/Fonts/seguisb.ttf": `C\\:/Windows/Fonts/seguisb.ttf`,
		"C:/Users/O'Brien/t.txt":       `C\\:/Users/O\\\'Brien/t.txt`,
		"between(t,1.000,2.000)":       `between(t\,1.000\,2.000)`,
		"a[b];c":                       `a\[b\]\;c`,
		`back\slash`:                   `back\\\\slash`,
	}
	for in, want := range cases {
		if got := filterValue(in); got != want {
			t.Errorf("filterValue(%q) = %s, want %s", in, got, want)
		}
	}
}

// TestTextFilterRender draws overlays with real ffmpeg and Segoe UI from a
// temp dir whose name needs every kind of escaping, then checks each text's
// ink is centred where the overlay says and shows only during its time.
//
//	go test ./internal/vid -run TextFilterRender -v
func TestTextFilterRender(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg; skipped with -short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	if _, err := os.Stat(DefaultTextFont); err != nil {
		t.Skipf("no Segoe UI Semibold: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := capture.RunFFmpeg(ctx, []string{
		"-hide_banner", "-f", "lavfi", "-i", "nullsrc=size=16x16", "-frames:v", "1", "-f", "null", "-",
	}); err != nil {
		t.Skipf("ffmpeg has no lavfi device: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "O'Brien [x], y; z")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const w, h = 640, 360
	texts := []TextOverlay{
		{Text: "It's 5:00, \"go\" [ok]; 100% 🎉\nline two", X: 0.3, Y: 0.3, Size: "l", StartMs: 0, EndMs: 1000},
		{Text: "later", X: 0.75, Y: 0.8, Size: "m", StartMs: 1000, EndMs: 2000},
	}
	filter, files, err := TextFilter(texts, w, h, DefaultTextFont, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, f := range files {
			_ = os.Remove(f)
		}
	}()

	frame := func(atMs int) image.Image {
		out := filepath.Join(dir, "f.png")
		if err := capture.RunFFmpeg(ctx, []string{"-y",
			"-f", "lavfi", "-i", "color=c=black:s=640x360:r=10:d=2",
			"-filter_complex", "[0:v]" + filter + "[v]", "-map", "[v]",
			"-ss", msSeconds(atMs), "-frames:v", "1", out,
		}); err != nil {
			t.Fatalf("render: %v", err)
		}
		fh, err := os.Open(out)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fh.Close() }()
		img, err := png.Decode(fh)
		if err != nil {
			t.Fatal(err)
		}
		return img
	}

	// At 0.5s only the first text: its white ink centres on (0.3, 0.3) of
	// the frame (within a few px: the ink isn't the line box).
	first := frame(500)
	box, ok := inkBox(first, image.Rect(0, 0, w, h))
	if !ok {
		t.Fatal("no text at 0.5s")
	}
	t.Logf("first text ink %v", box)
	cx, cy := (box.Min.X+box.Max.X)/2, (box.Min.Y+box.Max.Y)/2
	if abs(cx-int(0.3*w)) > 6 || abs(cy-int(0.3*h)) > 6 {
		t.Errorf("first text centred at %d,%d, want about %d,%d", cx, cy, int(0.3*w), int(0.3*h))
	}
	if box.Dy() < 2*TextFontSize("l", h) {
		t.Errorf("first text is %dpx tall: the second line is missing", box.Dy())
	}
	if _, ok := inkBox(first, image.Rect(w/2, h/2, w, h)); ok {
		t.Error("second text shows before its start")
	}

	// At 1.5s only the second.
	second := frame(1500)
	if _, ok := inkBox(second, image.Rect(0, 0, w/2, h/2)); ok {
		t.Error("first text still shows after its end")
	}
	box, ok = inkBox(second, image.Rect(0, 0, w, h))
	if !ok {
		t.Fatal("no text at 1.5s")
	}
	t.Logf("second text ink %v", box)
	cx = (box.Min.X + box.Max.X) / 2
	if abs(cx-int(0.75*w)) > 4 {
		t.Errorf("second text centred at x=%d, want about %d", cx, int(0.75*w))
	}
}

// inkBox is the bounding box of near-white pixels inside r.
func inkBox(img image.Image, r image.Rectangle) (image.Rectangle, bool) {
	var box image.Rectangle
	found := false
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			if cr>>8 < 200 || cg>>8 < 200 || cb>>8 < 200 {
				continue
			}
			p := image.Rect(x, y, x+1, y+1)
			if !found {
				box, found = p, true
			} else {
				box = box.Union(p)
			}
		}
	}
	return box, found
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
