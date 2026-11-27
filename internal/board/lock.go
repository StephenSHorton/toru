package board

import (
	"fmt"
	"slices"
)

// Locked items (whiteboard#29). The person locks a block, frame, line or
// stroke in the board window's properties panel to keep the layout as it
// is. Then no agent can move, resize or delete it, or unlock it: the
// change is refused with an error that says it is locked. The person
// can still change it, and unlock it, in the window. Its text and style
// stay open to everyone. Undo and redo put back earlier states exactly
// and aren't held.

// agentEdit reports whether the change being applied is an agent's own
// (not an undo or redo), the kind a lock holds against.
func (a *applier) agentEdit() bool { return !a.restore && a.src.IsAgent() }

// lockedDelete refuses an agent's removal of a locked item.
func (a *applier) lockedDelete(id string) error {
	if !a.agentEdit() {
		return nil
	}
	kind := a.kinds[id]
	if lockedOn(a.b, id) {
		return lockedErr(kind, id, "delete")
	}
	return nil
}

// lockedOn reports whether id is a locked item on b.
func lockedOn(b Board, id string) bool {
	has := func(locked bool, x string) bool { return locked && x == id }
	for _, x := range b.Blocks {
		if has(x.Locked, x.ID) {
			return true
		}
	}
	for _, x := range b.Frames {
		if has(x.Locked, x.ID) {
			return true
		}
	}
	for _, x := range b.Ink {
		if has(x.Locked, x.ID) {
			return true
		}
	}
	for _, x := range b.Links {
		if has(x.Locked, x.ID) {
			return true
		}
	}
	return false
}

func lockedErr(kind, id, what string) error {
	name := map[string]string{"link": "line", "stroke": "ink stroke"}[kind]
	if name == "" {
		name = kind
	}
	return fmt.Errorf("%s %q is locked, so it can't be %s: only the person can unlock it, in the board window",
		name, id, map[string]string{"move": "moved", "resize": "resized", "delete": "deleted", "unlock": "unlocked"}[what])
}

// heldBox is held for an item with a box: a locked one can't move, resize
// or be unlocked.
func heldBox(wasLocked, locked bool, x, y, w, h, nx, ny, nw, nh float64) string {
	switch {
	case !wasLocked:
		return ""
	case !locked:
		return "unlock"
	case nw != w || nh != h:
		return "resize"
	case nx != x || ny != y:
		return "move"
	}
	return ""
}

// heldStroke is held for ink: its points are where it is and how big.
func heldStroke(o, n *Stroke) string {
	if why := heldBox(o.Locked, n.Locked, 0, 0, 0, 0, 0, 0, 0, 0); why != "" {
		return why
	}
	if o.Locked && !slices.Equal(o.Points, n.Points) {
		return "move"
	}
	return ""
}
