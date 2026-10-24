package hotkey

import (
	"fmt"
	"strings"
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
	Key    string `json:"key"` // "A".."Z", "0".."9", "F1".."F24" or a named key ("PageUp", "Num5", "[")
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
func (s *HotkeyService) SetShortcut(action string, sc Shortcut) error {
	b, err := shortcutToBinding(sc)
	if err != nil {
		return err
	}
	for _, r := range s.mgr.snapshot() {
		if r.Action != action && r.B == b {
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
// too, and nothing fires while recording. Esc, CancelRecording or the timeout
// return an empty Key.
func (s *HotkeyService) RecordShortcut() (Shortcut, error) {
	b, ok, err := s.mgr.Record(30 * time.Second)
	if err != nil || !ok {
		return Shortcut{}, err
	}
	return bindingToShortcut("", "", b), nil
}

// CancelRecording ends a pending RecordShortcut (it returns an empty Key).
func (s *HotkeyService) CancelRecording() { s.mgr.CancelRecord() }

// comboLabel renders b like the UI does: "Ctrl + Alt + F10".
func comboLabel(b Binding) string {
	var parts []string
	if b.Win {
		parts = append(parts, "Win")
	}
	if b.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if b.Alt {
		parts = append(parts, "Alt")
	}
	if b.Shift {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, KeyName(b.Key)), " + ")
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
	}
}

// shortcutToBinding validates sc and converts it to an internal Binding.
func shortcutToBinding(sc Shortcut) (Binding, error) {
	if err := validateShortcut(sc); err != nil {
		return Binding{}, err
	}
	key, _ := ParseKeyName(sc.Key)
	return Binding{
		Ctrl:  sc.Ctrl,
		Shift: sc.Shift,
		Alt:   sc.Alt,
		Win:   sc.Win,
		Key:   key,
	}, nil
}

// validateShortcut enforces the rules: a supported trigger key, and at least
// one modifier unless the key doesn't type text (F-keys, PrintScreen, numpad,
// PageUp, …). A bare letter or Space would fire while typing.
func validateShortcut(sc Shortcut) error {
	key, ok := ParseKeyName(sc.Key)
	if !ok {
		return fmt.Errorf("that key can't be used for a shortcut")
	}
	if !bareAllowed(key) && !sc.Ctrl && !sc.Shift && !sc.Alt && !sc.Win {
		return fmt.Errorf("%s on its own would fire while you type; add Ctrl, Alt, Shift or Win", KeyName(key))
	}
	return nil
}
