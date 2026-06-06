//go:build windows

package overlay

import (
	"syscall"
	"time"
)

// dwmapi.DwmFlush blocks until the DWM has finished its next composition pass,
// i.e. until the buffered frame (here: the just-Hidden overlay) has actually left
// the screen. It is the precise primitive for "wait for the compositor to drop the
// fading overlay before grabbing the live desktop".
var (
	dwmapi       = syscall.NewLazyDLL("dwmapi.dll")
	procDwmFlush = dwmapi.NewProc("DwmFlush")
)

// settleCompositor waits for the DWM to compose at least one frame so a window
// Hidden microseconds ago is fully off-screen before the next freeze. DwmFlush is
// authoritative; the small sleep is belt-and-suspenders (one extra ~16ms frame)
// for the rare case DwmFlush is unavailable or returns immediately on a paused
// compositor. Called ONLY when a window was actually visible (New-from-edit), so
// the cold/idle instant-re-engage paths never pay it.
func settleCompositor() {
	if procDwmFlush.Find() == nil {
		_, _, _ = procDwmFlush.Call()
	}
	time.Sleep(16 * time.Millisecond)
}
