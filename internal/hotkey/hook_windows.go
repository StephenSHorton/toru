//go:build windows

package hotkey

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
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
	procRegisterHotKey      = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey    = user32.NewProc("UnregisterHotKey")

	procWindowFromPoint          = user32.NewProc("WindowFromPoint")
	procGetAncestor              = user32.NewProc("GetAncestor")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")

	procGetCurrentThreadId = kernel32.NewProc("GetCurrentThreadId")

	// RtlMoveMemory(dst, src, len) lets us copy the hook structs out of the
	// hook's lParam without converting that uintptr to an unsafe.Pointer (which
	// `go vet`'s unsafeptr analyzer rejects). Mirrors the dodge in
	// internal/export/clipboard_windows.go.
	procRtlMoveMemory = kernel32.NewProc("RtlMoveMemory")
)

const (
	whKeyboardLL = 13 // WH_KEYBOARD_LL
	whMouseLL    = 14 // WH_MOUSE_LL

	wmKeyDown    = 0x0100 // WM_KEYDOWN
	wmKeyUp      = 0x0101 // WM_KEYUP
	wmSysKeyDown = 0x0104 // WM_SYSKEYDOWN (Alt/Win chords arrive here)
	wmSysKeyUp   = 0x0105 // WM_SYSKEYUP
	wmQuit       = 0x0012 // WM_QUIT

	wmMButtonDown = 0x0207 // WM_MBUTTONDOWN
	wmMButtonUp   = 0x0208 // WM_MBUTTONUP
	wmXButtonDown = 0x020B // WM_XBUTTONDOWN (HIWORD(mouseData): 1 = X1, 2 = X2)
	wmXButtonUp   = 0x020C // WM_XBUTTONUP

	hcAction = 0 // HC_ACTION
	gaRoot   = 2 // GA_ROOT

	asyncDownBit = 0x8000 // GetAsyncKeyState high bit => key currently down
)

// kbdllhookstruct mirrors the Win32 KBDLLHOOKSTRUCT. The hook's lParam points at
// one of these; we copy it out and never retain the pointer past the proc return.
type kbdllhookstruct struct {
	VkCode      uint32
	ScanCode    uint32
	Flags       uint32
	Time        uint32
	DwExtraInfo uintptr
}

