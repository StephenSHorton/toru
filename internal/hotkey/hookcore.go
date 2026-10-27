package hotkey

import (
	"slices"
	"sync"
	"sync/atomic"

	"github.com/StephenSHorton/toru/internal/inputtag"
)

// The decision half of the low-level hook procs, free of syscalls so tests can
// drive it with crafted events. hook_windows.go copies the hook structs out of
// lParam, calls handleKey / handleMouse, and swallows the event (returns 1)
// when they say so; otherwise it chains to the next hook.

var (
	// active is the Manager the hook procs dispatch to (single-app-instance
	// assumption; Toru is single-instance). Guarded by actMu.
	active *Manager
	actMu  sync.Mutex

	// chordHeld + heldVK implement fire-on-first-down latching (guarded by actMu).
	// Holding a chord generates auto-repeat WM_KEYDOWNs (~30/sec); without a latch
	// each one would dispatch another OpenOverlay (overlay teardown+refreeze storm).
	// On the first matched down we set chordHeld=true + remember the pressed VK; the
	// repeats are still swallowed (return 1) but not re-dispatched. We clear the
	// latch when that key's WM_KEYUP/WM_SYSKEYUP arrives, re-arming the next
	// real press.
	chordHeld bool
	heldVK    uint32

	// escHeld is the fire-on-first-down latch for the global Escape-to-cancel: a
	// HELD Escape auto-repeats (~30/sec) and we want exactly ONE Cancel per press,
	// not a flood. Set on the first armed Escape down, cleared on Escape up. Guarded
	// by actMu (same as chordHeld), independent of the chord latch so a held
	// Win+Shift+S and a tapped Escape never suppress each other.
	escHeld bool

	// mouseSwallowUp[vk] is set when the mouse hook swallowed a matched button
	// down, so it swallows that button's up too (an unpaired Mouse4 up would
	// still navigate Back in some apps).
	mouseSwallowUp [8]atomic.Bool

	// overOwnWindow reports whether the screen point is over one of Toru's own
	// windows (hook_windows.go). A mouse-button hotkey is ignored there so
	// clicking Toru's UI with that button doesn't trigger it.
	overOwnWindow = func(x, y int32) bool { return false }
)

// keyEvent is the part of KBDLLHOOKSTRUCT the engine reads.
type keyEvent struct {
	vk, scan, flags uint32
	extra           uintptr
	down            bool
}

// mouseEvent is a button event from the mouse hook: vk is VK_MBUTTON,
// VK_XBUTTON1 or VK_XBUTTON2 (other messages never reach handleMouse).
type mouseEvent struct {
	vk    uint32
	down  bool
	extra uintptr
	x, y  int32
}

// handleKey decides one keyboard event. It reports whether to swallow it.
// It MUST stay fast and IO-free (Windows silently drops a slow LL hook) and
// never calls into Wails: a match is a non-blocking send to m.sig.
func handleKey(ev keyEvent) bool {
	vk := sidedVK(ev.vk, ev.scan, ev.flags)

	// Input Toru injected (the auto clicker) is never a hotkey, never recorded
	// and never counts as physically held. This is the first check, before
	// recording, latches and Escape. LLKHF_INJECTED is deliberately NOT
	// filtered: AutoHotkey / Stream Deck users fire Toru hotkeys that way.
	if ev.extra == inputtag.Toru {
		if ev.down {
			phys.noteInjected(vk)
		}
		return false
	}
	phys.note(vk, ev.down)

	if recordKey(vk, ev.down) {
		if ev.down {
			phys.noteSwallowed(vk)
		}
		return true
	}

	// Key-up of the latched key clears the held latch so the next real press
	// fires again. Never swallowed: the up is harmless and the OS may need it.
	if !ev.down {
		actMu.Lock()
		if chordHeld && vk == heldVK {
			chordHeld = false
			heldVK = 0
		}
		if vk == vkEscape {
			escHeld = false // re-arm the next Escape press
		}
		actMu.Unlock()
		return false
	}

	actMu.Lock()
	m := active
	actMu.Unlock()

	// Global Escape-to-cancel while the capture overlay is armed. This exists
	// BECAUSE the in-page DOM Esc handler can miss: the overlay is a transparent,
	// frameless, always-on-top window that may not hold WebView2 keyboard focus
	// (clicks hit-test via the DOM without granting keyboard focus, and
	// SetForegroundWindow can fail over a fullscreen app) — the same reason Toru
	// uses an LL hook for Win+Shift+S instead of RegisterHotKey. We dispatch
	// Cancel off the hook thread and ALWAYS chain on (never swallow): Escape has
	// no harmful default while the fullscreen overlay is up, and not swallowing
	// means a stuck-armed flag can only cause a harmless extra Cancel, never break
	// Escape system-wide. escHeld coalesces auto-repeat into one Cancel per press.
	if vk == vkEscape {
		if m != nil && m.escapeArmed() {
			actMu.Lock()
			first := !escHeld
			if first {
				escHeld = true
			}
			actMu.Unlock()
			if first {
				m.signal(escapeAction)
			}
		}
		return false
	}

	if m == nil {
		return false
	}
	matched := m.matchPress(vk, false)
	if matched == "" {
		return false
	}

	// Fire-on-first-down: only dispatch on the FIRST matched key-down.
	// Auto-repeat key-downs (holding the chord, ~30/sec) are still swallowed
	// (so Snipping Tool never sees them) but NOT re-dispatched — otherwise each
	// repeat would trigger another OpenOverlay. A different latched key means a
	// distinct press, so distinct chords don't suppress each other.
	actMu.Lock()
	firstDown := !chordHeld || heldVK != vk
	if firstDown {
		chordHeld = true
		heldVK = vk
	}
	actMu.Unlock()
	if firstDown {
		m.signal(matched)
	}

	// Swallow every matched down (first + repeats) so the OS snip (and any
	// other consumer) never sees it. A modifier used as the key (RCtrl alone)
	// is the exception: swallowing it would break that modifier everywhere.
	if isModifierVK(vk) {
		return false
	}
	phys.noteSwallowed(vk)
	return true
}

