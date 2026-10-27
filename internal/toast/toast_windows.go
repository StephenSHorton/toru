//go:build windows

package toast

import (
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/inputtag"
	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// Each card is a tiny native window, not a Wails webview: it must NEVER take
// focus from a game. Cards are WS_EX_NOACTIVATE, answer WM_MOUSEACTIVATE with
// MA_NOACTIVATE (so clicking one doesn't activate it either), are shown with
// SW_SHOWNOACTIVATE and put back on top of the topmost band (HWND_TOPMOST)
// whenever a note is posted. They appear over borderless/windowed games;
// exclusive-fullscreen games bypass the desktop compositor so nothing can draw
// over them, which is what the sound is for.
//
// All the windows live on one locked OS thread, started by the first Post. Other
// goroutines only change the model under mu and PostMessage the thread's
// message-only window. The thread never runs a note's callbacks itself: each
// gets its own goroutine, after the card is out of the model and mu is free,
// so a callback that posts, closes or blocks can't wedge the windows (Suzuri's
// notice stack deadlocked exactly that way).
//
// Cards are excluded from screen capture (WDA_EXCLUDEFROMCAPTURE), so a
// Screenshot taken while "Copied to clipboard" is still up, or a Game session
// recorded under a card, doesn't picture Toru's own notification. Clicks Toru
// injects itself (the auto clicker) never press a card: they are ignored, and
// while a run is going the cards are click-through (HoldClickThrough).

var (
	shcore        = windows.NewLazySystemDLL("shcore.dll")
	procDpiForMon = shcore.NewProc("GetDpiForMonitor")
	gdi32         = windows.NewLazySystemDLL("gdi32.dll")
	procCompatBmp = gdi32.NewProc("CreateCompatibleBitmap")
	procBitBlt    = gdi32.NewProc("BitBlt")
	winmm         = windows.NewLazySystemDLL("winmm.dll")
	procPlaySound = winmm.NewProc("PlaySoundW")
	user32        = windows.NewLazySystemDLL("user32.dll")
	procAffinity  = user32.NewProc("SetWindowDisplayAffinity")
	procExtraInfo = user32.NewProc("GetMessageExtraInfo")
)

const (
	hostClass    = "ToruToastHost"
	cardClass    = "ToruToast"
	wmSync       = w32.WM_APP + 1
	wmRehome     = w32.WM_APP + 2 // a display or work-area change: re-place the stack
	timerTick    = 1
	tickMs       = 30 // expiry and fades; runs only while cards are up
	maNoActivate = 3
	// wdaExcludeFromCapture hides a window from BitBlt, DXGI duplication and
	// Windows.Graphics.Capture while it stays on screen (Windows 10 2004+;
	// older builds refuse it and the card is captured as before).
	wdaExcludeFromCapture = 0x11

	iconFace = "Segoe MDL2 Assets" // on every Windows 10/11
	glyphX   = "\uE8BB"            // ChromeClose
)

// Toru's palette (frontend/src/index.css, oklch converted to sRGB).
var (
	colCard       = w32.RGB(0x11, 0x12, 0x13) // --card
	colBorder     = w32.RGB(0x2c, 0x2e, 0x31) // --border
	colTitle      = w32.RGB(0xfa, 0xfa, 0xfa) // --foreground
	colBody       = w32.RGB(0x9e, 0xa1, 0xa7) // --muted-foreground
	colFaint      = w32.RGB(0x5c, 0x5f, 0x66) // the x on a card the cursor isn't over
	colHot        = w32.RGB(0x26, 0x29, 0x2e) // --accent (hover fill)
	colButton     = w32.RGB(0x20, 0x22, 0x25) // --secondary
	colPrimary    = w32.RGB(0x4c, 0xab, 0xfd) // --primary
	colPrimaryHot = w32.RGB(0x6f, 0xbc, 0xff)
	colOnPrimary  = w32.RGB(0x06, 0x0a, 0x13) // --primary-foreground
	colWarn       = w32.RGB(0xf5, 0xae, 0x39)
	colError      = w32.RGB(0xea, 0x3c, 0x3f) // --destructive
)

func accentFor(k Kind) uint32 {
	switch k {
	case Warn:
		return colWarn
	case Error:
		return colError
	default:
		return colPrimary
	}
}

func iconFor(k Kind) string {
	switch k {
	case Success:
		return "\uE73E" // CheckMark
	case Warn:
		return "\uE7BA" // Warning
	case Error:
		return "\uE783" // Error
	default:
		return "\uE946" // Info
	}
}

// Shared state, guarded by mu.
var (
	mu      sync.Mutex
	model   stack
	host    w32.HWND // the thread's message-only window; 0 until it starts
	work    rect     // work area (px) of the monitor the stack is on
	scale   = 1.0
	raise   bool       // re-assert topmost on the next sync
	through int        // HoldClickThrough holds; cards are click-through while > 0
	hwnds   []w32.HWND // card windows, for tests
	started sync.Once
	shown   atomic.Int32 // card windows on screen
)

// Toast-thread state: only the window thread touches these.
var (
	wins     = map[uint64]*cardWin{}
	byHwnd   = map[w32.HWND]*cardWin{}
	hoverKey uint64 // card under the cursor, 0 = none
	ticking  bool
)

// cardWin is a card's window and its copy of the card's content.
type cardWin struct {
	hwnd         w32.HWND
	key, rev     uint64
	scale        float64
	title, body  string
	kind         Kind
	labels       []string
	clickable    bool // has OnClick: the body gets a hand cursor
	lines        int  // body lines
	w, h         int
	close        rect
	buttons      []rect
	x, y, pw, ph int // last placement
	placed       bool
	alpha        byte
	hot, pressed part
	tracking     bool
	through      bool // WS_EX_TRANSPARENT is set
}

// Post shows n in the notification stack (or replaces the card with its ID)
// and plays its sound. A no-op when notifications are off. Never blocks.
func Post(n Note) {
	o := CurrentOptions()
	n, ok := clean(n)
	if !o.Enabled || !ok {
		return
	}
	mu.Lock()
	if len(model.cards) == 0 { // a new stack goes where the user is looking
		work, scale = activeWorkArea()
	} else { // a monitor unplugged or resized under a sticky card moves the stack
		work, scale = rehome(work)
	}
	model.post(n, time.Now())
	raise = true
	h := host
	mu.Unlock()
	if !n.Silent && o.Sound {
		play(soundFor(n.Kind, len(n.Actions) > 0), o.Volume)
	}
	wake(h)
}

// Close removes the card with id, if it is up, without running its callbacks.
func Close(id string) {
	mu.Lock()
	closed := model.closeID(id)
	h := host
	mu.Unlock()
	if closed {
		wake(h)
	}
}

// Show posts a plain card: ok picks Success (blue check) or Error (red).
func Show(title, body string, ok bool) {
	k := Success
	if !ok {
		k = Error
	}
	Post(Note{Title: title, Body: body, Kind: k})
}

// HoldClickThrough makes the cards click-through until the returned release
// is called, so clicks land on the window under them. The auto clicker holds
// it for a run: its injected clicks then reach the app it is clicking instead
// of a card. Holds nest (a stopped run can outlive the next one's start).
func HoldClickThrough() (release func()) {
	mu.Lock()
	through++
	h := host
	mu.Unlock()
	if h != 0 {
		w32.PostMessage(h, wmSync, 0, 0)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			mu.Lock()
			through--
			h := host
			mu.Unlock()
			if h != 0 {
				w32.PostMessage(h, wmSync, 0, 0)
			}
		})
	}
}

