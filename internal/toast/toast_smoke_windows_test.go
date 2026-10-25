//go:build windows

package toast

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestToastSmoke shows the real toast and checks focus never moves.
// Opt-in (TORU_SMOKE=1): it draws on the desktop.
func TestToastSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	before := windows.GetForegroundWindow()
	Show("Saving clip…", "Test · Last 1 min", true)
	time.Sleep(700 * time.Millisecond)
	Show("Clip saved", "Test · Last 1 min", true)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if fg := windows.GetForegroundWindow(); fg != before {
			t.Fatalf("toast stole focus: foreground %x -> %x", before, fg)
		}
	}
	if !Visible() {
		t.Fatal("toast window not on screen")
	}
	time.Sleep(3500 * time.Millisecond) // hold + fade
	if Visible() {
		t.Error("toast should have faded out and closed")
	}
}
