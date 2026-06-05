//go:build windows

package export

import "errors"

// errNotImplemented marks the Phase 0 stubs. Replace each with the real Win32
// implementation; the signatures and the multi-format/CF_HDROP plan are fixed.
var errNotImplemented = errors.New("toru/export: not implemented yet (Phase 0 stub)")

// copyImageToClipboard publishes a PNG as a MULTI-FORMAT image in ONE
// OpenClipboard/EmptyClipboard transaction:
//
//	registered 'PNG'  -> lossless, alpha-correct for modern targets
//	CF_DIBV5          -> 32bpp, premultiplied alpha (BI_BITFIELDS)
//	CF_DIB            -> 24bpp, composited over white (legacy/CF_DIB-only targets,
//	                     so transparency does NOT paste black)
//
// TODO(dev1+shared): implement via OpenClipboard/SetClipboardData syscalls.
func copyImageToClipboard(pngPath string) error {
	_ = pngPath
	return errNotImplemented
}

// copyFileToClipboard publishes a single file as CF_HDROP (a DROPFILES struct
// with a double-NUL-terminated path list) so the file pastes into Explorer,
// Discord, Slack, and Teams. This is how "copy video" works.
//
// TODO(dev2+shared): implement via a global-alloc'd DROPFILES + SetClipboardData(CF_HDROP).
func copyFileToClipboard(path string) error {
	_ = path
	return errNotImplemented
}

// saveAsDialog shows a native Save-As dialog and copies srcPath to the chosen
// destination, returning the chosen path ("" if cancelled).
//
// TODO(shared): use the Wails dialog runtime (or IFileSaveDialog) + io.Copy.
func saveAsDialog(srcPath, suggestedName string) (string, error) {
	_, _ = srcPath, suggestedName
	return "", errNotImplemented
}

// readClipboardImage returns a base64 data URL of a clipboard image.
//
// TODO(dev1+shared): read 'PNG'/CF_DIBV5/CF_DIB from the clipboard.
func readClipboardImage() (string, error) {
	return "", errNotImplemented
}
