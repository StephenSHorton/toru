//go:build windows

package replay

import (
	"os"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestToastSmoke shows the save toasts + sound and checks focus never moves.
// Opt-in (TORU_SMOKE=1): it draws on the desktop and makes a sound. The card
// window's own show/click/fade assertions live in go-toast.
func TestToastSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	before := windows.GetForegroundWindow()
	savingToast("Test · Last 1 min")
	time.Sleep(700 * time.Millisecond)
	playSaveSound(true)
	savedToast("Test · Last 1 min", nil)
	for i := 0; i < 20; i++ {
		time.Sleep(100 * time.Millisecond)
		if fg := windows.GetForegroundWindow(); fg != before {
			t.Fatalf("toast stole focus: foreground %x -> %x", before, fg)
		}
	}
}
