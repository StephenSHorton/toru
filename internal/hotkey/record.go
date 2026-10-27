package hotkey

import (
	"slices"
	"time"
)

// Shortcut recording state (guarded by actMu). recCh is non-nil while a
// Record is waiting. Every key pressed during a recording is swallowed, and
// its key-up too (recSwallow), even if that arrives after the recording ended,
// so the OS never sees half a chord (a lone Win key-up would open Start).
//
// Keys are gathered into a chord: each key (or Middle/Mouse4/Mouse5 button)
// pressed within chordGather of the previous one joins it while at least one
// is still held. The recording ends with the largest set that was held at
// once when every key is released, when chordGather passes with no new key,
// or at the fifth key. Modifiers are recorded as generic Ctrl/Alt/Shift/Win
// (the Shortcuts UI narrows them to one side); pressing and releasing only
// modifiers records them as keys (a modifier-only shortcut like RCtrl).
var (
	recCh      chan recordResult
	recMods    Binding
	recSwallow = map[uint32]bool{}

	recHeld     []rune   // chord keys down right now
	recBest     []rune   // largest set of chord keys held at once
	recBestMods Binding  // modifiers held when recBest was taken
	recModPeak  SideMask // sided modifiers pressed since all were last up
	recModsDown SideMask // sided modifiers down right now
	recTimer    *time.Timer
	recGen      int // bumps per recording so a stale timer can't end a new one
)

// chordGather is how long a recording waits for the next chord key.
const chordGather = 600 * time.Millisecond

// maxChordKeys is the most keys a shortcut can hold (Key + 4 Extra).
const maxChordKeys = 5

// startRecordLocked resets the recording state and opens a new recording.
// Caller holds actMu.
func startRecordLocked() chan recordResult {
	endRecordLocked(recordResult{}) // a second Record replaces the first
	recCh = make(chan recordResult, 1)
	recMods = Binding{}
	recHeld, recBest, recBestMods = nil, nil, Binding{}
	recModPeak, recModsDown = 0, 0
	recGen++
	requestSync() // the mouse hook is wanted while recording
	return recCh
}

func endRecord() {
	actMu.Lock()
	endRecordLocked(recordResult{})
	actMu.Unlock()
}

func endRecordLocked(r recordResult) {
	if recTimer != nil {
		recTimer.Stop()
		recTimer = nil
	}
	if recCh != nil {
		recCh <- r
		recCh = nil
		requestSync()
	}
}

// recording reports whether a Record is waiting.
func recording() bool {
	actMu.Lock()
	defer actMu.Unlock()
	return recCh != nil
}

// recordNeedsMouseHook reports whether recording still needs the mouse hook:
// a Record is waiting, or a recorded Middle/Mouse4/Mouse5 is still held and
// its up must be swallowed. A recording that ends on the gather timeout with
// Mouse4 held would otherwise unhook before the up, and that unpaired up would
// reach the focused window (Toru's own WebView navigates Back on it).
func recordNeedsMouseHook() bool {
	actMu.Lock()
	defer actMu.Unlock()
	return recCh != nil || mouseUpPendingLocked()
}

// mouseUpPendingLocked reports whether a recorded mouse button's up is still
// owed a swallow. Caller holds actMu.
func mouseUpPendingLocked() bool {
	return recSwallow[vkMButton] || recSwallow[vkXButton1] || recSwallow[vkXButton2]
}

// commitChordLocked ends the recording with the gathered chord.
func commitChordLocked() {
	if len(recBest) == 0 {
		return
	}
	b := recBestMods
	b.Key = recBest[0]
	copy(b.Extra[:], recBest[1:])
	endRecordLocked(recordResult{b: canonical(b), ok: true})
}

