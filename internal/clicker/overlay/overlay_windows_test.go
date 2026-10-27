//go:build windows

package overlay

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestTextMask renders into a memory DC only; nothing reaches the screen.
func TestTextMask(t *testing.T) {
	m := textMask("Paused", pillTextSz, 600, 1)
	if m.W < 20 || m.H < 10 {
		t.Fatalf("mask too small: %dx%d", m.W, m.H)
	}
	solid := 0
	for _, a := range m.A {
		if a > 200 {
			solid++
		}
	}
	if solid == 0 {
		t.Fatal("no text coverage")
	}
	if big := textMask("Paused", pillTextSz, 600, 2); big.H <= m.H {
		t.Errorf("200 %% text should be taller: %d vs %d", big.H, m.H)
	}
	if e := textMask("", pillTextSz, 600, 1); e.W != 0 {
		t.Error("empty text should give an empty mask")
	}
}

// TestOverlaySmoke shows the real safety preview, the running pill and an
// immediately cancelled pick, and checks focus never moves. Opt-in
// (TORU_SMOKE=1): it draws on the desktop and installs low-level hooks for a
// moment, but injects no input.
func TestOverlaySmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	defer Shutdown()
	before := windows.GetForegroundWindow()
	focusKept := func(what string) {
		t.Helper()
		if fg := windows.GetForegroundWindow(); fg != before {
			t.Fatalf("%s stole focus: foreground %x -> %x", what, before, fg)
		}
	}

	mons := monitors()
	if len(mons) == 0 {
		t.Fatal("no monitors enumerated")
	}
	m := mons[0]
	ShowSafety(SafetyScene{
		Monitors:  mons,
		Corners:   [4]int32{50, 50, 50, 50},
		CornersOn: true,
		Edges:     [4]int32{30, 30, 30, 30},
		EdgesOn:   true,
		OuterOnly: true,
		Zones:     []SceneZone{{L: m.L + 200, T: m.T + 200, R: m.L + 500, B: m.T + 400, Action: ZoneStop}},
		ZonesOn:   true,
	}, false)
	for i := 0; i < 10; i++ {
		time.Sleep(50 * time.Millisecond)
		focusKept("safety preview")
	}
	c, ok := visibleCounts()
	if !ok || c.safety != len(mons) {
		t.Fatalf("safety layer: %+v ok=%v, want %d windows", c, ok, len(mons))
	}

	IndicatorShow("Clicking · 1,234 · Ctrl+Shift+Y to stop")
	time.Sleep(200 * time.Millisecond)
	IndicatorShow("Paused")
	time.Sleep(200 * time.Millisecond)
	focusKept("indicator")
	if c, _ := visibleCounts(); c.indicator != 1 {
		t.Fatalf("indicator not shown: %+v", c)
	}
	IndicatorHide()
	if c, _ := visibleCounts(); c.indicator != 0 {
		t.Fatalf("indicator not hidden: %+v", c)
	}

	// A pick that is cancelled at once: hooks in and out, OnEnd(true).
	var ended, cancelled atomic.Bool
	cancel, err := StartPick(PickPoint, SafetyScene{Monitors: mons}, []ScenePoint{{ID: "a", X: m.L + 300, Y: m.T + 300, Index: 0}},
		PickCallbacks{OnEnd: func(c bool) { cancelled.Store(c); ended.Store(true) }})
	if err != nil {
		t.Fatalf("StartPick: %v", err)
	}
	if _, err := StartPick(PickZone, SafetyScene{}, nil, PickCallbacks{}); err != errPickActive {
		t.Errorf("second pick: want errPickActive, got %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	focusKept("pick")
	cancel()
	for i := 0; i < 50 && !ended.Load(); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !ended.Load() || !cancelled.Load() {
		t.Fatalf("cancel: ended=%v cancelled=%v", ended.Load(), cancelled.Load())
	}
	time.Sleep(50 * time.Millisecond)
	if c, _ := visibleCounts(); c.pick != 0 || c.safety != 0 || c.points != 0 {
		t.Fatalf("cancelled pick left layers up: %+v", c)
	}

	// The non-persistent preview is gone by 3 s.
	ShowSafety(SafetyScene{Monitors: mons, Corners: [4]int32{50, 50, 50, 50}, CornersOn: true}, false)
	time.Sleep(3300 * time.Millisecond)
	if c, _ := visibleCounts(); c.safety != 0 {
		t.Fatalf("safety preview should have faded out: %+v", c)
	}
	focusKept("fade")
}
