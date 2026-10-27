package hotkey

import "sync/atomic"

// Physical key state. The low-level hooks keep a table of which keys are down,
// fed only by input Toru didn't inject (events tagged with inputtag.Toru are
// skipped), so the auto clicker's own presses never count as the user's.
//
// GetAsyncKeyState is still the main source: it sees keys held before the hook
// was installed and key-ups that happened while an elevated window had focus
// (when Windows stops calling our hooks, so the table would go stale). The
// table is used for exactly the keys GetAsyncKeyState gets wrong:
//
//   - a key whose down our hook swallowed (a matched hotkey, a recorded key):
//     the swallowed down never reached the async state, so only the table
//     knows it is held, until that key's up;
//   - while a clicker run is injecting (SetInjecting(true)), a key Toru has
//     injected during the run: the async state mixes the user's presses with
//     the clicker's (an injected Shift down/up would hide a held physical
//     Shift), so only the table can tell them apart.
//
// Everything else reads GetAsyncKeyState, which also keeps plain bindings
// exactly as they matched before the table existed.
type keyTable struct {
	down      [256]atomic.Bool // physical (untagged) state, sided VKs
	swallowed [256]atomic.Bool // our hook swallowed the current down
	injected  [256]atomic.Bool // Toru injected this key during the current run
	injecting atomic.Bool      // a clicker run is injecting input
}

// phys is the process-wide table (Toru has one hook thread; see active).
var phys keyTable

// kbdHookUp / mouseHookUp report whether the keyboard / mouse hook is
// installed, i.e. whether the table is being fed for those keys at all.
var kbdHookUp, mouseHookUp atomic.Bool

// asyncKeyDown reads GetAsyncKeyState's down bit on Windows (set in
// hook_windows.go); tests replace it. Off Windows nothing is ever down.
var asyncKeyDown = func(vk uint32) bool { return false }

// note records a physical (untagged) key event. vk is already sided.
func (t *keyTable) note(vk uint32, down bool) {
	if vk > 0xFF {
		return
	}
	t.down[vk].Store(down)
	if !down {
		t.swallowed[vk].Store(false)
	}
}

// noteSwallowed marks vk's current down as swallowed by our hook.
func (t *keyTable) noteSwallowed(vk uint32) {
	if vk <= 0xFF {
		t.swallowed[vk].Store(true)
	}
}

// noteInjected records a tagged (Toru-injected) key-down for vk.
func (t *keyTable) noteInjected(vk uint32) {
	if vk <= 0xFF {
		t.injected[vk].Store(true)
	}
}

// setInjecting turns injecting mode on or off. Turning it off forgets which
// keys the run injected (the run has released them by then).
func (t *keyTable) setInjecting(on bool) {
	t.injecting.Store(on)
	if !on {
		for i := range t.injected {
			t.injected[i].Store(false)
		}
	}
}

// isDown is the hooks' answer to "is this sided vk held by the user?" (the
// rule in the type comment). Only call it for keys whose hook is installed.
func (t *keyTable) isDown(vk uint32) bool {
	if vk > 0xFF {
		return false
	}
	if t.swallowed[vk].Load() || (t.injecting.Load() && t.injected[vk].Load()) {
		return t.down[vk].Load()
	}
	return asyncKeyDown(vk)
}

// physicallyDown answers PhysicallyDown for one (sided or plain) vk: the table
// rule while the hook that sees vk is installed, GetAsyncKeyState otherwise
// (no hook, or a key no hook tracks, like the left/right mouse buttons).
func (t *keyTable) physicallyDown(vk uint32) bool {
	switch vk {
	case vkShift:
		return t.physicallyDown(vkLShift) || t.physicallyDown(vkRShift)
	case vkControl:
		return t.physicallyDown(vkLControl) || t.physicallyDown(vkRControl)
	case vkMenu:
		return t.physicallyDown(vkLMenu) || t.physicallyDown(vkRMenu)
	}
	hooked := kbdHookUp.Load()
	if vk < 0x07 { // mouse buttons
		hooked = isMouseVK(vk) && mouseHookUp.Load()
	}
	if !hooked {
		return asyncKeyDown(vk)
	}
	return t.isDown(vk)
}

// modState returns the sided modifiers that are down (one SideMask bit each),
// counting pressed (a modifier going down right now, which the async state
// doesn't show yet) as down.
func (t *keyTable) modState(pressed uint32) SideMask {
	var s SideMask
	for i, vk := range sideVKs {
		if vk == pressed || t.isDown(vk) {
			s |= SideMask(1) << i
		}
	}
	return s
}
