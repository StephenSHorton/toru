//go:build windows

package capture

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"github.com/kbinani/screenshot"
)

// captureStill grabs the request Rect (virtual-desktop PHYSICAL pixels) off the
// live desktop and encodes it to a PNG temp file, returning the path.
//
// Coordinate note: kbinani/screenshot's coordinate space is identical to the
// frozen contract Rect — origin = primary-monitor top-left, monitors to the
// left/above carry NEGATIVE X/Y. Per-Monitor-V2 awareness is set in main()
// BEFORE any window is created, so these are true physical pixels. There is NO
// rebasing here; rebasing is exclusively the ddagrab VIDEO path (args.go).
//
// Despite the file name, the implementation is GDI BitBlt (kbinani), which is
// CGO-free and correct for a single still. DXGI proper is reserved for video.
func captureStill(req CaptureRequest) (string, error) {
	// Guard absurd / empty rects: image.NewRGBA panics on a degenerate or huge
	// rectangle, and the underlying GDI calls are undefined for W/H <= 0.
	if req.Rect.W <= 0 || req.Rect.H <= 0 {
		return "", fmt.Errorf("capture still: invalid rect %dx%d (W/H must be > 0)", req.Rect.W, req.Rect.H)
	}

	// image.Rect takes MIN/MAX corners — pass (X, Y, X+W, Y+H), not W/H.
	r := image.Rect(req.Rect.X, req.Rect.Y, req.Rect.X+req.Rect.W, req.Rect.Y+req.Rect.H)

	img, err := screenshot.CaptureRect(r)
	if err != nil {
		return "", fmt.Errorf("capture still: %w", err)
	}

	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("capture still: mkdir temp: %w", err)
	}

	f, err := os.CreateTemp(dir, "toru-shot-*.png")
	if err != nil {
		return "", fmt.Errorf("capture still: create temp: %w", err)
	}
	// Close + remove on any encode failure so we never leave a 0-byte PNG.
	defer func() { _ = f.Close() }()

	if err := png.Encode(f, img); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("capture still: encode png: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("capture still: close png: %w", err)
	}
	return f.Name(), nil
}
