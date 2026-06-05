//go:build !windows

package capture

import "errors"

// captureStill is Windows-only (kbinani/screenshot via GDI BitBlt). This stub
// exists so the module cross-compiles for tooling/CI on non-Windows hosts.
func captureStill(req CaptureRequest) (string, error) {
	_ = req
	return "", errors.New("toru/capture: still capture is Windows-only")
}
