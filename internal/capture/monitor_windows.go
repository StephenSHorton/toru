//go:build windows

package capture

var procMonitorFromPoint = user32Enum.NewProc("MonitorFromPoint")

// monitorHandle returns the HMONITOR at the centre of screen (0 if none),
// which is how gfxcapture is told the monitor: its monitor_idx order is not
// ddagrab's output_idx order. Looked up per run, since handles change when
// displays are reconfigured.
func monitorHandle(screen ScreenInfo) uintptr {
	const monitorDefaultToNull = 0
	x := int32(screen.X + screen.W/2)
	y := int32(screen.Y + screen.H/2)
	// POINT is passed by value: on x64 both fields travel in one register.
	pt := uintptr(uint32(x)) | uintptr(uint32(y))<<32
	h, _, _ := procMonitorFromPoint.Call(pt, monitorDefaultToNull)
	return h
}
