//go:build windows

package hotkey

import (
	"runtime"
	"sync"
	"syscall"
	"unicode"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------------------
// Win32 procs. golang.org/x/sys/windows exports none of the hook / message-loop
// APIs, so we lazy-proc them exactly like internal/export/clipboard_windows.go.
// ---------------------------------------------------------------------------

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procSetWindowsHookExW   = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procPostThreadMessageW  = user32.NewProc("PostThreadMessageW")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")

	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")

	// RtlMoveMemory(dst, src, len) lets us copy the KBDLLHOOKSTRUCT out of the
	// hook's lParam without converting that uintptr to an unsafe.Pointer (which
	// `go vet`'s unsafeptr analyzer rejects). Mirrors the dodge in
	// internal/export/clipboard_windows.go.
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

const (
	whKeyboardLL = 13 // WH_KEYBOARD_LL

	wmKeyDown    = 0x0100 // WM_KEYDOWN
	wmKeyUp      = 0x0101 // WM_KEYUP
	wmSysKeyDown = 0x0104 // WM_SYSKEYDOWN (Alt/Win chords arrive here)
	wmSysKeyUp   = 0x0105 // WM_SYSKEYUP
	wmQuit       = 0x0012 // WM_QUIT

	hcAction = 0 // HC_ACTION

	vkLWin    = 0x5B // VK_LWIN
	vkRWin    = 0x5C // VK_RWIN
	vkShift   = 0x10 // VK_SHIFT  (merged L+R)
	vkControl = 0x11 // VK_CONTROL (merged L+R)
	vkMenu    = 0x12 // VK_MENU / Alt (merged L+R)

	asyncDownBit = 0x8000 // GetAsyncKeyState high bit => key currently down
)

// kbdllhookstruct mirrors the Win32 KBDLLHOOKSTRUCT. The hook's lParam points at
// one of these; we read VkCode and never retain the pointer past the proc return.
type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// msg mirrors the Win32 MSG used by the GetMessageW pump on the hook thread.
type msg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
}

// Package-level callback + active manager. The syscall.NewCallback trampoline
// MUST be a package-level var created exactly once: a local would be GC'd and
// crash the process on the next keystroke. NewCallback can't capture instance
// state, so the proc finds the live Manager via `active` (single-app-instance
// assumption — Toru is single-instance).
var (
	cbOnce sync.Once
	hookCB uintptr
	active *Manager
	actMu  sync.Mutex
)

// keyDown reports whether vk is currently down via GetAsyncKeyState's high bit.
func keyDown(vk int) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&asyncDownBit != 0
}

// triggerVK maps a binding Key rune to its virtual-key code. For A-Z and 0-9 the
// VK equals the ASCII uppercase rune ('S'==0x53, '1'==0x31).
func triggerVK(key rune) uint32 {
	return uint32(unicode.ToUpper(key))
}

// installHook publishes m as the active manager, lazily builds the trampoline,
// then spins up a dedicated OS thread that installs the WH_KEYBOARD_LL hook and
// pumps messages. NEVER installs on Wails' main thread (Wails owns that pump).
func (m *Manager) installHook() {
	actMu.Lock()
	active = m
	actMu.Unlock()

	cbOnce.Do(func() { hookCB = syscall.NewCallback(lowLevelKeyboardProc) })

	go func() {
		// The hook + its GetMessageW pump must live on ONE dedicated OS thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		tid, _, _ := procGetCurrentThreadId.Call()

		// hMod=0: a global LL hook needs no module handle.
		hook, _, _ := procSetWindowsHookExW.Call(uintptr(whKeyboardLL), hookCB, 0, 0)
		if hook == 0 {
			// Soft failure: no OS keys will fire, but Trigger()/dispatch still
			// work. Do not pump a message loop with no hook installed.
			return
		}

		m.mu.Lock()
		m.hook = hook
		m.threadID = uint32(tid)
		m.mu.Unlock()

		// Pump. An LL-hook thread only needs to keep pumping; the OS delivers the
		// callback directly, so no TranslateMessage/DispatchMessage is needed.
		// GetMessageW returns BOOL: 0 == WM_QUIT, -1 == error; break on <= 0.
		var mm msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&mm)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
		}
	}()
}

// stopHook unhooks and breaks the GetMessageW pump so the hook thread exits
// cleanly, then clears active (if it still points at m). Callable from any thread.
func (m *Manager) stopHook() {
	m.mu.Lock()
	hook := m.hook
	tid := m.threadID
	m.hook = 0
	m.threadID = 0
	m.mu.Unlock()

	if hook != 0 {
		_, _, _ = procUnhookWindowsHookEx.Call(hook)
	}
	if tid != 0 {
		_, _, _ = procPostThreadMessageW.Call(uintptr(tid), uintptr(wmQuit), 0, 0)
	}

	actMu.Lock()
	if active == m {
		active = nil
	}
	actMu.Unlock()
}

// lowLevelKeyboardProc is the WH_KEYBOARD_LL callback. It MUST be fast, IO-free,
// and must NOT call into Wails (Windows silently drops a slow LL hook). On a
// matching key-down it does a non-blocking buffered send and RETURNS 1 to swallow
// the key (so Snipping Tool never sees Win+Shift+S); everything else chains on.
func lowLevelKeyboardProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode == hcAction && (wParam == wmKeyDown || wParam == wmSysKeyDown) {
		// Copy the KBDLLHOOKSTRUCT out of lParam via RtlMoveMemory rather than
		// casting the uintptr to an unsafe.Pointer (go vet's unsafeptr analyzer
		// rejects that for a non-syscall-return uintptr). The pointer is never
		// retained past this proc.
		var ks kbdllhookstruct
		_, _, _ = procRtlMoveMemory.Call(
			uintptr(unsafe.Pointer(&ks)),
			lParam,
			unsafe.Sizeof(ks),
		)
		vk := ks.VkCode

		actMu.Lock()
		m := active
		actMu.Unlock()

		if m != nil {
			matched := ""
			m.mu.Lock()
			for action, b := range m.bindings {
				if b.Key == 0 || triggerVK(b.Key) != vk {
					continue
				}
				// Every modifier the binding sets must currently be down.
				// Shift/Ctrl/Alt use the merged L+R virtual keys; only Win needs
				// the explicit L/R check.
				if b.Win && !keyDown(vkLWin) && !keyDown(vkRWin) {
					continue
				}
				if b.Shift && !keyDown(vkShift) {
					continue
				}
				if b.Ctrl && !keyDown(vkControl) {
					continue
				}
				if b.Alt && !keyDown(vkMenu) {
					continue
				}
				matched = action
				break
			}
			m.mu.Unlock()

			if matched != "" {
				// Non-blocking send; drop if the dispatch goroutine is backed up.
				// NEVER block the hook proc.
				select {
				case m.sig <- matched:
				default:
				}
				// Swallow: do NOT chain to the next hook, so the OS snip (and any
				// other consumer) never sees this key-down.
				return 1
			}
		}
	}

	r, _, _ := procCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, lParam)
	return r
}
