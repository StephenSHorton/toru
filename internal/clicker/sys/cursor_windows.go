//go:build windows

package sys

import (
	"syscall"
	"unsafe"
)

// CursorPos returns the cursor position in virtual-desktop physical px.
func CursorPos() (x, y int32, ok bool) {
	var pt [2]int32
	r, _, _ := syscall.SyscallN(procGetCursorPos.Addr(), uintptr(unsafe.Pointer(&pt)))
	if r == 0 {
		return 0, 0, false
	}
	return pt[0], pt[1], true
}

// VirtualScreen returns the virtual desktop rect (all monitors) in physical px.
// The origin is negative when a monitor sits left of or above the primary.
func VirtualScreen() (x, y, w, h int32) {
	return systemMetric(smXVirtualScreen), systemMetric(smYVirtualScreen),
		systemMetric(smCXVirtualScreen), systemMetric(smCYVirtualScreen)
}

const (
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

func systemMetric(i uintptr) int32 {
	r, _, _ := syscall.SyscallN(procGetSystemMetrics.Addr(), i)
	return int32(r)
}
