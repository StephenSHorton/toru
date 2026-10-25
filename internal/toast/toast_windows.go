//go:build windows

// Package toast draws Toru's small native notification in the top-right corner
// of the active monitor. It is shared by instant replay ("Clip saved") and the
// updater ("Toru vX is ready").
package toast

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// The toast is a tiny native window, not a Wails webview: it must NEVER take
// focus from a game. It is created WS_EX_NOACTIVATE (can't be activated),
// WS_EX_TRANSPARENT + WS_EX_LAYERED (clicks pass through), shown with
// SW_SHOWNOACTIVATE, and lives on its own thread. It appears over
// borderless/windowed games; exclusive-fullscreen games bypass the desktop
// compositor so nothing can draw over them, which is what replay's sound is for.

var (
	shcore        = windows.NewLazySystemDLL("shcore.dll")
	procDpiForMon = shcore.NewProc("GetDpiForMonitor")
)

const (
	toastClass     = "ToruReplayToast"
	toastHoldMs    = 2600 // fully visible
	toastFadeMs    = 25   // fade step
	toastAlpha     = 240
	timerHold      = 1
	timerFade      = 2
	wmToastRefresh = w32.WM_APP + 1

	// Geometry (dip). The body gets one line for the usual short texts and a
	// second line for a longer announcement such as the updater's, measured in
	// toastHeightDip; anything past two lines is ellipsized.
	toastWidthDip     = 300
	toastPadLeftDip   = 16
	toastPadRightDip  = 14
	toastBodyTopDip   = 33
	toastPadBottomDip = 8
	toastBodyLineDip  = 17   // Segoe UI 12.5 dip line height
	toastBodySizeDip  = 12.5 // body font size
)

// toastState is the one toast on screen (guarded by toastMu).
var (
	toastMu       sync.Mutex
	toastHwnd     w32.HWND
	toastTitle    string
	toastBody     string
	toastOK       bool
	toastAlphaNow byte
	toastScale    float64

	toastClassOnce sync.Once
	toastProc      uintptr
)

// Show shows (or updates) the toast in the top-right corner of the monitor the
// focused window is on. ok picks the accent (blue = fine, red = failed). Never
// blocks the caller.
func Show(title, body string, ok bool) {
	toastMu.Lock()
	toastTitle, toastBody, toastOK = title, body, ok
	hwnd := toastHwnd
	toastMu.Unlock()
	if hwnd != 0 {
		w32.PostMessage(hwnd, wmToastRefresh, 0, 0)
		return
	}
	go runToast()
}

// Visible reports whether a toast window is currently on screen (tests).
func Visible() bool {
	toastMu.Lock()
	defer toastMu.Unlock()
	return toastHwnd != 0
}