// Visible reports whether any card is on screen (tests).
func Visible() bool { return shown.Load() > 0 }

func optionsChanged(o Options) {
	mu.Lock()
	if !o.Enabled {
		model.cards = nil
	}
	h := host
	mu.Unlock()
	if h != 0 {
		w32.PostMessage(h, wmSync, 0, 0) // a corner change moves the stack
	}
}

// wake asks the window thread to sync, starting it the first time. A post that
// races the start is picked up by the thread's first sync, which runs after it
// publishes host under mu.
func wake(h w32.HWND) {
	if h != 0 {
		w32.PostMessage(h, wmSync, 0, 0)
		return
	}
	started.Do(func() { go run() })
}

func playWAV(b []byte) {
	const sndAsync, sndNoDefault, sndMemory = 0x0001, 0x0002, 0x0004
	_, _, _ = procPlaySound.Call(uintptr(unsafe.Pointer(&b[0])), 0, sndAsync|sndNoDefault|sndMemory)
}

// run is the window thread. It lives as long as Toru.
func run() {
	runtime.LockOSThread()
	inst := w32.GetModuleHandle("")
	for class, proc := range map[string]uintptr{
		hostClass: syscall.NewCallback(hostProc),
		cardClass: syscall.NewCallback(cardProc),
	} {
		wc := w32.WNDCLASSEX{WndProc: proc, Instance: inst, ClassName: windows.StringToUTF16Ptr(class)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		w32.RegisterClassEx(&wc)
	}
	h := w32.CreateWindowEx(0, windows.StringToUTF16Ptr(hostClass), windows.StringToUTF16Ptr("Toru"),
		0, 0, 0, 0, 0, w32.HWND_MESSAGE, 0, inst, nil)
	if h == 0 {
		log.Printf("toru: toast: can't create the notification window")
		return
	}
	mu.Lock()
	host = h
	mu.Unlock()
	syncCards()

	var m w32.MSG
	for w32.GetMessage(&m, 0, 0, 0) > 0 {
		w32.TranslateMessage(&m)
		w32.DispatchMessage(&m)
	}
}

func hostProc(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmSync:
		syncCards()
		return 0
	case wmRehome:
		mu.Lock()
		if len(model.cards) > 0 {
			work, scale = rehome(work)
			raise = true
		}
		mu.Unlock()
		syncCards()
		return 0
	case w32.WM_TIMER:
		mu.Lock()
		model.tick(time.Now(), hoverKey != 0)
		mu.Unlock()
		syncCards()
		return 0
	}
	return w32.DefWindowProc(hwnd, msg, wParam, lParam)
}

