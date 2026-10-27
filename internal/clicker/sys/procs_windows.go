//go:build windows

package sys

import "golang.org/x/sys/windows"

// Win32 procs for the whole package. golang.org/x/sys/windows covers events and
// WaitForMultipleObjects; everything else is a lazy proc. Calls on the hot path
// (SendInput, QueryPerformanceCounter, SetWaitableTimer) go through
// syscall.SyscallN(proc.Addr(), ...) instead of proc.Call, whose
// //go:uintptrescapes arguments, variadic slice and boxed error allocate.

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	winmm    = windows.NewLazySystemDLL("winmm.dll")

	procSendInput             = user32.NewProc("SendInput")
	procMapVirtualKeyW        = user32.NewProc("MapVirtualKeyW")
	procGetKeyState           = user32.NewProc("GetKeyState")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procSetCursorPos          = user32.NewProc("SetCursorPos")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
	procGetDoubleClickTime    = user32.NewProc("GetDoubleClickTime")
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")

	procQueryPerformanceCounter   = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerformanceFrequency = kernel32.NewProc("QueryPerformanceFrequency")
	procCreateWaitableTimerExW    = kernel32.NewProc("CreateWaitableTimerExW")
	procSetWaitableTimer          = kernel32.NewProc("SetWaitableTimer")
	procCancelWaitableTimer       = kernel32.NewProc("CancelWaitableTimer")
	procSetThreadPriority         = kernel32.NewProc("SetThreadPriority")
	procGetThreadPriority         = kernel32.NewProc("GetThreadPriority")
	procSetProcessInformation     = kernel32.NewProc("SetProcessInformation")
	procGetProcessInformation     = kernel32.NewProc("GetProcessInformation")

	procTimeBeginPeriod = winmm.NewProc("timeBeginPeriod")
	procTimeEndPeriod   = winmm.NewProc("timeEndPeriod")
)
