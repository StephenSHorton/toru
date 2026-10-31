//go:build windows

package capture

import "testing"

func TestMonitorHandleFindsEachScreen(t *testing.T) {
	screens := enumScreens()
	if len(screens) == 0 {
		t.Skip("no displays")
	}
	seen := map[uintptr]bool{}
	for _, s := range screens {
		h := monitorHandle(s)
		if h == 0 {
			t.Fatalf("no monitor at the centre of screen %+v", s)
		}
		if seen[h] {
			t.Fatalf("two screens gave the same monitor %#x", h)
		}
		seen[h] = true
	}
	if h := monitorHandle(ScreenInfo{X: -1 << 20, Y: -1 << 20, W: 2, H: 2}); h != 0 {
		t.Fatalf("a point on no monitor gave %#x", h)
	}
}
