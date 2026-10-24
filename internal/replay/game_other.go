//go:build !windows

package replay

// foreground is Windows-only; elsewhere clips always go to Desktop.
func foreground() (foregroundApp, bool) { return foregroundApp{}, false }
