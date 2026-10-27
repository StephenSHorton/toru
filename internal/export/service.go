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
type ExportService struct {
	// beforeDialog, if set, runs just before a native Save dialog opens and
	// returns a restore func deferred until the dialog returns. Used by main to
	// suspend "alt-tab cancels overlay" so the Save dialog itself doesn't
	// dismiss the capture mid-save.
	beforeDialog func() (restore func())
}

// NewService returns the shared export service.
func NewService() *ExportService { return &ExportService{} }

// SetBeforeDialog injects a guard run around native file dialogs.
//
//wails:ignore
func (s *ExportService) SetBeforeDialog(fn func() (restore func())) {
	s.beforeDialog = fn
	beforeDialogHook = fn
}

// beforeDialogHook is a package-level copy so dialog_windows.go (same package)
// can run the guard without holding an ExportService receiver.
var beforeDialogHook func() (restore func())

func runBeforeDialog() (restore func()) {
	if beforeDialogHook == nil {
		return nil
	}
	return beforeDialogHook()
}

// CopyImageFile publishes a PNG at path to the system clipboard. Package-level
// so the overlay can auto-copy on capture without going through the Wails
// binding (and without the overlay package depending on the Service type).
// Posts the native copy notice either way.
func CopyImageFile(path string) error {
	err := copyImage(path)
	notifyCopy(whatImage, "", err)
	return err
}

func copyImage(path string) error {
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
//
// Posts a native "Copied to clipboard" notice naming a "Screenshot" or the
// video's file name; CopyToClipboardAs names it something else.
func (s *ExportService) CopyToClipboard(path, mediaType string) error {
	return s.CopyToClipboardAs(path, mediaType, "")
}

// CopyToClipboardAs is CopyToClipboard with what naming the copy on its notice
// (e.g. "Discord-sized copy"). Empty what uses the CopyToClipboard default.
func (s *ExportService) CopyToClipboardAs(path, mediaType, what string) error {
	var err error
	switch mediaType {
	case MediaImage:
		if what == "" {
			what = whatImage
		}
		err = copyImage(path)
		notifyCopy(what, "", err)
	case MediaVideo:
		if what == "" {
			what = whatVideo
		}
		if _, err = os.Stat(path); err != nil {
			err = fmt.Errorf("copy: source not found: %w", err)
		} else {
			err = copyFileToClipboard(path)
		}
		notifyCopy(what, path, err)
	default:
		err = fmt.Errorf("copy: unknown mediaType %q (want %q|%q)", mediaType, MediaImage, MediaVideo)
	}
	return err
}

// CopyText puts text (e.g. a share link) on the clipboard. Done in Go:
// the webview's clipboard API is unreliable in Wails windows. Posts a native
// notice naming it a "Link"; CopyTextAs names it something else.
func (s *ExportService) CopyText(text string) error {
	return s.CopyTextAs(text, "")
}

// CopyTextAs is CopyText with what naming the copy on its notice (e.g.
// "Share link", "Send code"). Empty what means "Link".
func (s *ExportService) CopyTextAs(text, what string) error {
	if text == "" {
		return fmt.Errorf("copy: empty text")
	}
	if what == "" {
		what = whatText
	}
	err := copyTextToClipboard(text)
	notifyCopy(what, "", err)
	return err
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
