//go:build !windows

package overlay

import "errors"

// Toru only ships on Windows; these keep cross-compiles and tests building.

// ShowSafety is a no-op off Windows.
func ShowSafety(SafetyScene, bool) {}

// ShowPoints is a no-op off Windows.
func ShowPoints([]ScenePoint, bool) {}

// Hide is a no-op off Windows.
func Hide() {}

// StartPick always fails off Windows.
func StartPick(PickKind, SafetyScene, []ScenePoint, PickCallbacks) (cancel func(), err error) {
	return func() {}, errors.New("overlay: picking needs Windows")
}

// UpdatePickPoints is a no-op off Windows.
func UpdatePickPoints([]ScenePoint) {}

// IndicatorShow is a no-op off Windows.
func IndicatorShow(string) {}

// IndicatorHide is a no-op off Windows.
func IndicatorHide() {}

// Shutdown is a no-op off Windows.
func Shutdown() {}
