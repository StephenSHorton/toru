package overlay

import (
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

// TestArmEscapeNilSafe: with no armer injected, the toggle is a no-op (tests /
// non-Windows wiring skip the hotkey engine).
func TestArmEscapeNilSafe(t *testing.T) {
	s := NewService(nil)
	s.armEscape(true)
	s.armEscape(false)
}

// TestDisarmOnExitPaths is the safety net: every path that LEAVES capture mode
// must disarm the global Escape hook, or Escape would keep firing Cancel during
// annotation / after dismiss. BeginSession (the lone arm site) needs a live Wails
// app + screens so it is covered by manual verification, not here; these exercise
// the disarm side, which is what a stuck-armed flag would depend on.
func TestDisarmOnExitPaths(t *testing.T) {
	cases := []struct {
		name string
		call func(s *OverlayService)
	}{
		{"HideOverlay", func(s *OverlayService) { s.HideOverlay() }},
		{"Teardown", func(s *OverlayService) { s.Teardown() }},
		{"EnterEdit", func(s *OverlayService) { _ = s.EnterEdit(0, capture.Rect{}, 0, 0, 0, 0) }},
		{"EnterEditLive", func(s *OverlayService) { _ = s.EnterEditLive(0, capture.Rect{}, 0, 0, 0, 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []bool
			s := NewService(nil)
			s.SetEscapeArmer(func(on bool) { calls = append(calls, on) })
			tc.call(s)
			if len(calls) == 0 {
				t.Fatalf("%s: escArmer was never called", tc.name)
			}
			if last := calls[len(calls)-1]; last != false {
				t.Fatalf("%s: expected final escArmer(false), got %v", tc.name, calls)
			}
		})
	}
}
