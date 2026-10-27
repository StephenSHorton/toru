package hotkey

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// Shortcut is the binding-friendly wire shape for the frontend. Key is a STRING
// ("S") so the TS side stays clean; it converts to/from the internal rune Binding
// in Go. JSON tags keep the generated TS field names stable + lowercase.
//
// JS binding name: HotkeyService.* (see HotkeyService below).
type Shortcut struct {
	Action string `json:"action"` // stable id, e.g. "overlay"
	Label  string `json:"label"`  // human label, e.g. "Open capture overlay"
	Ctrl   bool   `json:"ctrl"`
	Shift  bool   `json:"shift"`
	Alt    bool   `json:"alt"`
	Win    bool   `json:"win"`
	Key    string `json:"key"` // "A".."Z", "0".."9", "F1".."F24" or a named key ("PageUp", "Num5", "[", "Mouse4"); "" = unbound
	// Sides narrows modifiers to one side: any of "lctrl", "rctrl", "lshift",
	// "rshift", "lalt", "ralt", "lwin", "rwin" (the generic flag must be set too).
	Sides []string `json:"sides"`
	// Extra are more chord keys held with Key (KeyName spellings, up to 4).
	Extra []string `json:"extra"`
	// Loose: fire even when other modifiers are held too.
	Loose bool `json:"loose"`
	// IsDefault is true when the combo is the action's default (output only).
	IsDefault bool `json:"isDefault"`
}

// HotkeyService is the Wails-bound API the React Shortcuts panel calls. It is a
// thin, cross-platform facade over the Manager (which owns the live LL hook) plus
// settings.json persistence.
//
// JS binding name: HotkeyService.*
type HotkeyService struct {
	mgr *Manager

	mu            sync.Mutex
	onRecordStart func() // run when RecordShortcut begins (stops the auto clicker)
}

// NewService wraps mgr. This is a plain Go constructor (NOT a bound method), so
// the *Manager param is fine — the binding generator only binds the methods on
// the registered service struct.
func NewService(mgr *Manager) *HotkeyService { return &HotkeyService{mgr: mgr} }

// GetShortcuts returns every configured action's current combo for the UI.
func (s *HotkeyService) GetShortcuts() []Shortcut {
	rows := s.mgr.snapshot()
	out := make([]Shortcut, 0, len(rows))
	for _, r := range rows {
		sc := bindingToShortcut(r.Action, r.Label, r.B)
		sc.IsDefault = r.B == r.Def
		out = append(out, sc)
	}
	return out
}

// SetShortcut validates sc, persists it to settings.json, then updates the live
// hook binding (no reinstall — the hook reads the mutex-guarded binding list). It
// returns a validation/persist error to the frontend, which surfaces it inline.
// A combo another action already uses, or one that would fire together with
// another action's (a chord containing it, a loose binding covering it), is
// refused.
func (s *HotkeyService) SetShortcut(action string, sc Shortcut) error {
	b, err := shortcutToBinding(sc)
	if err != nil {
		return err
	}
	for _, r := range s.mgr.snapshot() {
		if r.Action != action && r.B.Key != 0 && (r.B == b || firesOn(r.B, b) || firesOn(b, r.B)) {
			return fmt.Errorf("%s is already used for %q", comboLabel(b), r.Label)
		}
	}
	if err := persistBinding(action, b); err != nil {
		return fmt.Errorf("save shortcut: %w", err)
	}
	s.mgr.SetBinding(action, b)
	return nil
}

// ResetShortcut restores action to its registered default, persists it, and
// updates the live binding.
func (s *HotkeyService) ResetShortcut(action string) error {
	def, ok := s.mgr.defaultFor(action)
	if !ok {
		return fmt.Errorf("unknown action %q", action)
	}
	if err := persistBinding(action, def); err != nil {
		return fmt.Errorf("save shortcut: %w", err)
	}
	s.mgr.SetBinding(action, def)
	return nil
}

// RecordShortcut waits for the user to press a key combo anywhere (up to 30s)
// and returns it WITHOUT saving it; the frontend then calls SetShortcut. It
// listens in the keyboard hook itself, so combos Windows or Toru would
// otherwise consume (Win+…, PrintScreen, Toru's own hotkeys) are captured
// too, and nothing fires while recording. Keys pressed together form a chord
// (up to 5); the middle and side mouse buttons can be recorded too. Esc,
// CancelRecording or the timeout return an empty Key.
func (s *HotkeyService) RecordShortcut() (Shortcut, error) {
	s.mu.Lock()
	fn := s.onRecordStart
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
	b, ok, err := s.mgr.Record(30 * time.Second)
	if err != nil || !ok {
		return Shortcut{Sides: []string{}, Extra: []string{}}, err
	}
	return bindingToShortcut("", "", b), nil
}

// CancelRecording ends a pending RecordShortcut (it returns an empty Key).
func (s *HotkeyService) CancelRecording() { s.mgr.CancelRecord() }

