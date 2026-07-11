package capture

// WindowInfo describes a top-level app window that can be captured (screenshot
// or video). Rect is virtual-desktop PHYSICAL px (same space as contract.Rect).
type WindowInfo struct {
	HWND      uint64 `json:"hwnd"`
	Title     string `json:"title"`
	Rect      Rect   `json:"rect"`
	MonitorID int    `json:"monitorId"` // dominant monitor (kbinani idx); -1 if unknown
}
