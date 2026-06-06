//go:build !windows

package overlay

import "time"

// settleCompositor on non-Windows hosts just waits ~one frame. The real overlay
// (freeze + DWM) is Windows-only; this stub keeps the package cross-compilable.
func settleCompositor() { time.Sleep(16 * time.Millisecond) }
