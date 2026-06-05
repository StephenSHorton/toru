//go:build !windows

package export

import "errors"

// Non-Windows stubs so the module cross-compiles for tooling/CI. Toru is
// Windows-first; these are never shipped.
var errUnsupported = errors.New("toru/export: clipboard is Windows-only")

func copyImageToClipboard(string) error              { return errUnsupported }
func copyFileToClipboard(string) error               { return errUnsupported }
func saveAsDialog(string, string) (string, error)    { return "", errUnsupported }
func readClipboardImage() (string, error)            { return "", errUnsupported }
