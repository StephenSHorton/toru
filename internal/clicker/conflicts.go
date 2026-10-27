package clicker

import (
	"github.com/StephenSHorton/toru/internal/hotkey"
)

// Hotkey action ids the clicker registers on the shared Manager (main.go).
const (
	actionToggle = "clickerToggle"
	actionArm    = "clickerArm"
)

// mouseHotkeyName is the hotkey.KeyName spelling of each mouse button that can
// be a hotkey (Left/Right never are).
var mouseHotkeyName = map[MouseButton]string{
	ButtonMiddle: "MouseMiddle",
	ButtonX1:     "Mouse4",
	ButtonX2:     "Mouse5",
}

// checkConflicts refuses a run whose own input would fire a Toru hotkey: a
// RegisterHotKey action cannot tell injected keys from real ones, and a matched
// combo is swallowed, so the run would trigger (or stop) itself. Checked:
//   - the auto-press key, and Shift+key when the run wraps it in Shift
//     (uppercase, or Caps Lock on), against every active binding;
//   - the clicked mouse button against a mouse-button clickerToggle.
func checkConflicts(p Profile, keys bindingSource, capsOn bool) error {
	if keys == nil {
		return nil
	}
	if p.Input == InputKeyboard {
		r, ok := hotkey.ParseKeyName(p.Key)
		if !ok {
			return nil // Start reports the unknown key itself
		}
		if action, hit := keys.Conflicts(hotkey.Binding{Key: r}); hit {
			return conflictErr(p.Key, action)
		}
		if isLetterKey(p.Key) && (p.Uppercase || capsOn) {
			if action, hit := keys.Conflicts(hotkey.Binding{Shift: true, Key: r}); hit {
				return conflictErr("Shift + "+p.Key, action)
			}
		}
		return nil
	}
	name, ok := mouseHotkeyName[p.Button]
	if !ok {
		return nil
	}
	b, ok := keys.Binding(actionToggle)
	if !ok {
		return nil
	}
	if hotkey.KeyName(b.Key) == name {
		return clickerErrf(ErrHotkeyConflict,
			"The auto clicker's start / stop shortcut uses %s, so clicking it would stop the run. Pick another button or shortcut", name)
	}
	for _, x := range b.Extra {
		if x != 0 && hotkey.KeyName(x) == name {
			return clickerErrf(ErrHotkeyConflict,
				"The auto clicker's start / stop shortcut uses %s, so clicking it would stop the run. Pick another button or shortcut", name)
		}
	}
	return nil
}

func conflictErr(combo, action string) error {
	return clickerErrf(ErrHotkeyConflict,
		"Pressing %s would trigger the shortcut %q. Pick another key or change that shortcut", combo, action)
}

// bindingLabel renders b for the page and tray: "Ctrl + Shift + Y",
// "LCtrl + Shift + A + S". "" when unbound.
func bindingLabel(b hotkey.Binding) string { return hotkey.ComboLabel(b) }
