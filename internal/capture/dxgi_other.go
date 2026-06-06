//go:build !windows

package capture

// dxgiOutputIndexFor is Windows-only (DXGI); see dxgi_windows.go. Off Windows
// there is no ddagrab path, so the mapping is never consulted.
func dxgiOutputIndexFor(DisplayBounds) (int, bool) { return 0, false }
