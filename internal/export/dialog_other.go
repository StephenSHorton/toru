//go:build !windows

package export

// saveAsDialog is Windows-only (native IFileSaveDialog via the Wails runtime).
// This stub exists only so the module cross-compiles for tooling/CI on
// non-Windows hosts.
func saveAsDialog(srcPath, suggestedName string) (string, error) {
	_, _ = srcPath, suggestedName
	return "", errUnsupported
}
