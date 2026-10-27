//go:build windows

package safety

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ListProcesses backs the process picker: one row per exe that has a visible,
// titled top-level window, with that window's title and the exe's icon.

var (
	shell32 = windows.NewLazySystemDLL("shell32.dll")
	gdi32   = windows.NewLazySystemDLL("gdi32.dll")
	dwmapi  = windows.NewLazySystemDLL("dwmapi.dll")

	procGetWindowLongW        = user32.NewProc("GetWindowLongW")
	procGetWindowTextLengthW  = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW        = user32.NewProc("GetWindowTextW")
	procDrawIconEx            = user32.NewProc("DrawIconEx")
	procDestroyIcon           = user32.NewProc("DestroyIcon")
	procExtractIconExW        = shell32.NewProc("ExtractIconExW")
	procCreateCompatibleDC    = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection      = gdi32.NewProc("CreateDIBSection")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procDeleteObject          = gdi32.NewProc("DeleteObject")
	procDeleteDC              = gdi32.NewProc("DeleteDC")
	procGdiFlush              = gdi32.NewProc("GdiFlush")
	procDwmGetWindowAttribute = dwmapi.NewProc("DwmGetWindowAttribute")
)

const (
	maxTitleRunes = 35
	iconPx        = 32

	gwlExStyle     = ^uintptr(19) // GWL_EXSTYLE = -20 as an unsigned bit pattern
	wsExToolWindow = 0x00000080
	wsExAppWindow  = 0x00040000
	dwmwaCloaked   = 14
	diNormal       = 0x0003
)

// The EnumWindows callback is made once (NewCallback slots are never freed) and
// collects into enumWinBuf under enumWinMu.
var (
	enumWinMu  sync.Mutex
	enumWinBuf []windows.HWND
	enumWinCB  = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		enumWinBuf = append(enumWinBuf, windows.HWND(hwnd))
		return 1
	})
)

// iconCache maps a lowercase exe path to its PNG data URL; failures are cached
// as "" so a broken exe is not re-read on every refresh.
var (
	iconMu    sync.Mutex
	iconCache = map[string]string{}
)

