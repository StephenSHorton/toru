package vid

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// textPNG is a w×h PNG data URL: an opaque red square with a half-transparent
// green border 2px wide, so both alpha and placement show in the export.
func textPNG(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := color.NRGBA{R: 255, A: 255}
			if x < 2 || y < 2 || x >= w-2 || y >= h-2 {
				c = color.NRGBA{G: 255, A: 128}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return pngDataPrefix + base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestProjectTextFilterChain(t *testing.T) {
	dir := t.TempDir()
	op := 0.5
	p := ProjectJSON{Canvas: CanvasJSON{Width: 640, Height: 360, FPS: 30}, Texts: []TextJSON{
		{ID: "a", Text: "A", Start: 0, End: 1000, Image: &TextImageJSON{PNG: textPNG(t, 8, 8), X: 10, Y: 20, W: 8, H: 8}},
		{ID: "b", Text: "B", Start: 500, End: 1500, Image: &TextImageJSON{PNG: textPNG(t, 8, 8), X: 30, Y: 40, W: 8, H: 8}, Style: &TextStyleJSON{Opacity: &op}},
		{ID: "c", Text: "C", Start: 200, End: 900, Image: &TextImageJSON{PNG: textPNG(t, 8, 8)},
			Style: &TextStyleJSON{OpacityKeys: []KeyframeJSON{{T: 0, V: 0.0, Ease: "linear"}, {T: 700, V: 1.0, Ease: "linear"}}}},
	}}
	f, err := ProjectTextFilter(p, dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"null[txb0];movie=filename=", "[txi0];[txb0][txi0]overlay=x=10:y=20:eof_action=repeat:enable=gte(t\\,0.000)*lt(t\\,1.000)",
		"[txb1];", "colorchannelmixer=aa=0.5", "overlay=x=30:y=40",
		"loop=loop=-1:size=1:start=0,settb=AVTB,setpts=N/(30*TB)", "colorchannelmixer@txo2=aa=0", "setpts=PTS+0.200/TB", "eof_action=pass",
	} {
		if !strings.Contains(f, want) {
			t.Errorf("filter lacks %q:\n%s", want, f)
		}
	}
	for i := range 3 {
		if _, err := os.Stat(filepath.Join(dir, "text-"+string(rune('0'+i))+".png")); err != nil {
			t.Errorf("text %d png: %v", i, err)
		}
	}

	// A text without its image is refused, not drawn some other way.
	p.Texts = append(p.Texts, TextJSON{ID: "d", Text: "lost", Start: 0, End: 100})
	if _, err := ProjectTextFilter(p, dir); err == nil || !strings.Contains(err.Error(), "wasn't rendered") {
		t.Errorf("text without image: err = %v", err)
	}
	// Not a PNG.
	p.Texts = []TextJSON{{ID: "e", Text: "x", Start: 0, End: 100, Image: &TextImageJSON{PNG: "data:image/png;base64,AAAA"}}}
	if _, err := ProjectTextFilter(p, dir); err == nil {
		t.Error("bad png accepted")
	}
}

// TestProjectTextExportInFFmpeg exports a black clip with text images and
// reads pixels back: the image lands at its x/y pixel for pixel, its alpha
// is kept, static opacity scales it, keyed opacity ramps, and it's gone
// outside its time. Skipped without ffmpeg ($TORU_FFMPEG or PATH).
func TestProjectTextExportInFFmpeg(t *testing.T) {
	ff := os.Getenv("TORU_FFMPEG")
	if ff == "" {
		var err error
		if ff, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg (set TORU_FFMPEG or put it on PATH)")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "black.mkv")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "color=black:s=320x180:r=30:d=2", "-c:v", "ffv1", src).CombinedOutput(); err != nil {
		t.Fatalf("make source: %v\n%s", err, out)
	}
	const W, H = 320, 180
	half := 0.5
	p := ProjectJSON{Version: 3, Canvas: CanvasJSON{Width: W, Height: H, FPS: 30},
		Media:  []MediaJSON{{ID: "m", Path: src, Kind: "video", Duration: 2000, Width: W, Height: H, FPS: 30}},
		Tracks: []TrackJSON{{ID: "v", Kind: "video", Clips: []ClipJSON{{ID: "c", Media: "m", Start: 0, In: 0, Out: 2000, Speed: 1, Volume: 1, Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}, Effects: []EffectJSON{}}}}},
		Texts: []TextJSON{
			{ID: "a", Text: "A", Start: 0, End: 1000, Image: &TextImageJSON{PNG: textPNG(t, 60, 40), X: 11, Y: 21, W: 60, H: 40}},
			{ID: "b", Text: "B", Start: 0, End: 2000, Image: &TextImageJSON{PNG: textPNG(t, 60, 40), X: 101, Y: 41, W: 60, H: 40}, Style: &TextStyleJSON{Opacity: &half}},
			{ID: "c", Text: "C", Start: 500, End: 1500, Image: &TextImageJSON{PNG: textPNG(t, 60, 40), X: 201, Y: 61, W: 60, H: 40},
				Style: &TextStyleJSON{OpacityKeys: []KeyframeJSON{{T: 0, V: 0.0, Ease: "inOut"}, {T: 1000, V: 1.0, Ease: "linear"}}}},
		}}
	work := t.TempDir()
	tf, err := ProjectTextFilter(p, work)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mkv")
	args, err := BuildProjectArgs(ProjectExport{Project: p, Preset: "original", OutPath: out}, "ffv1", tf, work)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("export: %v\n%s\n%q", err, b, args)
	}
	frame := func(n int) []byte {
		f, err := exec.Command(ff, "-v", "error", "-i", out, "-vf", "select=eq(n\\,"+itoa(n)+")", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
		if err != nil || len(f) != W*H*3 {
			t.Fatalf("frame %d: %v (%d bytes)", n, err, len(f))
		}
		return f
	}
	px := func(f []byte, x, y int) [3]float64 {
		i := (y*W + x) * 3
		return [3]float64{float64(f[i]), float64(f[i+1]), float64(f[i+2])}
	}
	near := func(name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.1f, want %.1f ± %.0f", name, got, want, tol)
		}
	}
	// Frame 15 = 0.5 s: a on, b at half, c just starting (opacity 0).
	f := frame(15)
	near("a: centre red", px(f, 41, 41)[0], 255, 12)
	near("a: green border (alpha 0.5)", px(f, 11, 41)[1], 128, 14)
	near("a: left of it untouched", px(f, 9, 41)[0], 0, 8)
	near("a: right of it untouched", px(f, 73, 41)[0], 0, 8)
	near("b: centre at half opacity", px(f, 131, 61)[0], 128, 14)
	near("c: keyed 0 at its start", px(f, 231, 81)[0], 0, 10)
	// Frame 31 = 1.03 s: a gone (it shows while t < end, as the player), c
	// 533 ms into an inOut ramp (the preview's fx/keyframes.ts ease).
	f = frame(31)
	near("a: gone after its end", px(f, 41, 41)[0], 0, 8)
	u := 1 - math.Pow(-2*0.5333+2, 3)/2
	near("c: keyed inOut part way", px(f, 231, 81)[0], 255*u, 14)
	// Frame 50 = 1.67 s: c gone.
	f = frame(50)
	near("c: gone after its end", px(f, 231, 81)[0], 0, 8)
	near("b: still there", px(f, 131, 61)[0], 128, 14)

	// Filters after the text chain keep chaining: a custom preset's scale, and the
	// export screen's GIF (fps, scale, palette).
	small := filepath.Join(dir, "small.mkv")
	args, err = BuildProjectArgs(ProjectExport{Project: p, Preset: "custom", Height: 90, OutPath: small}, "ffv1", tf, work)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("scaled export: %v\n%s\n%q", err, b, args)
	}
	fs, err := exec.Command(ff, "-v", "error", "-i", small, "-vf", "select=eq(n\\,15)", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil || len(fs) != 160*90*3 {
		t.Fatalf("scaled frame: %v (%d bytes)", err, len(fs))
	}
	near("scaled: a's centre at half size", float64(fs[(20*160+20)*3]), 255, 20)

	gif := filepath.Join(dir, "out.gif")
	args, err = BuildProjectArgs(ProjectExport{Project: p, Preset: "original", OutPath: gif, Options: &ExportOptions{Format: FormatGIF, GIF: GIFOptions{FPS: 10, Width: 320}}}, "ffv1", tf, work)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("gif export: %v\n%s\n%q", err, b, args)
	}
	fg, err := exec.Command(ff, "-v", "error", "-i", gif, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
	if err != nil || len(fg) != W*H*3 {
		t.Fatalf("gif frame: %v (%d bytes)", err, len(fg))
	}
	near("gif: a's centre", px(fg, 41, 41)[0], 255, 20)
}

func itoa(n int) string { return strconv.Itoa(n) }