// syncCards makes the windows match the model: creates, re-measures, places,
// fades and destroys them, and runs the tick timer while any card is fading
// or counting down (a stack of only sticky cards needs no timer).
func syncCards() {
	type view struct {
		key, rev  uint64
		title     string
		body      string
		kind      Kind
		labels    []string
		clickable bool
		alpha     byte
	}
	now := time.Now()
	corner := CurrentOptions().Corner
	timed := false
	mu.Lock()
	views := make([]view, len(model.cards))
	for i, c := range model.cards {
		v := view{key: c.key, rev: c.rev, title: c.Title, body: c.Body, kind: c.Kind,
			clickable: c.OnClick != nil, alpha: c.alpha(now)}
		for _, a := range c.Actions {
			v.labels = append(v.labels, a.Label)
		}
		views[i] = v
		if !c.until.IsZero() || !c.closing.IsZero() || v.alpha < fullAlpha {
			timed = true
		}
	}
	wa, sc, up, pass := work, scale, raise, through > 0
	raise = false
	mu.Unlock()

	live := make(map[uint64]bool, len(views))
	for _, v := range views {
		live[v.key] = true
	}
	for k, cw := range wins {
		if !live[k] {
			destroyCard(cw)
		}
	}

	var cards []*cardWin
	var heights []int
	var alphas []byte
	for _, v := range views {
		cw := wins[v.key]
		fresh := cw == nil
		if fresh {
			cw = &cardWin{key: v.key, hot: partNone, pressed: partNone}
		}
		if fresh || cw.rev != v.rev || cw.scale != sc {
			cw.rev, cw.scale = v.rev, sc
			cw.title, cw.body, cw.kind, cw.labels, cw.clickable = v.title, v.body, v.kind, v.labels, v.clickable
			measure(cw)
			if !fresh {
				w32.InvalidateRect(cw.hwnd, nil, false)
			}
		}
		if fresh && !createCard(cw) {
			continue
		}
		if cw.through != pass {
			setClickThrough(cw, pass)
		}
		cards, heights, alphas = append(cards, cw), append(heights, cw.h), append(alphas, v.alpha)
	}

	w := px(cardWidthDip, sc)
	origins := stackOrigins(wa, corner, w, heights, px(cardGapDip, sc), px(cardMarginDip, sc))
	for i, cw := range cards {
		p := origins[i]
		moved := !cw.placed || p.X != cw.x || p.Y != cw.y || cw.w != cw.pw || cw.h != cw.ph
		if moved || up {
			after, flags := w32.HWND_TOPMOST, uint(w32.SWP_NOACTIVATE)
			if cw.placed && !up {
				after, flags = 0, flags|w32.SWP_NOZORDER
			}
			w32.SetWindowPos(cw.hwnd, after, p.X, p.Y, cw.w, cw.h, flags)
			cw.x, cw.y, cw.pw, cw.ph = p.X, p.Y, cw.w, cw.h
		}
		if alphas[i] != cw.alpha {
			cw.alpha = alphas[i]
			w32.SetLayeredWindowAttributes(cw.hwnd, 0, cw.alpha, w32.LWA_ALPHA)
		}
		if !cw.placed {
			cw.placed = true
			w32.ShowWindow(cw.hwnd, w32.SW_SHOWNOACTIVATE)
		}
	}

	list := make([]w32.HWND, len(cards))
	for i, cw := range cards {
		list[i] = cw.hwnd
	}
	mu.Lock()
	hwnds = list
	h := host
	mu.Unlock()
	switch {
	case timed && !ticking:
		ticking = w32.SetTimer(h, timerTick, tickMs, 0) != 0
	case !timed && ticking:
		w32.KillTimer(h, timerTick)
		ticking = false
	}
}

