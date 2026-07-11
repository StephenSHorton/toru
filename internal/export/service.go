// Package export is the SHARED output service used by BOTH editors. It owns all
// clipboard-write and save-as logic for both media types so neither developer
// implements clipboard handling divergently.
//
//	image -> multi-format clipboard write (registered 'PNG' + CF_DIBV5 + CF_DIB)
//	video -> CF_HDROP file-drop reference (paste the file into Explorer/Discord/Slack/Teams)
//
// JS binding name: ExportService.*
package export

import (
	"fmt"
	"os"
)

// Media types accepted by CopyToClipboard.
const (
	MediaImage = "image"
	MediaVideo = "video"
)

// Service is the Wails-bound export API.
type ExportService struct{}

// NewService returns the shared export service.
func NewService() *ExportService { return &ExportService{} }

// CopyImageFile publishes a PNG at path to the system clipboard. Package-level
// so the overlay can auto-copy on capture without going through the Wails
// binding (and without the overlay package depending on the Service type).
func CopyImageFile(path string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("copy: source not found: %w", err)
	}
	return copyImageToClipboard(path)
}

// CopyToClipboard copies the file at path to the system clipboard.
//
//   - mediaType "image": publishes the PNG as a multi-format image bitmap so it
//     pastes correctly (incl. transparency) into modern AND legacy targets.
//   - mediaType "video": publishes the file as a CF_HDROP file-drop reference so
//     it pastes into Explorer / Discord / Slack / Teams. (Windows has no
//     universal "video bitstream on the clipboard" format; the file IS the payload.)
func (s *ExportService) CopyToClipboard(path, mediaType string) error {
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("copy: source not found: %w", err)
	}
	switch mediaType {
	case MediaImage:
		return copyImageToClipboard(path)
	case MediaVideo:
		return copyFileToClipboard(path)
	default:
		return fmt.Errorf("copy: unknown mediaType %q (want %q|%q)", mediaType, MediaImage, MediaVideo)
	}
}

// SaveAs opens a native Save-As dialog seeded with suggestedName and copies
// srcPath to the chosen destination. Used by BOTH editors. Returns the chosen
// path, or an empty string if the user cancelled.
func (s *ExportService) SaveAs(srcPath, suggestedName string) (string, error) {
	if _, err := os.Stat(srcPath); err != nil {
		return "", fmt.Errorf("saveAs: source not found: %w", err)
	}
	return saveAsDialog(srcPath, suggestedName)
}

// ReadClipboardImage returns a base64 data URL of an image currently on the
// clipboard, for the editor's "paste image as a layer" feature (toolbar-button
// fallback; the in-webview JS `paste` event is the primary path).
func (s *ExportService) ReadClipboardImage() (string, error) {
	return readClipboardImage()
}
