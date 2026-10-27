//go:build windows

package overlay

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// Win32 calls the bundled wails w32 package doesn't wrap. Lazy-loaded like
// internal/hotkey and go-toast.
var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shcore   = windows.NewLazySystemDLL("shcore.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procMonitorFromPoint    = user32.NewProc("MonitorFromPoint")
	procGdiFlush            = gdi32.NewProc("GdiFlush")
	procGetDpiForMonitor    = shcore.NewProc("GetDpiForMonitor")
	// RtlMoveMemory copies a hook struct out of lParam without turning a
	// uintptr into an unsafe.Pointer (vet's unsafeptr check), as in
	// internal/hotkey.
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

const (
	// Every overlay window: layered (per-pixel alpha), click-through, topmost,
	// never activated, no taskbar button.
	overlayExStyle = w32.WS_EX_LAYERED | w32.WS_EX_TRANSPARENT | w32.WS_EX_TOPMOST |
		w32.WS_EX_NOACTIVATE | w32.WS_EX_TOOLWINDOW

	spriteClass  = "ToruClickerOverlay"
	maNoActivate = 3 // MA_NOACTIVATE
)

// msg mirrors the Win32 MSG for the GetMessageW pumps.
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var (
	spriteClassOnce sync.Once
	spriteProcCB    uintptr
)

func registerSpriteClass() {
	spriteClassOnce.Do(func() {
		spriteProcCB = syscall.NewCallback(spriteWndProc)
		wc := w32.WNDCLASSEX{
			WndProc:   spriteProcCB,
			Instance:  w32.GetModuleHandle(""),
			ClassName: windows.StringToUTF16Ptr(spriteClass),
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		w32.RegisterClassEx(&wc)
	})
}

// spriteWndProc keeps every overlay window out of the way of input: hit tests
// fall through to the window below and a click never activates it.
func spriteWndProc(hwnd w32.HWND, m uint32, wParam, lParam uintptr) uintptr {
	switch m {
	case w32.WM_NCHITTEST:
		return uintptr(w32.HTTRANSPARENT & 0xFFFFFFFF)
	case w32.WM_MOUSEACTIVATE:
		return maNoActivate
	}
	return w32.DefWindowProc(hwnd, m, wParam, lParam)
}

// sprite is one layered window and the DIB it is painted from. Only the UI
// thread touches it. The DIB may be larger than the shown size (it only grows
// while the sprite lives, so a live zone drag doesn't reallocate per frame)
// and is freed with the window.
type sprite struct {
	hwnd       w32.HWND
	dc         w32.HDC
	bmp, old   w32.HGDIOBJ
	bits       unsafe.Pointer
	capW, capH int
	w, h       int
	x, y       int32
	alpha      uint8
}

// canvas returns a w x h canvas over the sprite's DIB, growing the DIB when
// needed. The contents are whatever was drawn last; renderers clear first.
func (s *sprite) canvas(w, h int) *Canvas {
	w, h = max(w, 1), max(h, 1)
	if w > s.capW || h > s.capH || s.dc == 0 {
		cw, ch := max(w, s.capW), max(h, s.capH)
		s.freeDIB()
		if !s.allocDIB(cw, ch) {
			return NewCanvas(w, h) // draw into the void rather than crash
		}
	}
	s.w, s.h = w, h
	return &Canvas{W: w, H: h, Stride: s.capW * 4, Pix: unsafe.Slice((*byte)(s.bits), s.capW*s.capH*4)}
}

func (s *sprite) allocDIB(w, h int) bool {
	bi := w32.BITMAPINFO{BmiHeader: w32.BITMAPINFOHEADER{
		BiWidth:       int32(w),
		BiHeight:      -int32(h), // top-down
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: w32.BI_RGB,
	}}
	bi.BmiHeader.BiSize = uint32(unsafe.Sizeof(bi.BmiHeader))
	dc := w32.CreateCompatibleDC(0)
	if dc == 0 {
		return false
	}
	var bits unsafe.Pointer
	bmp := w32.CreateDIBSection(dc, &bi, w32.DIB_RGB_COLORS, &bits, 0, 0)
	if bmp == 0 || bits == nil {
		w32.DeleteDC(dc)
		return false
	}
	s.dc, s.bmp, s.bits, s.capW, s.capH = dc, w32.HGDIOBJ(bmp), bits, w, h
	s.old = w32.SelectObject(dc, s.bmp)
	return true
}

func (s *sprite) freeDIB() {
	if s.dc != 0 {
		w32.SelectObject(s.dc, s.old)
		w32.DeleteDC(s.dc)
	}
	if s.bmp != 0 {
		w32.DeleteObject(s.bmp)
	}
	s.dc, s.bmp, s.old, s.bits, s.capW, s.capH = 0, 0, 0, nil, 0, 0
}

// present shows the last canvas at screen (x, y) with constant opacity alpha,
// creating the window on first use. The first show puts it at the top of the
// topmost band without activating it.
func (s *sprite) present(x, y int32, alpha uint8) {
	if s.dc == 0 {
		return
	}
	first := s.hwnd == 0
	if first {
		registerSpriteClass()
		s.hwnd = w32.CreateWindowEx(overlayExStyle,
			windows.StringToUTF16Ptr(spriteClass), windows.StringToUTF16Ptr("Toru"),
			w32.WS_POPUP, int(x), int(y), s.w, s.h, 0, 0, w32.GetModuleHandle(""), nil)
		if s.hwnd == 0 {
			return
		}
	}
	s.x, s.y, s.alpha = x, y, alpha
	dst := w32.POINT{X: x, Y: y}
	size := w32.SIZE{CX: int32(s.w), CY: int32(s.h)}
	src := w32.POINT{}
	blend := w32.BLENDFUNCTION{BlendOp: w32.AC_SRC_OVER, SourceConstantAlpha: alpha, AlphaFormat: w32.AC_SRC_ALPHA}
	_, _, _ = procUpdateLayeredWindow.Call(uintptr(s.hwnd), 0,
		uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)),
		uintptr(s.dc), uintptr(unsafe.Pointer(&src)), 0,
		uintptr(unsafe.Pointer(&blend)), w32.ULW_ALPHA)
	if first {
		w32.ShowWindow(s.hwnd, w32.SW_SHOWNOACTIVATE)
		s.raise()
	}
}