// msllhookstruct mirrors the Win32 MSLLHOOKSTRUCT (the mouse hook's lParam).
// On 64-bit, 4 bytes of padding precede DwExtraInfo; Go lays it out the same.
type msllhookstruct struct {
	Pt          struct{ X, Y int32 }
	MouseData   uint32
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

// Package-level callbacks. The syscall.NewCallback trampolines MUST be
// package-level vars created exactly once: a local would be GC'd and crash the
// process on the next keystroke. NewCallback can't capture instance state, so
// the procs find the live Manager via `active` (hookcore.go).
var (
	cbOnce  sync.Once
	hookCB  uintptr
	mouseCB uintptr

	// mouseHook is the WH_MOUSE_LL handle. Touched only on the hook thread.
	mouseHook uintptr

	// hookTID is the hook thread's id (0 when not running), readable without
	// m.mu so requestSync can post from under actMu.
	hookTID atomic.Uint32
)

func init() {
	asyncKeyDown = func(vk uint32) bool { return keyDown(int(vk)) }
	overOwnWindow = pointOverOwnWindow
}

// keyDown reports whether vk is currently down via GetAsyncKeyState's high bit.
func keyDown(vk int) bool {
	r, _, _ := procGetAsyncKeyState.Call(uintptr(vk))
	return r&asyncDownBit != 0
}

// pointOverOwnWindow reports whether the window under the screen point
// belongs to this process (WindowFromPoint -> GA_ROOT -> owning pid).
func pointOverOwnWindow(x, y int32) bool {
	var hwnd uintptr
	if unsafe.Sizeof(uintptr(0)) == 8 {
		// POINT is passed by value, packed into one register on x64.
		hwnd, _, _ = procWindowFromPoint.Call(uintptr(uint32(x)) | uintptr(uint32(y))<<32)
	} else {
		hwnd, _, _ = procWindowFromPoint.Call(uintptr(x), uintptr(y))
	}
	if hwnd == 0 {
		return false
	}
	if root, _, _ := procGetAncestor.Call(hwnd, gaRoot); root != 0 {
		hwnd = root
	}
	var pid uint32
	_, _, _ = procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	return pid != 0 && pid == windows.GetCurrentProcessId()
}

// requestSync asks the hook thread to re-apply system hotkeys and the mouse
// hook. Lock-free, so it is safe under actMu or m.mu.
func requestSync() {
	if tid := hookTID.Load(); tid != 0 {
		_, _, _ = procPostThreadMessageW.Call(uintptr(tid), wmSyncHotkeys, 0, 0)
	}
}

// installHook publishes m as the active manager, lazily builds the trampolines,
// then spins up a dedicated OS thread that installs the WH_KEYBOARD_LL hook and
// pumps messages. NEVER installs on Wails' main thread (Wails owns that pump).
// The WH_MOUSE_LL hook is installed on the same thread, but only while some
// binding uses a mouse button (applyMouseHook): it is called on every mouse
// move, so it isn't left in the input path when nothing needs it.
func (m *Manager) installHook() {
	actMu.Lock()
	active = m
	actMu.Unlock()

	cbOnce.Do(func() {
		hookCB = syscall.NewCallback(lowLevelKeyboardProc)
		mouseCB = syscall.NewCallback(lowLevelMouseProc)
	})

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

		// Close-before-install race: stopHook may have run while we were still
		// inside SetWindowsHookExW, before m.hook/m.threadID were published — it
		// would have found 0/0 and skipped the unhook + WM_QUIT, leaving a leaked
		// system-wide LL hook and a locked OS thread pumping forever. If a stop
		// landed first, self-unhook here and bail WITHOUT entering the pump.
		m.mu.Lock()
		if m.closing {
			m.mu.Unlock()
			_, _, _ = procUnhookWindowsHookEx.Call(hook)
			return
		}
		m.hook = hook
		m.threadID = uint32(tid)
		m.mu.Unlock()
		hookTID.Store(uint32(tid))
		kbdHookUp.Store(true)

		// Pump. An LL-hook thread only needs to keep pumping; the OS delivers the
		// callback directly, so no TranslateMessage/DispatchMessage is needed.
		// GetMessageW returns BOOL: 0 == WM_QUIT, -1 == error; break on <= 0.
		m.applySystemHotkeys()

		var mm msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&mm)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			switch mm.Message {
			case wmHotkey:
				m.fireSystemHotkey(int(mm.WParam))
			case wmSyncHotkeys:
				m.applySystemHotkeys()
			}
		}
		m.unregisterSystemHotkeys()
		setMouseHook(false)
	}()
}

// System hotkeys. A low-level keyboard hook in an unelevated process is not
// called while an elevated window has focus, and many games and anti-cheat
// launchers run elevated, so a hook-only hotkey silently does nothing in
// exactly the games people clip. RegisterHotKey works regardless of the
// focused window's elevation, so actions in systemHotkeyActions are
// registered with it whenever possible; the hook skips those actions (so they
// can't fire twice) and remains the fallback when registration fails (combo
// already owned by another app, e.g. GeForce's Alt+F10). Win+Shift+S stays
// hook-only: the shell owns it and it must be swallowed. clickerToggle is here
// so Stop works over an elevated window, except while a clicker run is
// injecting (systemHotkeyWant).
//
// Bindings RegisterHotKey can't express (hookOnly: sides, chords, loose
// matching, mouse buttons, a modifier as the key) are never registered; the
// hook matches them, so they don't fire while an elevated window has focus.
//
// RegisterHotKey binds to the calling thread, so registrations are only ever
// changed on the hook thread (wmSyncHotkeys), whose message loop also
// receives WM_HOTKEY.
var systemHotkeyActions = map[string]bool{"replaySave": true, "recordToggle": true, "timelineMarker": true, "clickerToggle": true}

const (
	wmHotkey      = 0x0312 // WM_HOTKEY
	wmSyncHotkeys = 0x8000 + 0x71

	modAlt, modControl, modShift, modWin, modNoRepeat = 0x1, 0x2, 0x4, 0x8, 0x4000
)