func createCard(cw *cardWin) bool {
	hwnd := w32.CreateWindowEx(
		w32.WS_EX_TOPMOST|w32.WS_EX_TOOLWINDOW|w32.WS_EX_NOACTIVATE|w32.WS_EX_LAYERED,
		windows.StringToUTF16Ptr(cardClass), windows.StringToUTF16Ptr("Toru"),
		w32.WS_POPUP, 0, 0, cw.w, cw.h, 0, 0, w32.GetModuleHandle(""), nil)
	if hwnd == 0 {
		return false
	}
	w32.SetLayeredWindowAttributes(hwnd, 0, 0, w32.LWA_ALPHA) // fades in from nothing
	_, _, _ = procAffinity.Call(uintptr(hwnd), wdaExcludeFromCapture)
	cw.hwnd = hwnd
	wins[cw.key], byHwnd[hwnd] = cw, cw
	shown.Add(1)
	return true
}

// setClickThrough adds or removes WS_EX_TRANSPARENT on cw. A card going
// click-through gets no WM_MOUSELEAVE, so its hover is dropped here.
func setClickThrough(cw *cardWin, on bool) {
	ex := uint32(w32.GetWindowLong(cw.hwnd, w32.GWL_EXSTYLE))
	if on {
		ex |= w32.WS_EX_TRANSPARENT
		if hoverKey == cw.key {
			hoverKey = 0
		}
		cw.tracking, cw.pressed = false, partNone
		setHot(cw, partNone)
	} else {
		ex &^= w32.WS_EX_TRANSPARENT
	}
	w32.SetWindowLong(cw.hwnd, w32.GWL_EXSTYLE, ex)
	w32.SetWindowPos(cw.hwnd, 0, 0, 0, 0, 0,
		w32.SWP_FRAMECHANGED|w32.SWP_NOMOVE|w32.SWP_NOSIZE|w32.SWP_NOZORDER|w32.SWP_NOACTIVATE)
	cw.through = on
}

func destroyCard(cw *cardWin) {
	delete(wins, cw.key)
	delete(byHwnd, cw.hwnd)
	if hoverKey == cw.key { // no WM_MOUSELEAVE comes for a destroyed window
		hoverKey = 0
	}
	w32.DestroyWindow(cw.hwnd)
	shown.Add(-1)
}

// click handles a left click on p (or a right click, as partClose): the card
// leaves the model first, then its callback gets its own goroutine. The
// windows catch up on the next sync, posted rather than run here so the
// clicked window isn't destroyed inside its own message.
func click(key uint64, p part) {
	mu.Lock()
	c := model.take(key)
	h := host
	mu.Unlock()
	if c == nil {
		return
	}
	switch {
	case p >= 0 && int(p) < len(c.Actions):
		goRun(c.Actions[p].Run)
	case p == partBody && c.OnClick != nil:
		goRun(c.OnClick)
	default:
		goRun(c.OnDismiss)
	}
	w32.PostMessage(h, wmSync, 0, 0)
}

func goRun(fn func()) {
	if fn == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("toru: toast: notification callback panicked: %v", r)
			}
		}()
		fn()
	}()
}

