//go:build !windows

package export

import "errors"

// Non-Windows stubs so the module cross-compiles for tooling/CI. Toru is
// Windows-first; these are never shipped.
var errUnsupported = errors.New("toru/export: clipboard is Windows-only")

// saveAsDialog lives in dialog_other.go (same build tag) to mirror the
// Windows split (clipboard_windows.go + dialog_windows.go).
func copyImageToClipboard(string) error   { return errUnsupported }
func copyFileToClipboard(string) error    { return errUnsupported }
func readClipboardImage() (string, error) { return "", errUnsupported }
