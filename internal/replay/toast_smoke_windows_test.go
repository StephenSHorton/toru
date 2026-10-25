//go:build windows

package replay

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestToastSmoke shows the save toast + chime and checks focus never moves.
// Opt-in (TORU_SMOKE=1): it draws on the desktop and makes a sound. The toast
// window's own show/fade assertions live in internal/toast.
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
}
