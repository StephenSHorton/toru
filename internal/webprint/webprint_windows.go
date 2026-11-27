//go:build windows

package webprint

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/wailsapp/wails/webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	ole32                = windows.NewLazySystemDLL("ole32.dll")
	procRegisterClassExW = user32.NewProc("RegisterClassExW")
	procCreateWindowExW  = user32.NewProc("CreateWindowExW")
	procDestroyWindow    = user32.NewProc("DestroyWindow")
	procDefWindowProcW   = user32.NewProc("DefWindowProcW")
	procPeekMessageW     = user32.NewProc("PeekMessageW")
	procTranslateMessage = user32.NewProc("TranslateMessage")
	procDispatchMessageW = user32.NewProc("DispatchMessageW")
	procShowWindow       = user32.NewProc("ShowWindow")
	procCoInitializeEx   = ole32.NewProc("CoInitializeEx")
	procCoUninitialize   = ole32.NewProc("CoUninitialize")
)

const (
	wsPopup         = 0x80000000
	wsExToolWindow  = 0x00000080
	wsExNoActivate  = 0x08000000
	swShowNoActive  = 4
	pmRemove        = 1
	coinitApartment = 0x2
	offScreen       = -32000
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSm     windows.Handle
}

type msg struct {
	Hwnd    windows.HWND
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

var (
	classOnce sync.Once
	classErr  error
	className = windows.StringToUTF16Ptr("ToruWebPrint")
)

func registerClass() error {
	classOnce.Do(func() {
		wc := wndClassEx{
			WndProc:   windows.NewCallback(func(h, m, w, l uintptr) uintptr { r, _, _ := procDefWindowProcW.Call(h, m, w, l); return r }),
			ClassName: className,
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
			classErr = fmt.Errorf("webprint: RegisterClassEx: %w", err)
		}
	})
	return classErr
}

// Page is an HTML file open in a WebView2 out of sight.
type Page struct {
	calls chan func()
	quit  chan struct{}
	done  chan struct{}

	mu       sync.Mutex
	messages []string
	signal   chan struct{}
	failed   error
	broken   chan struct{} // closed when WebView2 reports an error

	chromium *edge.Chromium
}

// Open loads the HTML file at path in a new WebView2 and returns once the
// page has finished loading. Close it when done.
func Open(ctx context.Context, path string, opt Options) (*Page, error) {
	if opt.DataDir == "" {
		return nil, errors.New("webprint: no DataDir")
	}
	if opt.Width <= 0 || opt.Height <= 0 {
		opt.Width, opt.Height = 1280, 800
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 30 * time.Second
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(opt.DataDir, 0o755); err != nil {
		return nil, err
	}
	if err := registerClass(); err != nil {
		return nil, err
	}
	p := &Page{
		calls:  make(chan func(), 16),
		quit:   make(chan struct{}),
		done:   make(chan struct{}),
		signal: make(chan struct{}, 1),
		broken: make(chan struct{}),
	}
	loaded := make(chan error, 1)
	go p.run(abs, opt, loaded)
	timer := time.NewTimer(opt.Timeout)
	defer timer.Stop()
	select {
	case err := <-loaded:
		if err != nil {
			p.Close()
			return nil, err
		}
		return p, nil
	case <-p.broken:
		p.Close()
		return nil, p.err()
	case <-ctx.Done():
		p.Close()
		return nil, ctx.Err()
	case <-timer.C:
		p.Close()
		return nil, errors.New("webprint: the page didn't load in time")
	}
}

// run owns the window and the WebView2: everything COM happens on its
// thread, which pumps messages until Close.
func (p *Page) run(path string, opt Options, loaded chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)

	_, _, _ = procCoInitializeEx.Call(0, coinitApartment)
	defer func() { _, _, _ = procCoUninitialize.Call() }()

	hwnd, _, err := procCreateWindowExW.Call(
		wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)), 0,
		wsPopup,
		offScreenPos(), offScreenPos(),
		uintptr(opt.Width), uintptr(opt.Height),
		0, 0, 0, 0)
	if hwnd == 0 {
		loaded <- fmt.Errorf("webprint: CreateWindowEx: %w", err)
		return
	}
	defer func() { _, _, _ = procDestroyWindow.Call(hwnd) }()
	// Shown, but off every screen and never active: WebView2 renders as for
	// a visible window, and nobody sees it.
	_, _, _ = procShowWindow.Call(hwnd, swShowNoActive)

	c := edge.NewChromium()
	c.DataPath = opt.DataDir
	c.BrowserPath = opt.browserPath
	// edge.Chromium exits the process after its error callback returns
	// (errorCallback calls os.Exit). An export must never quit Toru, so the
	// callback reports the error and never returns: this page's thread
	// stops there, and Open, Next and Close give up on it.
	c.SetErrorCallback(func(err error) {
		p.fail(err)
		select {}
	})
	var once sync.Once
	c.NavigationCompletedCallback = func(_ *edge.ICoreWebView2, args *edge.ICoreWebView2NavigationCompletedEventArgs) {
		once.Do(func() { loaded <- nil })
	}
	c.MessageCallback = func(m string, _ *edge.ICoreWebView2, _ *edge.ICoreWebView2WebMessageReceivedEventArgs) {
		p.mu.Lock()
		p.messages = append(p.messages, m)
		p.mu.Unlock()
		select {
		case p.signal <- struct{}{}:
		default:
		}
	}
	if !c.Embed(hwnd) {
		loaded <- errors.New("webprint: WebView2 didn't start")
		return
	}
	p.chromium = c
	c.Resize()
	_ = c.Show()
	c.Navigate((&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(path), Fragment: opt.Fragment}).String())

	var m msg
	for {
		for {
			r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
			if r == 0 {
				break
			}
			_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
			_, _, _ = procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		select {
		case f := <-p.calls:
			f()
		case <-p.quit:
			c.ShuttingDown()
			if ctl := c.GetController(); ctl != nil {
				closeController(ctl)
			}
			return
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}

// offScreenPos is the window's x and y, off every screen (a negative int,
// sign-extended as Win32 reads it).
func offScreenPos() uintptr {
	v := int32(offScreen)
	return uintptr(v)
}

func (p *Page) fail(err error) {
	p.mu.Lock()
	if p.failed == nil {
		p.failed = fmt.Errorf("webprint: WebView2: %w", err)
		close(p.broken)
	}
	p.mu.Unlock()
	select {
	case p.signal <- struct{}{}:
	default:
	}
}

// do runs f on the page's thread and waits for it.
func (p *Page) do(ctx context.Context, f func() error) error {
	res := make(chan error, 1)
	select {
	case p.calls <- func() { res <- f() }:
	case <-p.broken:
		return p.err()
	case <-p.done:
		return errors.New("webprint: the page is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-res:
		return err
	case <-p.broken:
		return p.err()
	case <-p.done:
		return errors.New("webprint: the page is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Eval runs script in the page. It doesn't wait for the script's result:
// the page answers with postMessage, which Next reads.
func (p *Page) Eval(ctx context.Context, script string) error {
	return p.do(ctx, func() error { p.chromium.Eval(script); return nil })
}

// Next is the next message from the page that starts with prefix, without
// the prefix. Messages with another prefix are kept for later. A message
// "error:<text>" from the page fails it.
func (p *Page) Next(ctx context.Context, prefix string) (string, error) {
	for {
		p.mu.Lock()
		if p.failed != nil {
			err := p.failed
			p.mu.Unlock()
			return "", err
		}
		for i, m := range p.messages {
			if strings.HasPrefix(m, prefix) {
				p.messages = append(p.messages[:i:i], p.messages[i+1:]...)
				p.mu.Unlock()
				return strings.TrimPrefix(m, prefix), nil
			}
			if strings.HasPrefix(m, "error:") {
				p.messages = append(p.messages[:i:i], p.messages[i+1:]...)
				p.mu.Unlock()
				return "", errors.New(strings.TrimPrefix(m, "error:"))
			}
		}
		p.mu.Unlock()
		select {
		case <-p.signal:
		case <-p.done:
			return "", errors.New("webprint: the page closed")
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// Close shuts the page's WebView2 and window.
func (p *Page) Close() {
	select {
	case <-p.quit:
	default:
		close(p.quit)
	}
	// A page whose WebView2 broke may never come back to its loop: don't
	// wait on it for long.
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

func (p *Page) err() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failed
}

// ── PrintToPdf ──────────────────────────────────────────────────────────

// COM vtable slots, counted from WebView2.h (each interface's methods follow
// its parent's): ICoreWebView2 has 58 methods after IUnknown's 3, _2 adds
// 7, _3 5, _4 4, _5 2, _6 1, then _7's PrintToPdf. ICoreWebView2Environment
// has 5, Environment2 adds 1, 3 adds 2, 4 adds 1, 5 adds 2, then 6's
// CreatePrintSettings.
const (
	slotQueryInterface      = 0
	slotRelease             = 2
	slotPrintToPdf          = 3 + 58 + 7 + 5 + 4 + 2 + 1 // 80
	slotCreatePrintSettings = 3 + 5 + 1 + 2 + 1 + 2      // 14
	// ICoreWebView2PrintSettings, after IUnknown: get/put pairs.
	slotPutOrientation           = 3 + 1
	slotPutScaleFactor           = 3 + 3
	slotPutPageWidth             = 3 + 5
	slotPutPageHeight            = 3 + 7
	slotPutMarginTop             = 3 + 9
	slotPutMarginBottom          = 3 + 11
	slotPutMarginLeft            = 3 + 13
	slotPutMarginRight           = 3 + 15
	slotPutShouldPrintBackground = 3 + 17
	slotPutShouldPrintHeaderFoot = 3 + 21
	// ICoreWebView2Controller: Close is its 22nd method after IUnknown.
	slotControllerClose = 3 + 21
)

var (
	iidICoreWebView2_7           = windows.GUID{Data1: 0x79c24d83, Data2: 0x09a3, Data3: 0x45ae, Data4: [8]byte{0x94, 0x18, 0x48, 0x7f, 0x32, 0xa5, 0x87, 0x40}}
	iidICoreWebView2Environment6 = windows.GUID{Data1: 0xe59ee362, Data2: 0xacbd, Data3: 0x4857, Data4: [8]byte{0x9a, 0x8e, 0xd3, 0x64, 0x4d, 0x94, 0x59, 0xa9}}
)

// method is slot i of the COM object at obj's vtable.
func method(obj unsafe.Pointer, i int) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	return *(*uintptr)(unsafe.Add(vtbl, uintptr(i)*unsafe.Sizeof(uintptr(0))))
}

func call(obj unsafe.Pointer, i int, args ...uintptr) error {
	hr, _, _ := syscall.SyscallN(method(obj, i), append([]uintptr{uintptr(obj)}, args...)...)
	if int32(hr) < 0 {
		return fmt.Errorf("HRESULT 0x%08x", uint32(hr))
	}
	return nil
}

func release(obj unsafe.Pointer) {
	if obj != nil {
		_, _, _ = syscall.SyscallN(method(obj, slotRelease), uintptr(obj))
	}
}

func queryInterface(obj unsafe.Pointer, iid *windows.GUID) (unsafe.Pointer, error) {
	var out unsafe.Pointer
	if err := call(obj, slotQueryInterface, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); err != nil {
		return nil, err
	}
	return out, nil
}

func closeController(ctl *edge.ICoreWebView2Controller) {
	_ = call(unsafe.Pointer(ctl), slotControllerClose)
}

// The completion handler PrintToPdf calls: a COM object whose Invoke gets
// (HRESULT, BOOL). Its callbacks are made once for the process (Windows
// allows only so many) and find their handler by its address.
type pdfHandler struct {
	vtbl *pdfHandlerVtbl
	done chan pdfResult
}

type pdfHandlerVtbl struct{ QueryInterface, AddRef, Release, Invoke uintptr }

type pdfResult struct {
	hr uintptr
	ok bool
}

var (
	pdfVtblOnce sync.Once
	pdfVtbl     pdfHandlerVtbl
	pdfLive     sync.Map // *pdfHandler → struct{}: kept reachable while WebView2 holds it
)

func newPDFHandler() *pdfHandler {
	pdfVtblOnce.Do(func() {
		pdfVtbl = pdfHandlerVtbl{
			QueryInterface: windows.NewCallback(func(this *pdfHandler, iid uintptr, out **pdfHandler) uintptr {
				*out = this
				return 0
			}),
			AddRef:  windows.NewCallback(func(this *pdfHandler) uintptr { return 1 }),
			Release: windows.NewCallback(func(this *pdfHandler) uintptr { return 1 }),
			Invoke: windows.NewCallback(func(h *pdfHandler, hr, ok uintptr) uintptr {
				select {
				case h.done <- pdfResult{hr: hr, ok: uint32(ok) != 0}:
				default:
				}
				return 0
			}),
		}
	})
	h := &pdfHandler{vtbl: &pdfVtbl, done: make(chan pdfResult, 1)}
	pdfLive.Store(h, struct{}{})
	return h
}

// PrintPDF prints the page to a PDF file at out, through WebView2's
// PrintToPdf: text and lines stay vector.
func (p *Page) PrintPDF(ctx context.Context, out string, opt PDFOptions) error {
	abs, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	h := newPDFHandler()
	defer pdfLive.Delete(h)
	err = p.do(ctx, func() error {
		ctl := p.chromium.GetController()
		if ctl == nil {
			return errors.New("webprint: no WebView2")
		}
		wv, err := ctl.GetCoreWebView2()
		if err != nil {
			return err
		}
		wv7, err := queryInterface(unsafe.Pointer(wv), &iidICoreWebView2_7)
		if err != nil || wv7 == nil {
			return errors.New("webprint: this WebView2 can't print to PDF (it needs ICoreWebView2_7); update the WebView2 runtime")
		}
		defer release(wv7)
		settings, err := p.printSettings(opt)
		if err != nil {
			return err
		}
		defer release(settings)
		path, err := windows.UTF16PtrFromString(abs)
		if err != nil {
			return err
		}
		return call(wv7, slotPrintToPdf, uintptr(unsafe.Pointer(path)), uintptr(settings), uintptr(unsafe.Pointer(h)))
	})
	if err != nil {
		return fmt.Errorf("webprint: PrintToPdf: %w", err)
	}
	select {
	case r := <-h.done:
		if int32(r.hr) < 0 || !r.ok {
			return fmt.Errorf("webprint: PrintToPdf failed (HRESULT 0x%08x)", uint32(r.hr))
		}
		return nil
	case <-p.broken:
		return p.err()
	case <-p.done:
		return errors.New("webprint: the page closed while printing")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Page) printSettings(opt PDFOptions) (unsafe.Pointer, error) {
	env := p.chromium.Environment()
	if env == nil {
		return nil, errors.New("webprint: no WebView2 environment")
	}
	env6, err := queryInterface(unsafe.Pointer(env), &iidICoreWebView2Environment6)
	if err != nil || env6 == nil {
		return nil, errors.New("webprint: this WebView2 has no print settings; update the WebView2 runtime")
	}
	defer release(env6)
	var s unsafe.Pointer
	if err := call(env6, slotCreatePrintSettings, uintptr(unsafe.Pointer(&s))); err != nil {
		return nil, err
	}
	f := func(v float64) uintptr { return uintptr(math.Float64bits(v)) }
	b := func(v bool) uintptr {
		if v {
			return 1
		}
		return 0
	}
	scale := opt.Scale
	if scale == 0 {
		scale = 1
	}
	set := []struct {
		slot int
		v    uintptr
	}{
		{slotPutOrientation, 0}, // portrait: the page size says which way
		{slotPutScaleFactor, f(scale)},
		{slotPutPageWidth, f(opt.PageWidth)},
		{slotPutPageHeight, f(opt.PageHeight)},
		{slotPutMarginTop, f(opt.Margin)},
		{slotPutMarginBottom, f(opt.Margin)},
		{slotPutMarginLeft, f(opt.Margin)},
		{slotPutMarginRight, f(opt.Margin)},
		{slotPutShouldPrintBackground, b(opt.Backgrounds)},
		{slotPutShouldPrintHeaderFoot, 0},
	}
	for _, x := range set {
		if x.slot == slotPutPageWidth && opt.PageWidth <= 0 || x.slot == slotPutPageHeight && opt.PageHeight <= 0 {
			continue
		}
		if err := call(s, x.slot, x.v); err != nil {
			release(s)
			return nil, fmt.Errorf("print settings: %w", err)
		}
	}
	return s, nil
}