// raise puts the window at the top of the topmost band, without activating.
func (s *sprite) raise() {
	if s.hwnd != 0 {
		w32.SetWindowPos(s.hwnd, w32.HWND_TOPMOST, 0, 0, 0, 0,
			w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOACTIVATE|w32.SWP_SHOWWINDOW)
	}
}

// setAlpha changes only the constant opacity. A NULL source DC tells Windows
// the pixels are unchanged, so a fade step doesn't re-upload the surface.
func (s *sprite) setAlpha(a uint8) {
	if s.hwnd == 0 || a == s.alpha {
		return
	}
	s.alpha = a
	blend := w32.BLENDFUNCTION{BlendOp: w32.AC_SRC_OVER, SourceConstantAlpha: a, AlphaFormat: w32.AC_SRC_ALPHA}
	_, _, _ = procUpdateLayeredWindow.Call(uintptr(s.hwnd), 0, 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&blend)), w32.ULW_ALPHA)
}

// move repositions the window without touching its pixels or z-order.
func (s *sprite) move(x, y int32) {
	if s.hwnd == 0 || (x == s.x && y == s.y) {
		return
	}
	s.x, s.y = x, y
	w32.SetWindowPos(s.hwnd, 0, int(x), int(y), 0, 0,
		w32.SWP_NOSIZE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE)
}

// visible reports whether the sprite has a window.
func (s *sprite) visible() bool { return s != nil && s.hwnd != 0 }

// destroy closes the window and frees the DIB.
func (s *sprite) destroy() {
	if s.hwnd != 0 {
		w32.DestroyWindow(s.hwnd)
		s.hwnd = 0
	}
	s.freeDIB()
	s.w, s.h = 0, 0
}

// Monitors.

var (
	monEnumOnce sync.Once
	monEnumCB   uintptr
	monMu       sync.Mutex
	monList     []Monitor
)

func monEnumProc(hmon, _ uintptr, _ *w32.RECT, _ uintptr) uintptr {
	mi := w32.MONITORINFO{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if w32.GetMonitorInfo(w32.HMONITOR(hmon), &mi) {
		r := mi.RcMonitor
		monList = append(monList, Monitor{r.Left, r.Top, r.Right, r.Bottom})
	}
	return 1
}

// monitors lists every display's rcMonitor in physical px.
func monitors() []Monitor {
	monEnumOnce.Do(func() { monEnumCB = syscall.NewCallback(monEnumProc) })
	monMu.Lock()
	defer monMu.Unlock()
	monList = nil
	w32.EnumDisplayMonitors(0, nil, monEnumCB, nil)
	return append([]Monitor(nil), monList...)
}

// scaleOf returns a monitor's DPI scale (1 = 96 dpi).
func scaleOf(hmon w32.HMONITOR) float64 {
	var dx, dy uint32
	if r, _, _ := procGetDpiForMonitor.Call(uintptr(hmon), 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
		return float64(dx) / 96
	}
	return 1
}

// hmonitorAt returns the monitor nearest (x, y). MonitorFromPoint takes its
// POINT by value, which on x64 is one register holding both coordinates; the
// wails wrapper passes them as two arguments, so call it directly.
func hmonitorAt(x, y int32) w32.HMONITOR {
	pt := uintptr(uint32(x)) | uintptr(uint32(y))<<32
	r, _, _ := procMonitorFromPoint.Call(pt, w32.MONITOR_DEFAULTTONEAREST)
	return w32.HMONITOR(r)
}

// scaleAt returns the DPI scale of the monitor nearest (x, y).
func scaleAt(x, y int32) float64 { return scaleOf(hmonitorAt(x, y)) }

// scaleFor returns the DPI scale of the monitor at m's centre.
func scaleFor(m Monitor) float64 { return scaleAt((m.L+m.R)/2, (m.T+m.B)/2) }

func keyDown(vk int) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&0x8000 != 0
}

func postThread(tid uint32, m uint32) {
	_, _, _ = procPostThreadMessageW.Call(uintptr(tid), uintptr(m), 0, 0)
}
