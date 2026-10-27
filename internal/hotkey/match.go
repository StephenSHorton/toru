package hotkey

import "slices"

// Binding matching, shared by the keyboard hook, the mouse hook, Conflicts and
// SetShortcut's duplicate check so they can never disagree. Pure: the callers
// pass in which keys are down.
//
// Semantics:
//   - The keys of a binding are Key plus its Extra keys (a chord). It fires on
//     the key-down that completes the chord: the pressed key is one of them and
//     the others are already held, in any order.
//   - A required modifier with no side bit is satisfied by either side; with a
//     side bit, only by that side.
//   - Exact (the default): no other modifier may be down, including the other
//     side of a sided one. Loose: unlisted modifiers are ignored.
//   - A sided modifier that is itself one of the binding's keys (RCtrl alone,
//     LCtrl + LShift) is a key, not a modifier, so it never counts as extra.

// chordKeys returns b's keys: Key, then the used Extra slots. Empty when unbound.
func chordKeys(b Binding) []rune {
	if b.Key == 0 {
		return nil
	}
	ks := []rune{b.Key}
	for _, r := range b.Extra {
		if r != 0 {
			ks = append(ks, r)
		}
	}
	return ks
}

// chordVKs is chordKeys mapped to virtual-key codes.
func chordVKs(b Binding) []uint32 {
	ks := chordKeys(b)
	vks := make([]uint32, len(ks))
	for i, k := range ks {
		vks[i] = triggerVK(k)
	}
	return vks
}

// isPlain reports whether b is an old-style binding (generic modifiers + one
// key, exact), the kind Toru always had.
func isPlain(b Binding) bool {
	return b.Sides == 0 && b.Extra == [4]rune{} && !b.Loose
}

// hookOnly reports whether b can only be matched by the low-level hooks:
// RegisterHotKey can't express sides, chords, loose matching, mouse buttons
// or a modifier used as the key. Such bindings don't fire while an elevated
// window has focus (Windows doesn't call an unelevated process's hooks then).
func hookOnly(b Binding) bool {
	if !isPlain(b) {
		return true
	}
	vk := triggerVK(b.Key)
	return isMouseVK(vk) || isModifierVK(vk)
}

// usesMouse reports whether any of b's keys is a mouse button.
func usesMouse(b Binding) bool {
	return slices.ContainsFunc(chordVKs(b), isMouseVK)
}

// canonical sorts b's keys ascending (Key = smallest, then Extra) so the same
// chord pressed in any order compares equal with ==.
func canonical(b Binding) Binding {
	ks := chordKeys(b)
	slices.Sort(ks)
	b.Extra = [4]rune{}
	if len(ks) == 0 {
		b.Key = 0
		return b
	}
	b.Key = ks[0]
	copy(b.Extra[:], ks[1:])
	return b
}

// modsMatch reports whether the modifier state (one SideMask bit per sided
// modifier that is down) satisfies b's modifiers.
func modsMatch(b Binding, state SideMask) bool {
	for _, vk := range chordVKs(b) {
		state &^= sideBit(vk)
	}
	groups := [4]struct {
		mask SideMask
		need bool
	}{{groupCtrl, b.Ctrl}, {groupShift, b.Shift}, {groupAlt, b.Alt}, {groupWin, b.Win}}
	for _, g := range groups {
		down := state & g.mask
		if !g.need {
			if !b.Loose && down != 0 {
				return false
			}
			continue
		}
		sides := b.Sides & g.mask
		if sides == 0 {
			if down == 0 {
				return false
			}
			continue
		}
		if down&sides != sides || (!b.Loose && down&^sides != 0) {
			return false
		}
	}
	return true
}

// firesOnPress reports whether b fires when pressed (a key that is one of
// b's keys goes down), given which other keys are down and the modifier
// state.
func firesOnPress(b Binding, pressed uint32, isDown func(vk uint32) bool, state SideMask) bool {
	vks := chordVKs(b)
	if !slices.Contains(vks, pressed) {
		return false
	}
	for _, vk := range vks {
		if vk != pressed && !isDown(vk) {
			return false
		}
	}
	return modsMatch(b, state)
}

// firesOn reports whether binding x would fire while the user presses
// combo b: every key of x is among b's keys and x's modifiers accept some way
// of holding b's modifiers (a generic Ctrl in b may be either side).
func firesOn(x, b Binding) bool {
	xv, bv := chordVKs(x), chordVKs(b)
	if len(xv) == 0 || len(bv) == 0 {
		return false
	}
	for _, vk := range xv {
		if !slices.Contains(bv, vk) {
			return false
		}
	}
	var base SideMask
	for _, vk := range bv {
		base |= sideBit(vk) // b's own sided-modifier keys are held too
	}
	// Each required modifier group of b is held on its required side(s), or,
	// when b names no side, on the left or on the right: try every mix.
	type choice struct{ left, right SideMask }
	var opts []choice
	for _, g := range []struct {
		mask SideMask
		need bool
	}{{groupCtrl, b.Ctrl}, {groupShift, b.Shift}, {groupAlt, b.Alt}, {groupWin, b.Win}} {
		if !g.need {
			continue
		}
		if s := b.Sides & g.mask; s != 0 {
			base |= s
			continue
		}
		left := g.mask & (SideLCtrl | SideLShift | SideLAlt | SideLWin)
		opts = append(opts, choice{left: left, right: g.mask &^ left})
	}
	for n := 0; n < 1<<len(opts); n++ {
		state := base
		for i, o := range opts {
			if n&(1<<i) != 0 {
				state |= o.right
			} else {
				state |= o.left
			}
		}
		if modsMatch(x, state) {
			return true
		}
	}
	return false
}

// specificity orders candidate matches: more keys first, then exact before
// loose, so the most specific binding wins when two could fire.
func specificity(b Binding) int {
	n := len(chordKeys(b)) * 2
	if !b.Loose {
		n++
	}
	return n
}