func cardProc(hwnd w32.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	// Top-level cards get the display broadcasts the message-only host
	// doesn't. Each card forwards them; re-placing is idempotent.
	switch {
	case msg == w32.WM_DISPLAYCHANGE, msg == w32.WM_DPICHANGED,
		msg == w32.WM_SETTINGCHANGE && wParam == w32.SPI_SETWORKAREA:
		mu.Lock()
		h := host
		mu.Unlock()
		if h != 0 {
			w32.PostMessage(h, wmRehome, 0, 0)
		}
	}
	cw := byHwnd[hwnd]
	if cw == nil {
		if msg == w32.WM_MOUSEACTIVATE {
			return maNoActivate
		}
		return w32.DefWindowProc(hwnd, msg, wParam, lParam)
	}
	x, y := int(int16(lParam&0xffff)), int(int16(lParam>>16&0xffff))
	switch msg {
	case w32.WM_MOUSEMOVE, w32.WM_LBUTTONDOWN, w32.WM_LBUTTONUP, w32.WM_RBUTTONUP:
		// Toru's own synthetic input (the auto clicker) neither hovers nor
		// presses a card: a run at a fixed point must not hit Install.
		if extra, _, _ := procExtraInfo.Call(); extra == inputtag.Toru {
			return 0
		}
	}
	switch msg {
	case w32.WM_MOUSEACTIVATE:
		return maNoActivate
	case w32.WM_ERASEBKGND:
		return 1
	case w32.WM_PAINT:
		paintCard(cw)
		return 0
	case w32.WM_SETCURSOR:
		cursor := uint16(w32.IDC_ARROW)
		if cw.hot >= 0 || cw.hot == partClose || (cw.hot == partBody && cw.clickable) {
			cursor = w32.IDC_HAND
		}
		w32.SetCursor(w32.LoadCursorWithResourceID(0, cursor))
		return 1
	case w32.WM_MOUSEMOVE:
		if !cw.tracking {
			tme := w32.TRACKMOUSEEVENT{DwFlags: w32.TME_LEAVE, HwndTrack: hwnd}
			tme.CbSize = uint32(unsafe.Sizeof(tme))
			cw.tracking = w32.TrackMouseEvent(&tme)
			w32.InvalidateRect(hwnd, nil, false) // the x brightens on a hovered card
		}
		hoverKey = cw.key
		setHot(cw, hitPart(x, y, cw.close, cw.buttons))
		return 0
	case w32.WM_MOUSELEAVE:
		cw.tracking = false
		if hoverKey == cw.key {
			hoverKey = 0
		}
		cw.pressed = partNone
		setHot(cw, partNone)
		w32.InvalidateRect(hwnd, nil, false)
		return 0
	case w32.WM_LBUTTONDOWN:
		cw.pressed = hitPart(x, y, cw.close, cw.buttons)
		return 0
	case w32.WM_LBUTTONUP:
		// Pressed and released on the same part, like a button.
		p := hitPart(x, y, cw.close, cw.buttons)
		if p == cw.pressed {
			click(cw.key, p)
		}
		cw.pressed = partNone
		return 0
	case w32.WM_RBUTTONUP:
		click(cw.key, partClose)
		return 0
	}
	return w32.DefWindowProc(hwnd, msg, wParam, lParam)
}

func setHot(cw *cardWin, p part) {
	if cw.hot != p {
		cw.hot = p
		w32.InvalidateRect(cw.hwnd, nil, false)
	}
}

// activeWorkArea returns the work area (physical px) of the monitor the
// focused window is on, and its DPI scale.
func activeWorkArea() (rect, float64) {
	return workAreaOf(w32.MonitorFromWindow(w32.GetForegroundWindow(), w32.MONITOR_DEFAULTTONEAREST))
}

// rehome returns the current work area and scale of the monitor old is on,
// or the active monitor's when old is on none any more (unplugged, or moved
// out from under it by a resolution or layout change).
func rehome(old rect) (rect, float64) {
	r := w32.RECT{Left: int32(old.L), Top: int32(old.T), Right: int32(old.R), Bottom: int32(old.B)}
	if mon := w32.MonitorFromRect(&r, w32.MONITOR_DEFAULTTONULL); mon != 0 {
		return workAreaOf(mon)
	}
	return activeWorkArea()
}

// workAreaOf returns mon's work area (physical px) and DPI scale.
func workAreaOf(mon w32.HMONITOR) (rect, float64) {
	mi := w32.MONITORINFO{}
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	w32.GetMonitorInfo(mon, &mi)
	s := 1.0
	var dx, dy uint32
	if r, _, _ := procDpiForMon.Call(uintptr(mon), 0, uintptr(unsafe.Pointer(&dx)), uintptr(unsafe.Pointer(&dy))); r == 0 && dx > 0 {
		s = float64(dx) / 96
	}
	wa := mi.RcWork
	return rect{L: int(wa.Left), T: int(wa.Top), R: int(wa.Right), B: int(wa.Bottom)}, s
}

