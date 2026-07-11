//go:build windows

package capture

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32Enum               = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows          = user32Enum.NewProc("EnumWindows")
	procIsWindowVisible      = user32Enum.NewProc("IsWindowVisible")
	procIsIconic             = user32Enum.NewProc("IsIconic")
	procGetWindowTextW       = user32Enum.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32Enum.NewProc("GetWindowTextLengthW")
	procGetWindowRect        = user32Enum.NewProc("GetWindowRect")
	procGetWindowLongW       = user32Enum.NewProc("GetWindowLongW")
	procGetWindow            = user32Enum.NewProc("GetWindow")
	procGetClassNameW        = user32Enum.NewProc("GetClassNameW")
)

const (
	// GWL_EXSTYLE = -20; pass as the two's-complement bit pattern so Call's
	// uintptr arg doesn't reject a negative constant.
	gwlExStyle       = ^uintptr(19) // -20
	wsExToolWindow   = 0x00000080
	wsExAppWindow    = 0x00040000
	gwOwner          = 4
	maxWindowsListed = 80
)

// ListTopLevelWindows returns visible, non-minimized top-level windows with a
// title, suitable for "capture this window". Toru's own overlay/settings chrome
// is filtered out by title/class heuristics. Order is Z-order (front to back).
func ListTopLevelWindows() []WindowInfo {
	screens := EnumDisplays()
	var out []WindowInfo

	cb := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		if len(out) >= maxWindowsListed {
			return 0 // stop
		}
		if !isVisible(hwnd) || isIconic(hwnd) {
			return 1
		}
		// Skip owned popups / tool windows that aren't real app frames.
		if isOwned(hwnd) && !hasAppWindowEx(hwnd) {
			return 1
		}
		if isToolWindow(hwnd) && !hasAppWindowEx(hwnd) {
			return 1
		}
		title := windowTitle(hwnd)
		if title == "" {
			return 1
		}
		// Filter Toru chrome so the user never captures the overlay/settings.
		if isToruChrome(title, windowClass(hwnd)) {
			return 1
		}
		r, ok := windowRect(hwnd)
		if !ok || r.W < 32 || r.H < 32 {
			return 1
		}
		// Skip off-screen / zero-size shells.
		if r.W > 100000 || r.H > 100000 {
			return 1
		}
		out = append(out, WindowInfo{
			HWND:      uint64(hwnd),
			Title:     title,
			Rect:      r,
			MonitorID: dominantMonitorIDFromDisplays(r, screens),
		})
		return 1
	})

	_, _, _ = procEnumWindows.Call(cb, 0)
	return out
}

func isVisible(hwnd uintptr) bool {
	r, _, _ := procIsWindowVisible.Call(hwnd)
	return r != 0
}

func isIconic(hwnd uintptr) bool {
	r, _, _ := procIsIconic.Call(hwnd)
	return r != 0
}

func isOwned(hwnd uintptr) bool {
	owner, _, _ := procGetWindow.Call(hwnd, gwOwner)
	return owner != 0
}

func isToolWindow(hwnd uintptr) bool {
	style, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle)
	return style&wsExToolWindow != 0
}

func hasAppWindowEx(hwnd uintptr) bool {
	style, _, _ := procGetWindowLongW.Call(hwnd, gwlExStyle)
	return style&wsExAppWindow != 0
}

func windowTitle(hwnd uintptr) string {
	n, _, _ := procGetWindowTextLengthW.Call(hwnd)
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	_, _, _ = procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
	return windows.UTF16ToString(buf)
}

func windowClass(hwnd uintptr) string {
	buf := make([]uint16, 256)
	_, _, _ = procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	return windows.UTF16ToString(buf)
}

func windowRect(hwnd uintptr) (Rect, bool) {
	var r winRECT
	ok, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&r)))
	if ok == 0 {
		return Rect{}, false
	}
	w := int(r.Right - r.Left)
	h := int(r.Bottom - r.Top)
	if w <= 0 || h <= 0 {
		return Rect{}, false
	}
	return Rect{X: int(r.Left), Y: int(r.Top), W: w, H: h}, true
}

func isToruChrome(title, class string) bool {
	_ = class
	// Overlay / settings / editor / trim / recframe titles.
	if len(title) >= 4 && (title == "Toru" || title[:4] == "Toru") {
		return true
	}
	return false
}

// dominantMonitorIDFromDisplays returns the EnumDisplays index with the largest
// overlap with r, or -1 if none.
func dominantMonitorIDFromDisplays(r Rect, screens []DisplayBounds) int {
	bestID := -1
	bestArea := 0
	for _, s := range screens {
		a := overlapArea(r, Rect{X: s.X, Y: s.Y, W: s.W, H: s.H})
		if a > bestArea {
			bestArea = a
			bestID = s.Index
		}
	}
	return bestID
}

func overlapArea(a, b Rect) int {
	x1 := maxInt(a.X, b.X)
	y1 := maxInt(a.Y, b.Y)
	x2 := minInt(a.X+a.W, b.X+b.W)
	y2 := minInt(a.Y+a.H, b.Y+b.H)
	if x2 <= x1 || y2 <= y1 {
		return 0
	}
	return (x2 - x1) * (y2 - y1)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