// handleMouse decides one Middle/Mouse4/Mouse5 button event. It reports
// whether to swallow it. Moves, wheel and left/right never get here.
func handleMouse(ev mouseEvent) bool {
	if ev.extra == inputtag.Toru {
		if ev.down {
			phys.noteInjected(ev.vk)
		}
		return false
	}
	phys.note(ev.vk, ev.down)

	if recordMouse(ev.vk, ev.down) {
		if ev.down {
			phys.noteSwallowed(ev.vk)
		}
		return true
	}

	if !ev.down {
		// Swallow the up of a down we swallowed, and only that.
		return mouseSwallowUp[ev.vk].Swap(false)
	}

	actMu.Lock()
	m := active
	actMu.Unlock()
	if m == nil {
		return false
	}
	matched := m.matchPress(ev.vk, true)
	if matched == "" || overOwnWindow(ev.x, ev.y) {
		return false
	}
	m.signal(matched) // buttons don't auto-repeat: every matched down fires
	mouseSwallowUp[ev.vk].Store(true)
	phys.noteSwallowed(ev.vk)
	return true
}

// matchPress returns the action whose binding the press of vk completes, or
// "". Candidates (active, not owned by RegisterHotKey, vk among their keys)
// are snapshotted under m.mu; the key-state reads happen after the lock is
// released, so no user32 call runs under the lock the rest of the app shares.
// mouseOnly limits candidates to bindings that use a mouse button (the mouse
// hook's case; a keyboard event can complete a chord that includes a held
// mouse button too).
func (m *Manager) matchPress(vk uint32, mouseOnly bool) string {
	type cand struct {
		action string
		b      Binding
	}
	var cands []cand
	m.mu.Lock()
	for action, b := range m.bindings {
		if b.Key == 0 || m.inactive[action] || m.sysOwned[action] {
			continue
		}
		if mouseOnly && !usesMouse(b) {
			continue
		}
		if !slices.Contains(chordVKs(b), vk) {
			continue
		}
		cands = append(cands, cand{action, b})
	}
	m.mu.Unlock()
	if len(cands) == 0 {
		return ""
	}
	slices.SortFunc(cands, func(a, b cand) int { return specificity(b.b) - specificity(a.b) })

	state := phys.modState(vk)
	isDown := func(v uint32) bool { return phys.isDown(v) }
	for _, c := range cands {
		if firesOnPress(c.b, vk, isDown, state) {
			return c.action
		}
	}
	return ""
}

// signal hands action to the dispatch goroutine without ever blocking the
// hook proc (dropped if the goroutine is backed up).
func (m *Manager) signal(action string) {
	select {
	case m.sig <- action:
	default:
	}
}

// wantsMouseHook reports whether the mouse hook should be installed: some
// active binding uses a mouse button, or a recording is waiting (so the user
// can record Mouse4).
func (m *Manager) wantsMouseHook() bool {
	if recording() {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for action, b := range m.bindings {
		if !m.inactive[action] && usesMouse(b) {
			return true
		}
	}
	return false
}
