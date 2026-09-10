package capture

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestComposeWindowStill_transparentPadAndShadow(t *testing.T) {
	// Solid red 40×30 "window".
	src := image.NewRGBA(image.Rect(0, 0, 40, 30))
	draw.Draw(src, src.Bounds(), &image.Uniform{C: color.RGBA{R: 255, A: 255}}, image.Point{}, draw.Src)

	opt := WindowComposeOpts{
		Pad:         20,
		Blur:        4,
		OffsetY:     4,
		CornerR:     6,
		ShadowAlpha: 120,
	}
	out := ComposeWindowStill(src, opt)
	if out.Bounds().Dx() != 40+40 || out.Bounds().Dy() != 30+40 {
		t.Fatalf("size = %dx%d, want 80x70", out.Bounds().Dx(), out.Bounds().Dy())
	}

	// Corner of the output canvas must be fully transparent (pad).
	if _, _, _, a := out.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("pad corner alpha = %d, want 0", a)
	}

	// Center of the window content (pad+20, pad+15) must be opaque red-ish.
	cx, cy := 20+20, 20+15
	r, g, b, a := out.At(cx, cy).RGBA()
	if a>>8 < 250 {
		t.Fatalf("window center alpha = %d, want ~255", a>>8)
	}
	if r>>8 < 250 || g>>8 > 10 || b>>8 > 10 {
		t.Fatalf("window center color = %d,%d,%d want red", r>>8, g>>8, b>>8)
	}

	// A pixel in the shadow band below the window (not under opaque content)
	// should have some black alpha, not zero and not full window red.
	sx, sy := 20+20, 20+30+2 // just under the window, with OffsetY
	_, _, _, sa := out.At(sx, sy).RGBA()
	if sa == 0 {
		t.Fatalf("expected shadow alpha below window, got 0")
	}
}

func TestComposeWindowStill_skipBeautify(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 16, 12))
	draw.Draw(src, src.Bounds(), &image.Uniform{C: color.RGBA{B: 255, A: 255}}, image.Point{}, draw.Src)
	out := ComposeWindowStill(src, WindowComposeOpts{SkipBeautify: true, Pad: 40, CornerR: 8})
	if out.Bounds().Dx() != 16 || out.Bounds().Dy() != 12 {
		t.Fatalf("skip size = %dx%d, want 16x12", out.Bounds().Dx(), out.Bounds().Dy())
	}
}

func TestComposeWindowStill_roundedCornerTransparent(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	draw.Draw(src, src.Bounds(), &image.Uniform{C: color.RGBA{G: 255, A: 255}}, image.Point{}, draw.Src)
	out := ComposeWindowStill(src, WindowComposeOpts{
		Pad: 0, CornerR: 12, SkipBeautify: false,
	})
	// Extreme top-left corner of the window should be transparent after mask.
	if _, _, _, a := out.At(0, 0).RGBA(); a != 0 {
		t.Fatalf("rounded corner alpha = %d, want 0", a)
	}
	// Center stays opaque.
	if _, _, _, a := out.At(25, 25).RGBA(); a>>8 < 250 {
		t.Fatalf("center alpha = %d, want ~255", a>>8)
	}
}

func TestDefaultWindowComposeOpts_scales(t *testing.T) {
	a := DefaultWindowComposeOpts(1.0)
	b := DefaultWindowComposeOpts(2.0)
	if b.Pad != a.Pad*2 || b.CornerR != a.CornerR*2 {
		t.Fatalf("2x scale: pad %d→%d corner %d→%d", a.Pad, b.Pad, a.CornerR, b.CornerR)
	}
}
