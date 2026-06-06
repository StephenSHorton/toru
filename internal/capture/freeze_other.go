//go:build !windows

package capture

import (
	"errors"
	"image"
)

// FreezeMonitor is Windows-only (kbinani/screenshot via GDI BitBlt). This stub
// exists so the module cross-compiles for tooling/CI on non-Windows hosts.
func FreezeMonitor(b image.Rectangle) (string, error) {
	_ = b
	return "", errors.New("toru/capture: monitor freeze is Windows-only")
}

// FreezeMonitorImage is Windows-only (kbinani/screenshot via GDI BitBlt). This
// stub exists so the module cross-compiles for tooling/CI on non-Windows hosts.
func FreezeMonitorImage(b image.Rectangle) (*image.RGBA, error) {
	_ = b
	return nil, errors.New("toru/capture: monitor freeze is Windows-only")
}

// DisplayBounds describes one enumerated monitor (virtual-desktop PHYSICAL px).
// Mirrors the Windows definition so internal/overlay stays cross-platform.
type DisplayBounds struct {
	Index int
	X, Y  int
	W, H  int
}

// EnumDisplays returns no displays off-Windows (the kbinani enumeration is
// Windows-only). Overlay code falls back gracefully when this is empty.
func EnumDisplays() []DisplayBounds { return nil }