func runToast() {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	inst := w32.GetModuleHandle("")
	toastClassOnce.Do(func() {
		toastProc = syscall.NewCallback(toastWndProc)
		wc := w32.WNDCLASSEX{
			WndProc:   toastProc,
			Instance:  inst,
			ClassName: windows.StringToUTF16Ptr(toastClass),
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		w32.RegisterClassEx(&wc)
	})

	toastMu.Lock()
	body := toastBody
	toastMu.Unlock()
	x, y, w, h, scale := toastBounds(body)
	hwnd := w32.CreateWindowEx(
		w32.WS_EX_TOPMOST|w32.WS_EX_TOOLWINDOW|w32.WS_EX_NOACTIVATE|w32.WS_EX_TRANSPARENT|w32.WS_EX_LAYERED,
		windows.StringToUTF16Ptr(toastClass), windows.StringToUTF16Ptr("Toru"),
		w32.WS_POPUP, x, y, w, h, 0, 0, inst, nil)
	if hwnd == 0 {
		return
	}
	toastMu.Lock()
	if toastHwnd != 0 { // lost a race with another toast; let that one win
		toastMu.Unlock()
		w32.DestroyWindow(hwnd)
		return
	}
	toastHwnd, toastScale, toastAlphaNow = hwnd, scale, toastAlpha
	toastMu.Unlock()

	w32.SetLayeredWindowAttributes(hwnd, 0, toastAlpha, w32.LWA_ALPHA)
	w32.SetWindowPos(hwnd, w32.HWND_TOPMOST, 0, 0, 0, 0,
		w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOACTIVATE|w32.SWP_SHOWWINDOW)
	w32.ShowWindow(hwnd, w32.SW_SHOWNOACTIVATE)
	w32.SetTimer(hwnd, timerHold, toastHoldMs, 0)

	var m w32.MSG
	for w32.GetMessage(&m, 0, 0, 0) > 0 {
		w32.TranslateMessage(&m)
		w32.DispatchMessage(&m)
	}
}

func toastWndProc(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case w32.WM_NCHITTEST:
		return uintptr(w32.HTTRANSPARENT & 0xFFFFFFFF)
	case wmToastRefresh: // new text: show it fully again and restart the hold
		w32.KillTimer(hwnd, timerFade)
		toastMu.Lock()
		toastAlphaNow = toastAlpha
		body, scale := toastBody, toastScale
		toastMu.Unlock()
		// The new body may need a different number of lines; the window is
		// anchored top-right so only its height changes.
		px := func(dip float64) int { return int(dip*scale + 0.5) }
		w32.SetWindowPos(hwnd, 0, 0, 0, px(toastWidthDip), px(toastHeightDip(body, scale)),
			w32.SWP_NOMOVE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE)
		w32.SetLayeredWindowAttributes(hwnd, 0, toastAlpha, w32.LWA_ALPHA)
		w32.InvalidateRect(hwnd, nil, true)
		w32.SetTimer(hwnd, timerHold, toastHoldMs, 0)
		return 0
	case w32.WM_TIMER:
		switch wParam {
		case timerHold:
			w32.KillTimer(hwnd, timerHold)
			w32.SetTimer(hwnd, timerFade, toastFadeMs, 0)
		case timerFade:
			toastMu.Lock()
			a := toastAlphaNow
			if a > 20 {
				a -= 20
			} else {
				a = 0
			}
			toastAlphaNow = a
			toastMu.Unlock()
			if a == 0 {
				w32.DestroyWindow(hwnd)
			} else {
				w32.SetLayeredWindowAttributes(hwnd, 0, a, w32.LWA_ALPHA)
			}
		}
		return 0
	case w32.WM_PAINT:
		paintToast(hwnd)
		return 0
	case w32.WM_DESTROY:
		toastMu.Lock()
		if toastHwnd == hwnd {
			toastHwnd = 0
		}
		toastMu.Unlock()
		w32.PostQuitMessage(0)
		return 0
	}
	return w32.DefWindowProc(hwnd, msg, wParam, lParam)
}