// System hotkey registrations, touched only on the hook thread.
var (
	sysIDs      = map[string]int{} // action -> RegisterHotKey id
	sysBindings = map[int]Binding{}
	sysActions  = map[int]string{}
	sysNextID   = 1
)

// syncSystemHotkeys asks the hook thread to re-apply registrations (and the
// mouse hook) after a binding or active state changed. Safe from any goroutine.
func (m *Manager) syncSystemHotkeys() { requestSync() }

// applySystemHotkeys registers/unregisters to match the current bindings and
// installs or removes the mouse hook. Hook thread only.
func (m *Manager) applySystemHotkeys() {
	want := m.systemHotkeyWant()

	owned := map[string]bool{}
	for action, id := range sysIDs {
		if b, ok := want[action]; ok && b == sysBindings[id] {
			owned[action] = true
			continue
		}
		_, _, _ = procUnregisterHotKey.Call(0, uintptr(id))
		delete(sysIDs, action)
		delete(sysBindings, id)
		delete(sysActions, id)
	}
	for action, b := range want {
		if owned[action] {
			continue
		}
		id := sysNextID
		sysNextID++
		mods := uintptr(modNoRepeat)
		if b.Alt {
			mods |= modAlt
		}
		if b.Ctrl {
			mods |= modControl
		}
		if b.Shift {
			mods |= modShift
		}
		if b.Win {
			mods |= modWin
		}
		if r, _, _ := procRegisterHotKey.Call(0, uintptr(id), mods, uintptr(triggerVK(b.Key))); r != 0 {
			sysIDs[action], sysBindings[id], sysActions[id] = id, b, action
			owned[action] = true
		}
	}

	m.mu.Lock()
	m.sysOwned = owned
	m.mu.Unlock()

	setMouseHook(m.wantsMouseHook())
}

// systemHotkeyWant returns the actions that should be registered with
// RegisterHotKey right now, and records in toggleDeferred whether
// clickerToggle was held back.
//
// clickerToggle is left to the hook while a clicker run is injecting:
// RegisterHotKey matches modifiers against Windows' key state, which the run's
// own injected keys change (a Shift-wrapped press flips Shift every cycle, so
// a held Ctrl+Shift+Y reads as Ctrl+Y and never fires, and Ctrl+Y reaches the
// app). The hook matches with the physical-key table instead. The trade-off:
// during a run the stop hotkey doesn't fire while an elevated window has
// focus; the page and tray Stop still work. It is also held back after the run
// until its swallowed keys are released: registered while its key is still
// held, that key's auto-repeat would reach RegisterHotKey as a fresh press
// (the hook swallowed the real down) and start the clicker again.
func (m *Manager) systemHotkeyWant() map[string]Binding {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]Binding{}
	deferred := false
	for action := range systemHotkeyActions {
		b, ok := m.bindings[action]
		if !ok || b.Key == 0 || m.inactive[action] || hookOnly(b) {
			continue
		}
		if action == toggleAction && (phys.injecting.Load() || phys.anySwallowedDown(chordVKs(b))) {
			deferred = true
			continue
		}
		want[action] = b
	}
	toggleDeferred.Store(deferred)
	return want
}

// setMouseHook installs or removes the WH_MOUSE_LL hook. Hook thread only
// (the hook is called through this thread's message loop).
func setMouseHook(on bool) {
	switch {
	case on && mouseHook == 0:
		mouseHook, _, _ = procSetWindowsHookExW.Call(uintptr(whMouseLL), mouseCB, 0, 0)
		mouseHookUp.Store(mouseHook != 0)
	case !on && mouseHook != 0:
		_, _, _ = procUnhookWindowsHookEx.Call(mouseHook)
		mouseHook = 0
		mouseHookUp.Store(false)
		for vk := uint32(vkMButton); vk <= vkXButton2; vk++ {
			phys.note(vk, false) // no longer tracked; don't leave stale downs
			mouseSwallowUp[vk].Store(false)
		}
	}
}

