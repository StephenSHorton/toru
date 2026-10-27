package overlay

// S0 placeholder: S4 replaces this file with the real implementation.

import "errors"

// ShowSafety draws the failsafe preview; persistent=false fades it out after ~3 s.
func ShowSafety(s SafetyScene, persistent bool) {}

// ShowPoints draws click points; persistent=false fades them out after ~5 s.
func ShowPoints(pts []ScenePoint, persistent bool) {}

// Hide removes the preview layers (not the indicator).
func Hide() {}

// StartPick starts a point or zone pick with its own low-level hooks; the scene
// and points are drawn underneath. cancel ends it (OnEnd(true)).
func StartPick(kind PickKind, s SafetyScene, pts []ScenePoint, cb PickCallbacks) (cancel func(), err error) {
	return func() {}, errors.New("overlay: not built yet")
}

// UpdatePickPoints redraws the points during an active point pick.
func UpdatePickPoints(pts []ScenePoint) {}

// IndicatorShow shows or updates the running pill with text.
func IndicatorShow(text string) {}

// IndicatorHide hides the running pill.
func IndicatorHide() {}

// Shutdown destroys every overlay window and unhooks an active pick.
func Shutdown() {}
