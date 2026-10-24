//go:build windows

package replay

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestToastSmoke shows the real toast + chime and checks focus never moves.
// Opt-in (TORU_SMOKE=1): it draws on the desktop and makes a sound.
func TestToastSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	before := windows.GetForegroundWindow()
	showToast("Saving clip…", "Test · Last 1 min", true)
	time.Sleep(700 * time.Millisecond)
	showToast("Clip saved", "Test · Last 1 min", true)
	playSaveSound(true)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if fg := windows.GetForegroundWindow(); fg != before {
			t.Fatalf("toast stole focus: foreground %x -> %x", before, fg)
		}
	}
	toastMu.Lock()
	shown := toastHwnd != 0
	toastMu.Unlock()
	if !shown {
		t.Fatal("toast window not on screen")
	}
	time.Sleep(3500 * time.Millisecond) // hold + fade
	toastMu.Lock()
	gone := toastHwnd == 0
	toastMu.Unlock()
	if !gone {
		t.Error("toast should have faded out and closed")
	}
}
