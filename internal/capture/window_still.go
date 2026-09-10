package capture

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// EncodeWindowStill composes src with macOS-style transparent padding + shadow
// (per opt) and writes a LOSSLESS temp PNG, returning its path.
func EncodeWindowStill(src image.Image, opt WindowComposeOpts) (string, error) {
	if src == nil {
		return "", fmt.Errorf("encode window still: nil image")
	}
	composed := ComposeWindowStill(src, opt)

	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("encode window still: mkdir temp: %w", err)
	}
	out, err := os.CreateTemp(dir, "toru-win-*.png")
	if err != nil {
		return "", fmt.Errorf("encode window still: create temp: %w", err)
	}
	if err := png.Encode(out, composed); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("encode window still: encode png: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("encode window still: close png: %w", err)
	}
	return out.Name(), nil
}

// CropImageRGBA crops sub (monitor-local PHYSICAL px) out of src into a fresh
// *image.RGBA with Bounds().Min at (0,0). Same clamp semantics as CropImage.
func CropImageRGBA(src image.Image, sub Rect) (*image.RGBA, error) {
	if sub.W <= 0 || sub.H <= 0 {
		return nil, fmt.Errorf("crop image rgba: invalid sub rect %dx%d (W/H must be > 0)", sub.W, sub.H)
	}
	b := src.Bounds()
	want := image.Rect(
		b.Min.X+sub.X,
		b.Min.Y+sub.Y,
		b.Min.X+sub.X+sub.W,
		b.Min.Y+sub.Y+sub.H,
	)
	r := want.Intersect(b)
	if r.Empty() {
		return nil, fmt.Errorf("crop image rgba: sub rect %+v does not intersect bounds %+v", sub, b)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return dst, nil
}

// StitchImageMulti is the in-memory counterpart of CropImageMulti: stitches a
// virtual-desktop region from per-monitor images into one *image.RGBA (dead
// zones filled opaque black). Used by window capture before shadow compose.
func StitchImageMulti(frozens map[int]*image.RGBA, screens []ScreenInfo, vr Rect) (*image.RGBA, error) {
	if vr.W <= 0 || vr.H <= 0 {
		return nil, fmt.Errorf("stitch multi: invalid rect %dx%d (W/H must be > 0)", vr.W, vr.H)
	}
	out := image.NewRGBA(image.Rect(0, 0, vr.W, vr.H))
	draw.Draw(out, out.Bounds(), image.NewUniform(blackOpaque), image.Point{}, draw.Src)

	want := image.Rect(vr.X, vr.Y, vr.X+vr.W, vr.Y+vr.H)
	covered := false
	for _, sc := range screens {
		img := frozens[sc.ID]
		if img == nil {
			continue
		}
		monRect := image.Rect(sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H)
		isect := want.Intersect(monRect)
		if isect.Empty() {
			continue
		}
		sp := img.Bounds().Min.Add(image.Pt(isect.Min.X-sc.X, isect.Min.Y-sc.Y))
		dst := image.Rect(
			isect.Min.X-vr.X,
			isect.Min.Y-vr.Y,
			isect.Max.X-vr.X,
			isect.Max.Y-vr.Y,
		)
		draw.Draw(out, dst, img, sp, draw.Src)
		covered = true
	}
	if !covered {
		return nil, fmt.Errorf("stitch multi: rect %+v intersects no monitor image", vr)
	}
	return out, nil
}