// ListProcesses returns running apps with a visible top-level window, with
// icons, one row per exe (the front-most window's title), sorted by title.
// Toru's own windows are left out.
func ListProcesses() ([]ProcessInfo, error) {
	enumWinMu.Lock()
	enumWinBuf = nil
	err := windows.EnumWindows(enumWinCB, nil)
	hwnds := enumWinBuf
	enumWinBuf = nil
	enumWinMu.Unlock()
	if err != nil && len(hwnds) == 0 {
		return nil, err
	}

	self := windows.GetCurrentProcessId()
	paths := map[uint32]string{} // pid -> image path ("" = unreadable)
	seen := map[string]bool{}    // exe name
	var out []ProcessInfo
	for _, hwnd := range hwnds { // EnumWindows walks front to back
		if !listableWindow(hwnd) {
			continue
		}
		var pid uint32
		if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 || pid == self {
			continue
		}
		path, ok := paths[pid]
		if !ok {
			path = pidPath(pid)
			paths[pid] = path
		}
		if path == "" {
			continue
		}
		exe := exeName(path)
		if seen[exe] || !strings.HasSuffix(exe, ".exe") {
			continue
		}
		title := windowTitle(hwnd)
		if title == "" {
			continue
		}
		seen[exe] = true
		out = append(out, ProcessInfo{Exe: exe, Title: truncateRunes(title, maxTitleRunes), IconURL: iconFor(path)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

// listableWindow: visible, not cloaked (suspended UWP frames, other virtual
// desktops) and not a tool window unless it forces a taskbar button.
func listableWindow(hwnd windows.HWND) bool {
	if !windows.IsWindowVisible(hwnd) {
		return false
	}
	ex, _, _ := procGetWindowLongW.Call(uintptr(hwnd), gwlExStyle)
	if ex&wsExToolWindow != 0 && ex&wsExAppWindow == 0 {
		return false
	}
	var cloaked uint32
	r, _, _ := procDwmGetWindowAttribute.Call(uintptr(hwnd), dwmwaCloaked, uintptr(unsafe.Pointer(&cloaked)), unsafe.Sizeof(cloaked))
	return r != 0 || cloaked == 0 // a failed query (S_OK is 0) counts as not cloaked
}

func windowTitle(hwnd windows.HWND) string {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	if n == 0 {
		return ""
	}
	buf := make([]uint16, n+1)
	got, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if got == 0 || got > uintptr(len(buf)) {
		return ""
	}
	return strings.TrimSpace(windows.UTF16ToString(buf[:got]))
}

func pidPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort close
	return processPath(h)
}

// truncateRunes cuts s to at most n characters without splitting one.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for k := range s {
		if i == n {
			return strings.TrimSpace(s[:k])
		}
		i++
	}
	return s
}

func iconFor(path string) string {
	key := strings.ToLower(path)
	iconMu.Lock()
	url, ok := iconCache[key]
	iconMu.Unlock()
	if ok {
		return url
	}
	url = renderIcon(path)
	iconMu.Lock()
	iconCache[key] = url
	iconMu.Unlock()
	return url
}

// renderIcon draws the exe's first large icon at 32x32 and returns it as a PNG
// data URL, or "". The icon is drawn twice, over black and over white: where
// both agree the pixel is opaque, and how far they differ gives its alpha. That
// recovers transparency for 32-bit alpha icons and old mask-only icons alike.
func renderIcon(path string) string {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	var icon uintptr
	n, _, _ := procExtractIconExW.Call(uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&icon)), 0, 1)
	if n == 0 || uint32(n) == ^uint32(0) || icon == 0 { // 0xFFFFFFFF: file not found
		return ""
	}
	defer procDestroyIcon.Call(icon) //nolint:errcheck // best-effort release

	dc, _, _ := procCreateCompatibleDC.Call(0)
	if dc == 0 {
		return ""
	}
	defer procDeleteDC.Call(dc) //nolint:errcheck // best-effort release

	bi := struct {
		Size          uint32
		Width, Height int32
		Planes, Bits  uint16
		Compression   uint32
		SizeImage     uint32
		XPels, YPels  int32
		ClrUsed       uint32
		ClrImportant  uint32
	}{Width: iconPx, Height: -iconPx, Planes: 1, Bits: 32} // negative height: top-down rows
	bi.Size = uint32(unsafe.Sizeof(bi))
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(dc, uintptr(unsafe.Pointer(&bi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return ""
	}
	defer procDeleteObject.Call(bmp) //nolint:errcheck // best-effort release
	old, _, _ := procSelectObject.Call(dc, bmp)
	defer procSelectObject.Call(dc, old) //nolint:errcheck // restore before the DC goes

	pix := unsafe.Slice((*byte)(bits), iconPx*iconPx*4)
	draw := func(bg byte) []byte {
		for i := range pix {
			pix[i] = bg
		}
		procDrawIconEx.Call(dc, 0, 0, icon, iconPx, iconPx, 0, 0, diNormal) //nolint:errcheck // a failed draw leaves the background
		procGdiFlush.Call()                                                 //nolint:errcheck // flush before reading the bits
		return append([]byte(nil), pix...)
	}
	black, white := draw(0x00), draw(0xFF)

	img := image.NewNRGBA(image.Rect(0, 0, iconPx, iconPx))
	visible := false
	for i := 0; i < len(black); i += 4 {
		// BGRA. Over black a pixel shows color*alpha; over white it shows that
		// plus (1-alpha)*255, so white-black = (1-alpha)*255 per channel.
		diff := (int(white[i]) - int(black[i]) + int(white[i+1]) - int(black[i+1]) + int(white[i+2]) - int(black[i+2])) / 3
		a := 255 - clampByte(diff)
		if a == 0 {
			continue
		}
		visible = true
		img.Pix[i+0] = byte(clampByte(int(black[i+2]) * 255 / a))
		img.Pix[i+1] = byte(clampByte(int(black[i+1]) * 255 / a))
		img.Pix[i+2] = byte(clampByte(int(black[i+0]) * 255 / a))
		img.Pix[i+3] = byte(a)
	}
	if !visible {
		return ""
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func clampByte(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}