// comboLabel renders b like the UI does: "Ctrl + Alt + F10",
// "LCtrl + Shift + A + S", "Mouse4". Empty when unbound.
func comboLabel(b Binding) string {
	if b.Key == 0 {
		return ""
	}
	var parts []string
	for _, g := range []struct {
		on    bool
		mask  SideMask
		label string
	}{{b.Win, groupWin, "Win"}, {b.Ctrl, groupCtrl, "Ctrl"}, {b.Alt, groupAlt, "Alt"}, {b.Shift, groupShift, "Shift"}} {
		if !g.on {
			continue
		}
		sides := b.Sides & g.mask
		if sides == 0 {
			parts = append(parts, g.label)
			continue
		}
		for i := range sideLabels {
			if sides&(SideMask(1)<<i) != 0 {
				parts = append(parts, sideLabels[i])
			}
		}
	}
	for _, k := range chordKeys(b) {
		parts = append(parts, KeyName(k))
	}
	return strings.Join(parts, " + ")
}

// sideList is the wire spelling of a SideMask (nil when empty).
func sideList(m SideMask) []string {
	var out []string
	for i, name := range sideNames {
		if m&(SideMask(1)<<i) != 0 {
			out = append(out, name)
		}
	}
	return out
}

// extraList is the wire spelling of b's Extra keys (nil when none).
func extraList(b Binding) []string {
	var out []string
	for _, r := range b.Extra {
		if r != 0 {
			out = append(out, KeyName(r))
		}
	}
	return out
}

// orEmpty keeps wire slices non-nil so the frontend always gets arrays.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// bindingToShortcut builds the wire Shortcut for an action + label.
func bindingToShortcut(action, label string, b Binding) Shortcut {
	return Shortcut{
		Action: action,
		Label:  label,
		Ctrl:   b.Ctrl,
		Shift:  b.Shift,
		Alt:    b.Alt,
		Win:    b.Win,
		Key:    KeyName(b.Key),
		Sides:  orEmpty(sideList(b.Sides)),
		Extra:  orEmpty(extraList(b)),
		Loose:  b.Loose,
	}
}

// shortcutToBinding validates sc and converts it to an internal Binding.
func shortcutToBinding(sc Shortcut) (Binding, error) {
	if err := validateShortcut(sc); err != nil {
		return Binding{}, err
	}
	return buildBinding(sc.Ctrl, sc.Shift, sc.Alt, sc.Win, sc.Key, sc.Sides, sc.Extra, sc.Loose)
}

// buildBinding parses the wire/persisted fields into a canonical Binding. A
// side implies its generic modifier. It checks only that every name parses
// and the chord fits; validateShortcut adds the usability rules.
func buildBinding(ctrl, shift, alt, win bool, key string, sides, extra []string, loose bool) (Binding, error) {
	k, ok := ParseKeyName(key)
	if !ok {
		return Binding{}, fmt.Errorf("that key can't be used for a shortcut")
	}
	b := Binding{Ctrl: ctrl, Shift: shift, Alt: alt, Win: win, Key: k, Loose: loose}
	for _, s := range sides {
		i := slices.Index(sideNames[:], strings.ToLower(strings.TrimSpace(s)))
		if i < 0 {
			return Binding{}, fmt.Errorf("%q isn't a modifier side", s)
		}
		b.Sides |= SideMask(1) << i
	}
	b.Ctrl = b.Ctrl || b.Sides&groupCtrl != 0
	b.Shift = b.Shift || b.Sides&groupShift != 0
	b.Alt = b.Alt || b.Sides&groupAlt != 0
	b.Win = b.Win || b.Sides&groupWin != 0
	if len(extra) > len(b.Extra) {
		return Binding{}, fmt.Errorf("a shortcut can have at most %d keys", maxChordKeys)
	}
	for i, name := range extra {
		r, ok := ParseKeyName(name)
		if !ok {
			return Binding{}, fmt.Errorf("%q can't be used for a shortcut", name)
		}
		b.Extra[i] = r
	}
	return canonical(b), nil
}

// validateShortcut enforces the rules: supported keys (at most 5, none
// twice), and at least one modifier unless one of the keys doesn't type text
// (F-keys, PrintScreen, numpad, PageUp, mouse buttons, …). A bare letter or
// Space would fire while typing, and so would a chord of only such keys.
func validateShortcut(sc Shortcut) error {
	b, err := buildBinding(sc.Ctrl, sc.Shift, sc.Alt, sc.Win, sc.Key, sc.Sides, sc.Extra, sc.Loose)
	if err != nil {
		return err
	}
	keys := chordKeys(b)
	seen := map[rune]bool{}
	for _, k := range keys {
		if seen[k] {
			return fmt.Errorf("%s is in the shortcut twice", KeyName(k))
		}
		seen[k] = true
		// A modifier can't be both held and a key (Ctrl + LCtrl).
		if bit := sideBit(triggerVK(k)); bit != 0 {
			if (bit&groupCtrl != 0 && b.Ctrl) || (bit&groupShift != 0 && b.Shift) || (bit&groupAlt != 0 && b.Alt) {
				return fmt.Errorf("%s is already a key in this shortcut", KeyName(k))
			}
		}
	}
	if !b.Ctrl && !b.Shift && !b.Alt && !b.Win && !slices.ContainsFunc(keys, bareAllowed) {
		if len(keys) == 1 {
			return fmt.Errorf("%s on its own would fire while you type; add Ctrl, Alt, Shift or Win", KeyName(b.Key))
		}
		return fmt.Errorf("%s would fire while you type; add Ctrl, Alt, Shift or Win, or a key that doesn't type", comboLabel(b))
	}
	return nil
}
