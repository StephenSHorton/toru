package hotkey

import (
	"fmt"
	"unicode"
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
	Key    string `json:"key"` // single uppercase char "A".."Z" or "0".."9"
}

// HotkeyService is the Wails-bound API the React Shortcuts panel calls. It is a
// thin, cross-platform facade over the Manager (which owns the live LL hook) plus
// settings.json persistence.
//
// JS binding name: HotkeyService.*
type HotkeyService struct {
	mgr *Manager
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
		out = append(out, bindingToShortcut(r.Action, r.Label, r.B))
	}
	return out
}

// SetShortcut validates sc, persists it to settings.json, then updates the live
// hook binding (no reinstall — the hook reads the mutex-guarded binding list). It
// returns a validation/persist error to the frontend, which surfaces it inline.
func (s *HotkeyService) SetShortcut(action string, sc Shortcut) error {
	b, err := shortcutToBinding(sc)
	if err != nil {
		return err
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

// bindingToShortcut builds the wire Shortcut for an action + label.
func bindingToShortcut(action, label string, b Binding) Shortcut {
	key := ""
	if b.Key != 0 {
		key = string(unicode.ToUpper(b.Key))
	}
	return Shortcut{
		Action: action,
		Label:  label,
		Ctrl:   b.Ctrl,
		Shift:  b.Shift,
		Alt:    b.Alt,
		Win:    b.Win,
		Key:    key,
	}
}

// shortcutToBinding validates sc and converts it to an internal Binding.
func shortcutToBinding(sc Shortcut) (Binding, error) {
	if err := validateShortcut(sc); err != nil {
		return Binding{}, err
	}
	key := unicode.ToUpper([]rune(sc.Key)[0])
	return Binding{
		Ctrl:  sc.Ctrl,
		Shift: sc.Shift,
		Alt:   sc.Alt,
		Win:   sc.Win,
		Key:   key,
	}, nil
}

// validateShortcut enforces the v1 rules: at least one modifier (a bare key would
// fire constantly), and a single trigger key in A-Z or 0-9.
func validateShortcut(sc Shortcut) error {
	if !sc.Ctrl && !sc.Shift && !sc.Alt && !sc.Win {
		return fmt.Errorf("shortcut needs at least one modifier")
	}
	rs := []rune(sc.Key)
	if len(rs) != 1 || !validKeyRune(rs[0]) {
		return fmt.Errorf("shortcut key must be a single letter A-Z or digit 0-9")
	}
	return nil
}
