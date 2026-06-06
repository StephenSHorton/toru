package capture

import (
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// CropStill crops a region out of a FROZEN still PNG (captured at overlay-session
// start) and writes the result to a new temp PNG, returning its path.
//
// sub is MONITOR-LOCAL PHYSICAL px: the frozen PNG's origin is the monitor's own
// top-left, so sub.X/Y are offsets within that PNG (always >= 0 in practice; the
// region is clamped to the image bounds to guard fractional 1px overscan).
//
// CRITICAL: this is the ONLY path a committed screenshot takes. It NEVER calls
// the live capture path again — doing so would photograph the dim overlay
// windows themselves. It operates purely on the already-grabbed frozen pixels.
func CropStill(srcPath string, sub Rect) (string, error) {
	if sub.W <= 0 || sub.H <= 0 {
		return "", fmt.Errorf("crop still: invalid sub rect %dx%d (W/H must be > 0)", sub.W, sub.H)
	}

	f, err := os.Open(srcPath)
	if err != nil {
		return "", fmt.Errorf("crop still: open frozen png: %w", err)
	}
	src, err := png.Decode(f)
	_ = f.Close()
	if err != nil {
		return "", fmt.Errorf("crop still: decode frozen png: %w", err)
	}

	// Clamp the requested region to the frozen still's bounds. The frozen PNG's
	// own origin may be non-zero (image.Rect started at the monitor's virtual
	// coords), so translate sub into the image's coordinate space by adding the
	// image Min before intersecting.
	b := src.Bounds()
	want := image.Rect(
		b.Min.X+sub.X,
		b.Min.Y+sub.Y,
		b.Min.X+sub.X+sub.W,
		b.Min.Y+sub.Y+sub.H,
	)
	r := want.Intersect(b)
	if r.Empty() {
		return "", fmt.Errorf("crop still: sub rect %+v does not intersect frozen still bounds %+v", sub, b)
	}

	// Prefer the zero-copy SubImage view; fall back to draw.Draw if the concrete
	// image type does not expose SubImage.
	var cropped image.Image
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		cropped = si.SubImage(r)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
		cropped = dst
	}

	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("crop still: mkdir temp: %w", err)
	}
	out, err := os.CreateTemp(dir, "toru-shot-*.png")
	if err != nil {
		return "", fmt.Errorf("crop still: create temp: %w", err)
	}
	if err := png.Encode(out, cropped); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("crop still: encode png: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("crop still: close png: %w", err)
	}
	return out.Name(), nil
}

// CropImage crops sub (monitor-local PHYSICAL px) out of an in-memory image and
// writes a LOSSLESS temp PNG, returning its path. It mirrors CropStill's clamp /
// SubImage / draw fallback EXACTLY so the in-memory commit/save crop is
// byte-identical to the old disk path — this replaces the disk round-trip for both
// the edit base image and the final saved/copied screenshot.
//
// It carries NO build tag (pure std-lib image/png + image/draw), so it compiles
// on every host even though the freeze that produces src is Windows-only.
func CropImage(src image.Image, sub Rect) (string, error) {
	if sub.W <= 0 || sub.H <= 0 {
		return "", fmt.Errorf("crop image: invalid sub rect %dx%d (W/H must be > 0)", sub.W, sub.H)
	}

	// Clamp the requested region to the source bounds. The source image's own
	// origin may be non-zero (FreezeMonitorImage's CaptureRect uses the monitor's
	// virtual coords), so translate sub into the image's coordinate space by adding
	// the image Min before intersecting — identical to CropStill.
	b := src.Bounds()
	want := image.Rect(
		b.Min.X+sub.X,
		b.Min.Y+sub.Y,
		b.Min.X+sub.X+sub.W,
		b.Min.Y+sub.Y+sub.H,
	)
	r := want.Intersect(b)
	if r.Empty() {
		return "", fmt.Errorf("crop image: sub rect %+v does not intersect bounds %+v", sub, b)
	}

	var cropped image.Image
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if si, ok := src.(subImager); ok {
		cropped = si.SubImage(r)
	} else {
		dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
		draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
		cropped = dst
	}

	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("crop image: mkdir temp: %w", err)
	}
	out, err := os.CreateTemp(dir, "toru-shot-*.png")
	if err != nil {
		return "", fmt.Errorf("crop image: create temp: %w", err)
	}
	if err := png.Encode(out, cropped); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("crop image: encode png: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("crop image: close png: %w", err)
	}
	return out.Name(), nil
}
