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

// FreezeMonitor grabs the LIVE desktop region described by b (virtual-desktop
// PHYSICAL pixels — origin = primary top-left, monitors left/above carry
// NEGATIVE coords) and writes it to a frozen PNG temp file, returning the path.
//
// It is the per-monitor still grab used at overlay-session start. It MUST run
// BEFORE any overlay window is shown, otherwise the still would photograph the
// dim overlay itself. The coordinate space is identical to kbinani/screenshot
// and the frozen contract Rect; there is NO rebasing here.
//
// Implementation mirrors still_dxgi_windows.go's temp+encode pattern (GDI BitBlt
// via kbinani, CGO-free; correct for a single still).
func FreezeMonitor(b image.Rectangle) (string, error) {
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return "", fmt.Errorf("freeze monitor: invalid bounds %dx%d (W/H must be > 0)", b.Dx(), b.Dy())
	}

	img, err := screenshot.CaptureRect(b)
	if err != nil {
		return "", fmt.Errorf("freeze monitor: %w", err)
	}

	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("freeze monitor: mkdir temp: %w", err)
	}

	f, err := os.CreateTemp(dir, "toru-frozen-*.png")
	if err != nil {
		return "", fmt.Errorf("freeze monitor: create temp: %w", err)
	}
	defer func() { _ = f.Close() }()

	// Encode at BestSpeed: this is the dominant overlay-startup cost (default
	// compression PNG-encoding a 4K monitor is ~0.5-1s). BestSpeed is still
	// LOSSLESS — required, because CommitScreenshot crops the saved screenshot
	// out of this exact PNG — but encodes several times faster.
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("freeze monitor: encode png: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("freeze monitor: close png: %w", err)
	}
	return f.Name(), nil
}

// DisplayBounds describes one enumerated monitor in virtual-desktop PHYSICAL px
// (origin = primary top-left; monitors left/above carry NEGATIVE X/Y). It is the
// raw kbinani enumeration the overlay enriches with DPI scale + primary flag.
type DisplayBounds struct {
	Index int
	X, Y  int
	W, H  int
}

// EnumDisplays enumerates every active monitor via kbinani/screenshot. The
// returned Index MUST equal the kbinani loop index (== ddagrab output_idx); the
// slice is never sorted or deduped. This keeps Overlay.ListScreens cross-platform
// (the kbinani calls are Windows-only and live behind this build tag).
func EnumDisplays() []DisplayBounds {
	n := screenshot.NumActiveDisplays()
	out := make([]DisplayBounds, 0, n)
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		out = append(out, DisplayBounds{
			Index: i,
			X:     b.Min.X,
			Y:     b.Min.Y,
			W:     b.Dx(),
			H:     b.Dy(),
		})
	}
	return out
}