// newFont creates a font of sizeDip at the given DPI scale. The caller deletes
// it.
func newFont(face string, sizeDip float64, weight int32, scale float64) w32.HFONT {
	lf := w32.LOGFONT{Height: -int32(sizeDip*scale + 0.5), Weight: weight, Quality: 5 /* CLEARTYPE */}
	copy(lf.FaceName[:len(lf.FaceName)-1], windows.StringToUTF16(face))
	return w32.CreateFontIndirect(&lf)
}

// textSize measures s in font (DT_CALCRECT) inside width px; width 0 means a
// single line.
func textSize(hdc w32.HDC, font w32.HFONT, s string, width int) (w, h int) {
	old := w32.SelectObject(hdc, w32.HGDIOBJ(font))
	defer w32.SelectObject(hdc, old)
	r := w32.RECT{Right: int32(width)}
	format := uint32(w32.DT_CALCRECT | w32.DT_NOPREFIX | w32.DT_SINGLELINE)
	if width > 0 {
		format = w32.DT_CALCRECT | w32.DT_NOPREFIX | w32.DT_WORDBREAK
	}
	u := windows.StringToUTF16(s)
	w32.DrawText(hdc, u[:len(u)-1], -1, &r, format)
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// measure sizes cw for its content at cw.scale: body lines (with the real font
// via DT_CALCRECT, so it tracks DPI), height, the x and the buttons. Past two
// lines the body is ellipsized: a card is a glance, not a paragraph.
func measure(cw *cardWin) {
	sc := cw.scale
	cw.w = px(cardWidthDip, sc)
	cw.lines = 0
	var labelW []int
	if hdc := w32.GetDC(0); hdc != 0 {
		if cw.body != "" {
			f := newFont("Segoe UI", bodySizeDip, 400, sc)
			_, one := textSize(hdc, f, "X", 0)
			_, all := textSize(hdc, f, cw.body, cw.w-px(textLeftDip, sc)-px(textRightDip, sc))
			w32.DeleteObject(w32.HGDIOBJ(f))
			cw.lines = 1
			if one > 0 && all > one+one/2 {
				cw.lines = maxBodyLines
			}
		}
		if len(cw.labels) > 0 {
			f := newFont("Segoe UI", buttonSizeDip, 600, sc)
			for _, l := range cw.labels {
				lw, _ := textSize(hdc, f, l, 0)
				labelW = append(labelW, lw)
			}
			w32.DeleteObject(w32.HGDIOBJ(f))
		}
		w32.ReleaseDC(0, hdc)
	} else if cw.body != "" {
		cw.lines = 1
	}
	cw.h = px(cardHeightDip(cw.lines, len(cw.labels) > 0), sc)
	cw.close = closeRect(cw.w, sc)
	cw.buttons = buttonRects(cw.w, cw.h, labelW, sc)
	if len(cw.buttons) < len(cw.labels) { // GetDC failed: still clickable
		cw.buttons = buttonRects(cw.w, cw.h, make([]int, len(cw.labels)), sc)
	}
}

// paintCard draws cw into an off-screen bitmap and copies it in, so a hover
// repaint doesn't flicker.
func paintCard(cw *cardWin) {
	var ps w32.PAINTSTRUCT
	hdc := w32.BeginPaint(cw.hwnd, &ps)
	defer w32.EndPaint(cw.hwnd, &ps)
	w, h := cw.w, cw.h
	mem := w32.CreateCompatibleDC(hdc)
	if mem == 0 {
		return
	}
	defer w32.DeleteDC(mem)
	bmp, _, _ := procCompatBmp.Call(uintptr(hdc), uintptr(w), uintptr(h))
	if bmp == 0 {
		return
	}
	defer w32.DeleteObject(w32.HGDIOBJ(bmp))
	old := w32.SelectObject(mem, w32.HGDIOBJ(bmp))
	drawCard(mem, cw)
	_, _, _ = procBitBlt.Call(uintptr(hdc), 0, 0, uintptr(w), uintptr(h), uintptr(mem), 0, 0, w32.SRCCOPY)
	w32.SelectObject(mem, old)
}

func drawCard(hdc w32.HDC, cw *cardWin) {
	sc := cw.scale
	p := func(dip float64) int32 { return int32(px(dip, sc)) }
	w, h := int32(cw.w), int32(cw.h)
	fill := func(r w32.RECT, c uint32) {
		b := w32.CreateSolidBrush(w32.COLORREF(c))
		w32.FillRect(hdc, &r, b)
		w32.DeleteObject(w32.HGDIOBJ(b))
	}
	frame := func(r w32.RECT, c uint32) {
		b := w32.CreateSolidBrush(w32.COLORREF(c))
		w32.FrameRect(hdc, &r, b)
		w32.DeleteObject(w32.HGDIOBJ(b))
	}
	text := func(s, face string, sizeDip float64, weight int32, c uint32, r w32.RECT, format uint32) {
		f := newFont(face, sizeDip, weight, sc)
		old := w32.SelectObject(hdc, w32.HGDIOBJ(f))
		w32.SetTextColor(hdc, w32.COLORREF(c))
		u := windows.StringToUTF16(s)
		w32.DrawText(hdc, u[:len(u)-1], -1, &r, format|w32.DT_NOPREFIX)
		w32.SelectObject(hdc, old)
		w32.DeleteObject(w32.HGDIOBJ(f))
	}
	in := func(r rect) w32.RECT {
		return w32.RECT{Left: int32(r.L), Top: int32(r.T), Right: int32(r.R), Bottom: int32(r.B)}
	}
	w32.SetBkMode(hdc, w32.TRANSPARENT)

	all := w32.RECT{Right: w, Bottom: h}
	fill(all, colCard)
	frame(all, colBorder)
	accent := accentFor(cw.kind)
	fill(w32.RECT{Right: p(stripeDip), Bottom: h}, accent)

	top := p(padTopDip)
	text(iconFor(cw.kind), iconFace, iconSizeDip, 400, accent,
		w32.RECT{Left: p(iconLeftDip), Top: top, Right: p(textLeftDip), Bottom: top + p(titleDip)},
		w32.DT_SINGLELINE|w32.DT_VCENTER|w32.DT_NOCLIP)
	text(cw.title, "Segoe UI", titleSizeDip, 600, colTitle,
		w32.RECT{Left: p(textLeftDip), Top: top, Right: w - p(titleRightDip), Bottom: top + p(titleDip)},
		w32.DT_SINGLELINE|w32.DT_VCENTER|w32.DT_END_ELLIPSIS)
	if cw.lines > 0 {
		// The body wraps to the lines the card was sized for (measure);
		// DT_EDITCONTROL hides a partial extra line and DT_END_ELLIPSIS marks the cut.
		bt := top + p(titleDip+bodyGapDip)
		text(cw.body, "Segoe UI", bodySizeDip, 400, colBody,
			w32.RECT{Left: p(textLeftDip), Top: bt, Right: w - p(textRightDip), Bottom: bt + p(float64(cw.lines*bodyLineDip))},
			w32.DT_WORDBREAK|w32.DT_END_ELLIPSIS|w32.DT_EDITCONTROL)
	}

	xc := colFaint
	switch {
	case cw.hot == partClose:
		fill(in(cw.close), colHot)
		xc = colTitle
	case hoverKey == cw.key:
		xc = colBody
	}
	text(glyphX, iconFace, 9, 400, xc, in(cw.close), w32.DT_SINGLELINE|w32.DT_VCENTER|w32.DT_CENTER)

	for i, b := range cw.buttons {
		if i >= len(cw.labels) {
			break
		}
		hot := cw.hot == part(i)
		bg, fg := colButton, colTitle
		if hot {
			bg = colHot
		}
		if i == 0 { // primary
			bg, fg = colPrimary, colOnPrimary
			if hot {
				bg = colPrimaryHot
			}
		}
		fill(in(b), bg)
		if i != 0 {
			frame(in(b), colBorder)
		}
		text(cw.labels[i], "Segoe UI", buttonSizeDip, 600, fg, in(b),
			w32.DT_SINGLELINE|w32.DT_VCENTER|w32.DT_CENTER|w32.DT_END_ELLIPSIS)
	}
}

// cardWindows returns the card windows oldest first (tests).
func cardWindows() []w32.HWND {
	mu.Lock()
	defer mu.Unlock()
	return append([]w32.HWND(nil), hwnds...)
}
