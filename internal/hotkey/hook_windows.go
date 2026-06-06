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

	// chordHeld + heldVK implement fire-on-first-down latching (guarded by actMu).
	// Holding a chord generates auto-repeat WM_KEYDOWNs (~30/sec); without a latch
	// each one would dispatch another OpenOverlay (overlay teardown+refreeze storm).
	// On the first matched down we set chordHeld=true + remember the trigger VK; the
	// repeats are still swallowed (return 1) but not re-dispatched. We clear the
	// latch when that trigger key's WM_KEYUP/WM_SYSKEYUP arrives, re-arming the next
	// real press.
	chordHeld bool
	heldVK    uint32
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
		vk := ks.VkCode

		// Key-up of the latched trigger key clears the held latch so the next
		// real press fires again. Fall through (do NOT swallow) — the up event is
		// harmless and the OS may need it.
		if isUp {
			actMu.Lock()
			if chordHeld && vk == heldVK {
				chordHeld = false
				heldVK = 0
			}
			actMu.Unlock()
			r, _, _ := procCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, lParam)
			return r
		}

		// isDown from here on.
		actMu.Lock()
		m := active
		actMu.Unlock()

		if m != nil {
			// Snapshot ONLY the candidate bindings (those whose trigger VK == vk)
			// under m.mu, then release the lock BEFORE the GetAsyncKeyState modifier
			// checks. m.mu is shared with SetBinding/snapshot/Trigger/dispatch, and
			// the LL-hook proc is the most latency-sensitive path in the system
			// (Windows silently drops a hook that exceeds LowLevelHooksTimeout), so
			// no user32 syscalls run under the shared lock. The async key state is
			// global + lock-independent, so reading it lock-free is race-safe.
			type cand struct {
				action string
				b      Binding
			}
			var cands []cand
			m.mu.Lock()
			for action, b := range m.bindings {
				if b.Key == 0 || triggerVK(b.Key) != vk {
					continue
				}
				cands = append(cands, cand{action, b})
			}
			m.mu.Unlock()

			matched := ""
			for _, c := range cands {
				b := c.b
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
				matched = c.action
				break
			}

			if matched != "" {
				// Fire-on-first-down: only dispatch on the FIRST matched key-down.
				// Auto-repeat key-downs (holding the chord, ~30/sec) are still
				// swallowed (return 1, so Snipping Tool never sees them) but NOT
				// re-dispatched — otherwise each repeat would trigger another
				// OpenOverlay (overlay teardown+refreeze storm). The latch is
				// cleared on the trigger key's key-up above.
				actMu.Lock()
				// First down = no chord latched, OR a different trigger key is
				// latched (so distinct chords don't suppress each other; only the
				// SAME held key's auto-repeats are coalesced).
				firstDown := !chordHeld || heldVK != vk
				if firstDown {
					chordHeld = true
					heldVK = vk
				}
				actMu.Unlock()

				if firstDown {
					// Non-blocking send; drop if the dispatch goroutine is backed
					// up. NEVER block the hook proc.
					select {
					case m.sig <- matched:
					default:
					}
				}
				// Swallow EVERY matched down (first + repeats): do NOT chain to the
				// next hook, so the OS snip (and any other consumer) never sees it.
				return 1
			}
		}
	}

	r, _, _ := procCallNextHookEx.Call(0, uintptr(uint32(nCode)), wParam, lParam)
	return r
}