// recordKey feeds one key event (vk sided, or a mouse button) to an active
// recording. It reports whether the event was consumed (and must be swallowed).
func recordKey(vk uint32, down bool) bool {
	actMu.Lock()
	defer actMu.Unlock()
	if !down {
		if !recSwallow[vk] {
			return false
		}
		delete(recSwallow, vk)
		if recCh != nil {
			recordUpLocked(vk)
		}
		return true
	}
	if recCh == nil {
		return false
	}
	recSwallow[vk] = true
	switch {
	case isModifierVK(vk):
		applyModifierVK(&recMods, vk, true)
		bit := sideBit(sidedVK(vk, 0, 0))
		recModsDown |= bit
		recModPeak |= bit
	case vk == vkEscape:
		endRecordLocked(recordResult{})
	default:
		if key, ok := keyForVK(vk); ok {
			addChordKeyLocked(key)
		}
		// Unsupported keys (Tab, Enter, CapsLock, …) are ignored; keep waiting.
	}
	return true
}

// recordUpLocked handles a key-up during a recording.
func recordUpLocked(vk uint32) {
	if isModifierVK(vk) {
		applyModifierVK(&recMods, vk, false)
		recModsDown &^= sideBit(sidedVK(vk, 0, 0))
		if recModsDown == 0 && len(recBest) == 0 {
			commitModifiersLocked()
		}
		return
	}
	if key, ok := keyForVK(vk); ok {
		releaseChordKeyLocked(key)
	}
}

// addChordKeyLocked adds a pressed key to the chord being recorded.
func addChordKeyLocked(key rune) {
	if slices.Contains(recHeld, key) {
		return // auto-repeat
	}
	recHeld = append(recHeld, key)
	if len(recHeld) > len(recBest) {
		recBest = slices.Clone(recHeld)
		recBestMods = recMods
	}
	if len(recBest) >= maxChordKeys {
		commitChordLocked()
		return
	}
	// Restart the gather window: no new key within it ends the chord.
	if recTimer != nil {
		recTimer.Stop()
	}
	gen := recGen
	recTimer = time.AfterFunc(chordGather, func() {
		actMu.Lock()
		defer actMu.Unlock()
		if recCh != nil && recGen == gen {
			commitChordLocked()
		}
	})
}

// releaseChordKeyLocked handles a chord key's release; releasing the last
// held key ends the recording.
func releaseChordKeyLocked(key rune) {
	recHeld = slices.DeleteFunc(recHeld, func(r rune) bool { return r == key })
	if len(recHeld) == 0 {
		commitChordLocked()
	}
}

// commitModifiersLocked ends the recording when only modifiers were pressed:
// the sided Ctrl/Shift/Alt keys become the shortcut's keys (RCtrl alone,
// LCtrl + LShift); a Win key among them stays a modifier. Win alone isn't a
// shortcut, so that keeps waiting.
func commitModifiersLocked() {
	peak := recModPeak
	recModPeak = 0
	var keys []rune
	for i, vk := range sideVKs {
		if peak&(SideMask(1)<<i) != 0 && vk != vkLWin && vk != vkRWin {
			keys = append(keys, namedKeyBase+rune(vk))
		}
	}
	if len(keys) == 0 || len(keys) > maxChordKeys {
		return
	}
	b := Binding{Win: peak&groupWin != 0, Key: keys[0]}
	copy(b.Extra[:], keys[1:])
	endRecordLocked(recordResult{b: canonical(b), ok: true})
}

// recordMouse feeds a Middle/Mouse4/Mouse5 event to an active recording. It
// reports whether the event was consumed (and must be swallowed).
func recordMouse(vk uint32, down bool) bool {
	actMu.Lock()
	defer actMu.Unlock()
	key := namedKeyBase + rune(vk)
	if !down {
		if !recSwallow[vk] {
			return false
		}
		delete(recSwallow, vk)
		if recCh != nil {
			releaseChordKeyLocked(key)
		} else if !mouseUpPendingLocked() {
			requestSync() // the last owed up is in: the hook may go now
		}
		return true
	}
	if recCh == nil {
		return false
	}
	recSwallow[vk] = true
	addChordKeyLocked(key)
	return true
}
