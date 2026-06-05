// Package shot is DEVELOPER 1's territory: server-side helpers for the
// screenshot annotation editor. Clipboard + save-as live in the shared
// internal/export package; this package holds screenshot-specific glue.
//
// JS binding name: ScreenshotService.*
package shot

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Service is the Wails-bound screenshot helper API (Developer 1).
type ScreenshotService struct{}

// New returns the screenshot service.
func New() *ScreenshotService { return &ScreenshotService{} }

// SavePNG decodes a base64 PNG (optionally a `data:image/png;base64,...` URL)
// produced by the Konva editor and writes it to a temp file, returning the
// path. The editor then hands that path to ExportService.SaveAs /
// ExportService.CopyToClipboard.
func (s *ScreenshotService) SavePNG(pngBase64 string) (string, error) {
	if i := strings.Index(pngBase64, ","); strings.HasPrefix(pngBase64, "data:") && i >= 0 {
		pngBase64 = pngBase64[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(pngBase64)
	if err != nil {
		return "", fmt.Errorf("savePNG: bad base64: %w", err)
	}
	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	out := filepath.Join(dir, "toru-edit.png")
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return "", err
	}
	return out, nil
}
