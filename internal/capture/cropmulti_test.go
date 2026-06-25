package capture

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// solid returns a w×h opaque RGBA filled with c, origin (0,0) — exactly what
// kbinani screenshot.CaptureRect returns (a zero-origin image regardless of the
// monitor's virtual position).
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func decodePNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open stitched png: %v", err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("decode stitched png: %v", err)
	}
	return img
}

// Two monitors side by side, same scale, tiling cleanly:
//
//	mon 0: (0,0) 100×100   red
//	mon 1: (100,0) 100×100 blue
//
// A crop straddling the seam should be red on its left half and blue on its
// right half, pixel-exact, with no rescaling.
func TestCropImageMulti_HorizontalSeam(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}
	screens := []ScreenInfo{
		{ID: 0, X: 0, Y: 0, W: 100, H: 100},
		{ID: 1, X: 100, Y: 0, W: 100, H: 100},
	}
	frozens := map[int]*image.RGBA{
		0: solid(100, 100, red),
		1: solid(100, 100, blue),
	}
	// Crop [80,20 .. 140,80): 20px on mon0 (x 80..100), 40px on mon1 (x 100..140).
	vr := Rect{X: 80, Y: 20, W: 60, H: 60}
	path, err := CropImageMulti(frozens, screens, vr)
	if err != nil {
		t.Fatalf("CropImageMulti: %v", err)
	}
	defer os.Remove(path)

	img := decodePNG(t, path)
	if got := img.Bounds().Dx(); got != 60 {
		t.Fatalf("stitched width = %d, want 60", got)
	}
	if got := img.Bounds().Dy(); got != 60 {
		t.Fatalf("stitched height = %d, want 60", got)
	}
	// Output x in [0,20) came from mon0 (red); x in [20,60) from mon1 (blue).
	assertColor(t, img, 0, 0, red)    // far left -> red
	assertColor(t, img, 19, 30, red)  // last red column (virtual x=99)
	assertColor(t, img, 20, 30, blue) // first blue column (virtual x=100)
	assertColor(t, img, 59, 59, blue) // far bottom-right -> blue
}

// A crop fully inside one monitor still works through the multi path (single hit).
func TestCropImageMulti_SingleMonitor(t *testing.T) {
	green := color.RGBA{G: 200, A: 255}
	screens := []ScreenInfo{{ID: 0, X: 0, Y: 0, W: 200, H: 200}}
	frozens := map[int]*image.RGBA{0: solid(200, 200, green)}
	vr := Rect{X: 50, Y: 50, W: 40, H: 30}
	path, err := CropImageMulti(frozens, screens, vr)
	if err != nil {
		t.Fatalf("CropImageMulti: %v", err)
	}
	defer os.Remove(path)
	img := decodePNG(t, path)
	if img.Bounds().Dx() != 40 || img.Bounds().Dy() != 30 {
		t.Fatalf("size = %dx%d, want 40x30", img.Bounds().Dx(), img.Bounds().Dy())
	}
	assertColor(t, img, 0, 0, green)
	assertColor(t, img, 39, 29, green)
}

// Monitors with NEGATIVE origin (a monitor to the LEFT of primary) and a dead
// zone (the crop pokes below the shorter monitor) must stitch correctly and fill
// the dead zone opaque black.
func TestCropImageMulti_NegativeOriginAndDeadZone(t *testing.T) {
	// mon -? left monitor at (-100,0) 100×60 (SHORT); primary at (0,0) 100×100.
	// Crop [-40,40 .. 40,100): left half from the short left monitor (only rows
	// 40..60 exist there -> rows 60..100 are dead zone = black), right half from
	// the primary (full height).
	cyan := color.RGBA{G: 255, B: 255, A: 255}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	screens := []ScreenInfo{
		{ID: 0, X: -100, Y: 0, W: 100, H: 60},
		{ID: 1, X: 0, Y: 0, W: 100, H: 100},
	}
	frozens := map[int]*image.RGBA{
		0: solid(100, 60, cyan),
		1: solid(100, 100, white),
	}
	vr := Rect{X: -40, Y: 40, W: 80, H: 60} // x:-40..40, y:40..100
	path, err := CropImageMulti(frozens, screens, vr)
	if err != nil {
		t.Fatalf("CropImageMulti: %v", err)
	}
	defer os.Remove(path)
	img := decodePNG(t, path)

	// Output (0,0) == virtual (-40,40): inside the short left monitor -> cyan.
	assertColor(t, img, 0, 0, cyan)
	// Output (39,0) == virtual (-1,40): still left monitor -> cyan.
	assertColor(t, img, 39, 0, cyan)
	// Output (0,59) == virtual (-40,99): left monitor only spans y<60, so this row
	// is a DEAD ZONE -> opaque black.
	assertColor(t, img, 0, 59, color.RGBA{A: 255})
	// Output (40,0) == virtual (0,40): primary -> white.
	assertColor(t, img, 40, 0, white)
	// Output (79,59) == virtual (39,99): primary, full height -> white.
	assertColor(t, img, 79, 59, white)
}

func TestCropImageMulti_NoIntersection(t *testing.T) {
	screens := []ScreenInfo{{ID: 0, X: 0, Y: 0, W: 100, H: 100}}
	frozens := map[int]*image.RGBA{0: solid(100, 100, color.RGBA{A: 255})}
	if _, err := CropImageMulti(frozens, screens, Rect{X: 500, Y: 500, W: 10, H: 10}); err == nil {
		t.Fatal("expected error for a rect intersecting no monitor, got nil")
	}
}

func assertColor(t *testing.T, img image.Image, x, y int, want color.RGBA) {
	t.Helper()
	r, g, b, a := img.At(x, y).RGBA()
	wr, wg, wb, wa := want.RGBA()
	if r != wr || g != wg || b != wb || a != wa {
		t.Errorf("pixel (%d,%d) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
			x, y, r>>8, g>>8, b>>8, a>>8, wr>>8, wg>>8, wb>>8, wa>>8)
	}
}