// toastBounds returns the toast rect (physical px) in the top-right of the
// work area of the monitor the focused window is on, and the DPI scale. The
// height depends on how many lines body needs (toastHeightDip).
func toastBounds(body string) (x, y, w, h int, scale float64) {
	mon := w32.MonitorFromWindow(w32.GetForegroundWindow(), w32.MONITOR_DEFAULTTONEAREST)
	mi := w32.MONITORINFO{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	w32.GetMonitorInfo(mon, &mi)
	scale = 1
	var dx, dy uint32
	if r, _, _ := procDpiForMon.Call(uintptr(mon), 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
		scale = float64(dx) / 96
	}
	px := func(dip float64) int { return int(dip*scale + 0.5) }
	w, h = px(toastWidthDip), px(toastHeightDip(body, scale))
	// Top-right also keeps it clear of most games' HUD corners (minimap is
	// usually top-left/bottom-left; health/ammo bottom).
	x = int(mi.RcWork.Right) - w - px(20)
	y = int(mi.RcWork.Top) + px(20)
	return x, y, w, h, scale
}

// newFont creates a Segoe UI font of sizeDip at the given DPI scale. The caller
// deletes it.
func newFont(sizeDip float64, weight int32, scale float64) w32.HFONT {
	lf := w32.LOGFONT{Height: -int32(sizeDip*scale + 0.5), Weight: weight, Quality: 5 /* CLEARTYPE */}
	copy(lf.FaceName[:], windows.StringToUTF16("Segoe UI"))
	return w32.CreateFontIndirect(&lf)
}

// toastHeightDip returns the window height (dip) that fits body: one body
// line, or two when the text word-wraps past the first (measured with the real
// font via DT_CALCRECT, so it tracks DPI and the font's own metrics). Longer
// bodies still get two lines and an ellipsis: a toast is a glance, not a
// paragraph.
func toastHeightDip(body string, scale float64) float64 {
	// 62 dip is the original one-line toast (replay's "Clip saved" etc.) and is
	// unchanged; a second line adds exactly one body line height.
	const oneLine = 62
	const twoLines = oneLine + toastBodyLineDip
	if body == "" {
		return oneLine
	}
	hdc := w32.GetDC(0)
	if hdc == 0 {
		return oneLine
	}
	defer w32.ReleaseDC(0, hdc)
	font := newFont(toastBodySizeDip, 400, scale)
	old := w32.SelectObject(hdc, w32.HGDIOBJ(font))
	defer func() {
		w32.SelectObject(hdc, old)
		w32.DeleteObject(w32.HGDIOBJ(font))
	}()
	width := int32((toastWidthDip-toastPadLeftDip-toastPadRightDip)*scale + 0.5)
	one := w32.RECT{Right: width}
	x := windows.StringToUTF16("X")
	w32.DrawText(hdc, x[:len(x)-1], -1, &one, w32.DT_CALCRECT|w32.DT_SINGLELINE)
	wrapped := w32.RECT{Right: width}
	u := windows.StringToUTF16(body)
	w32.DrawText(hdc, u[:len(u)-1], -1, &wrapped, w32.DT_CALCRECT|w32.DT_WORDBREAK)
	if one.Bottom > 0 && wrapped.Bottom > one.Bottom+one.Bottom/2 {
		return twoLines
	}
	return oneLine
}

func paintToast(hwnd w32.HWND) {
	var ps w32.PAINTSTRUCT
	hdc := w32.BeginPaint(hwnd, &ps)
	defer w32.EndPaint(hwnd, &ps)

	toastMu.Lock()
	title, body, ok, scale := toastTitle, toastBody, toastOK, toastScale
	toastMu.Unlock()
	px := func(dip float64) int32 { return int32(dip*scale + 0.5) }

	rc := w32.GetClientRect(hwnd)
	if rc == nil {
		return
	}
	bg := w32.CreateSolidBrush(w32.COLORREF(w32.RGB(0x16, 0x17, 0x1c)))
	w32.FillRect(hdc, rc, bg)
	w32.DeleteObject(w32.HGDIOBJ(bg))

	accent := w32.RGB(0x4a, 0xa8, 0xff) // Toru blue
	if !ok {
		accent = w32.RGB(0xf0, 0x5a, 0x5a)
	}
	bar := *rc
	bar.Right = bar.Left + px(4)
	ab := w32.CreateSolidBrush(w32.COLORREF(accent))
	w32.FillRect(hdc, &bar, ab)
	w32.DeleteObject(w32.HGDIOBJ(ab))

	w32.SetBkMode(hdc, w32.TRANSPARENT)
	text := func(s string, sizeDip float64, weight int32, color uint32, r w32.RECT, format uint32) {
		font := newFont(sizeDip, weight, scale)
		old := w32.SelectObject(hdc, w32.HGDIOBJ(font))
		w32.SetTextColor(hdc, w32.COLORREF(color))
		u := windows.StringToUTF16(s)
		w32.DrawText(hdc, u[:len(u)-1], -1, &r, format)
		w32.SelectObject(hdc, old)
		w32.DeleteObject(w32.HGDIOBJ(font))
	}
	left, right := rc.Left+px(toastPadLeftDip), rc.Right-px(toastPadRightDip)
	text(title, 15, 600, w32.RGB(0xf2, 0xf3, 0xf5),
		w32.RECT{Left: left, Top: rc.Top + px(9), Right: right, Bottom: rc.Top + px(32)},
		w32.DT_SINGLELINE|w32.DT_END_ELLIPSIS|w32.DT_VCENTER)
	// The body wraps to the lines the window was sized for (toastHeightDip);
	// DT_EDITCONTROL hides a partial third line and DT_END_ELLIPSIS marks the cut.
	text(body, toastBodySizeDip, 400, w32.RGB(0x9a, 0x9d, 0xa6),
		w32.RECT{Left: left, Top: rc.Top + px(toastBodyTopDip), Right: right, Bottom: rc.Bottom - px(toastPadBottomDip)},
		w32.DT_WORDBREAK|w32.DT_END_ELLIPSIS|w32.DT_EDITCONTROL)
}