func (m *Manager) unregisterSystemHotkeys() {
	for _, id := range sysIDs {
		_, _, _ = procUnregisterHotKey.Call(0, uintptr(id))
	}
	clear(sysIDs)
	clear(sysBindings)
	clear(sysActions)
	m.mu.Lock()
	m.sysOwned = nil
	m.mu.Unlock()
}

// fireSystemHotkey dispatches a WM_HOTKEY, unless a shortcut is being
// recorded (the hook swallows keys then, but it can't see keys aimed at an
// elevated window).
func (m *Manager) fireSystemHotkey(id int) {
	action, ok := sysActions[id]
	if !ok {
		return
	}
	if recording() {
		return
	}
	m.signal(action)
}

// stopHook unhooks and breaks the GetMessageW pump so the hook thread exits
// cleanly, then clears active (if it still points at m). Callable from any thread.
func (m *Manager) stopHook() {
	m.mu.Lock()
	// Mark closing under the same lock the install goroutine re-checks after
	// SetWindowsHookExW. This closes the window where a stop arrives before the
	// hook/threadID are published: if hook==0 here, the goroutine will see
	// m.closing and self-unhook instead of pumping forever.
	m.closing = true
	hook := m.hook
	tid := m.threadID
	m.hook = 0
	m.threadID = 0
	m.mu.Unlock()

	if hook != 0 {
		_, _, _ = procUnhookWindowsHookEx.Call(hook)
		kbdHookUp.Store(false)
	}
	if tid != 0 {
		hookTID.CompareAndSwap(tid, 0)
		// The pump removes the mouse hook on its way out.
		_, _, _ = procPostThreadMessageW.Call(uintptr(tid), uintptr(wmQuit), 0, 0)
	}

	actMu.Lock()
	if active == m {
		active = nil
	}
	actMu.Unlock()
}

func (m *Manager) beginRecord() (<-chan recordResult, error) {
	m.mu.Lock()
	installed := m.hook != 0
	m.mu.Unlock()
	if !installed {
		return nil, errors.New("the keyboard hook isn't running")
	}
	actMu.Lock()
	defer actMu.Unlock()
	return startRecordLocked(), nil
}

// lowLevelKeyboardProc is the WH_KEYBOARD_LL callback: it copies the event out
// and lets handleKey (hookcore.go) decide. On a swallow it RETURNS 1 (so the
// Snipping Tool never sees Win+Shift+S); everything else chains on.
func lowLevelKeyboardProc(nCode int32, wParam, lParam uintptr) uintptr {
	isDown := wParam == wmKeyDown || wParam == wmSysKeyDown
	isUp := wParam == wmKeyUp || wParam == wmSysKeyUp

	if nCode == hcAction && (isDown || isUp) {
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
		if handleKey(keyEvent{vk: ks.VkCode, scan: ks.ScanCode, flags: ks.Flags, extra: ks.DwExtraInfo, down: isDown}) {
			return 1
		}
	}

	r, _, _ := procCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, lParam)
	return r
}

// lowLevelMouseProc is the WH_MOUSE_LL callback. It is called for every mouse
// move (1000+/s with gaming mice), so anything but a Middle/X button message
// chains on at once, before the struct is even copied.
func lowLevelMouseProc(nCode int32, wParam, lParam uintptr) uintptr {
	if nCode == hcAction {
		var vk uint32
		down := false
		switch wParam {
		case wmMButtonDown, wmMButtonUp:
			vk, down = vkMButton, wParam == wmMButtonDown
		case wmXButtonDown, wmXButtonUp:
			vk, down = vkXButton1, wParam == wmXButtonDown
		}
		if vk != 0 {
			var ms msllhookstruct
			_, _, _ = procRtlMoveMemory.Call(
				uintptr(unsafe.Pointer(&ms)),
				lParam,
				unsafe.Sizeof(ms),
			)
			if vk == vkXButton1 && ms.MouseData>>16 == 2 {
				vk = vkXButton2
			}
			if handleMouse(mouseEvent{vk: vk, down: down, extra: ms.DwExtraInfo, x: ms.Pt.X, y: ms.Pt.Y}) {
				return 1
			}
		}
	}

	r, _, _ := procCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, lParam)
	return r
}
