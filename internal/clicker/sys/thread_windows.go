//go:build windows

package sys

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	threadPriorityHighest     = 2          // THREAD_PRIORITY_HIGHEST (not TIME_CRITICAL)
	threadPriorityErrorReturn = 0x7FFFFFFF // THREAD_PRIORITY_ERROR_RETURN

	processPowerThrottling = 4 // PROCESS_INFORMATION_CLASS ProcessPowerThrottling

	powerThrottlingCurrentVersion = 1   // PROCESS_POWER_THROTTLING_CURRENT_VERSION
	powerThrottlingExecutionSpeed = 0x1 // PROCESS_POWER_THROTTLING_EXECUTION_SPEED
	powerThrottlingIgnoreTimerRes = 0x4 // PROCESS_POWER_THROTTLING_IGNORE_TIMER_RESOLUTION
)

// powerThrottlingState mirrors PROCESS_POWER_THROTTLING_STATE.
//
// Semantics (SetProcessInformation docs): ControlMask picks the mechanisms the
// process takes control of; StateMask says, for each controlled mechanism,
// whether throttling is on (bit set) or off (bit clear). Mechanisms not in
// ControlMask are left to the system. So ControlMask=EXECUTION_SPEED|
// IGNORE_TIMER_RESOLUTION with StateMask=0 means "never EcoQoS, always honour
// timer resolution", and ControlMask=0, StateMask=0 hands everything back to
// the system's heuristics.
type powerThrottlingState struct {
	Version     uint32
	ControlMask uint32
	StateMask   uint32
}

// Power throttling is process-wide, so the opt-out is ref-counted across runs:
// the first BeginRun saves the prior state and opts out, the last end restores.
var (
	throttleMu    sync.Mutex
	throttleRefs  int
	throttlePrev  powerThrottlingState
	throttleSaved bool // throttlePrev came from GetProcessInformation
	throttleSet   bool // the opt-out call succeeded
)

// BeginRun prepares the calling goroutine to run the click loop: it locks it to
// its OS thread, raises the thread to THREAD_PRIORITY_HIGHEST and opts the
// process out of Windows 11 power throttling (EcoQoS and ignored timer
// resolution, which otherwise hit Toru when it has no visible window). end
// undoes all of it and MUST be called on the same goroutine (it unlocks the
// OS thread). warning is non-empty when the thread priority could not be
// raised; a failed throttling opt-out is silent (older Windows lacks it).
func BeginRun() (end func(), warning string) {
	runtime.LockOSThread()

	thread := windows.CurrentThread()
	prev, _, _ := syscall.SyscallN(procGetThreadPriority.Addr(), uintptr(thread))
	raised := false
	if r, _, _ := syscall.SyscallN(procSetThreadPriority.Addr(), uintptr(thread), threadPriorityHighest); r != 0 {
		raised = true
	} else {
		warning = "Could not raise the click thread's priority; timing may be less steady"
	}

	optOutThrottling()

	return func() {
		releaseThrottling()
		if raised && uint32(prev) != threadPriorityErrorReturn {
			_, _, _ = syscall.SyscallN(procSetThreadPriority.Addr(), uintptr(thread), uintptr(int32(prev)))
		}
		runtime.UnlockOSThread()
	}, warning
}

func setThrottling(s powerThrottlingState) bool {
	r, _, _ := syscall.SyscallN(procSetProcessInformation.Addr(),
		uintptr(windows.CurrentProcess()), processPowerThrottling,
		uintptr(unsafe.Pointer(&s)), unsafe.Sizeof(s))
	return r != 0
}

func getThrottling() (powerThrottlingState, bool) {
	s := powerThrottlingState{Version: powerThrottlingCurrentVersion}
	r, _, _ := syscall.SyscallN(procGetProcessInformation.Addr(),
		uintptr(windows.CurrentProcess()), processPowerThrottling,
		uintptr(unsafe.Pointer(&s)), unsafe.Sizeof(s))
	return s, r != 0
}

func optOutThrottling() {
	throttleMu.Lock()
	defer throttleMu.Unlock()
	throttleRefs++
	if throttleRefs > 1 {
		return
	}
	throttlePrev, throttleSaved = getThrottling()
	throttleSet = setThrottling(powerThrottlingState{
		Version:     powerThrottlingCurrentVersion,
		ControlMask: powerThrottlingExecutionSpeed | powerThrottlingIgnoreTimerRes,
	})
	if !throttleSet {
		// Windows 10 rejects the IGNORE_TIMER_RESOLUTION bit; take what it has.
		throttleSet = setThrottling(powerThrottlingState{
			Version:     powerThrottlingCurrentVersion,
			ControlMask: powerThrottlingExecutionSpeed,
		})
	}
}

func releaseThrottling() {
	throttleMu.Lock()
	defer throttleMu.Unlock()
	if throttleRefs == 0 {
		return
	}
	throttleRefs--
	if throttleRefs > 0 || !throttleSet {
		return
	}
	restore := powerThrottlingState{Version: powerThrottlingCurrentVersion} // system managed
	if throttleSaved {
		restore = throttlePrev
		restore.Version = powerThrottlingCurrentVersion
	}
	if !setThrottling(restore) {
		setThrottling(powerThrottlingState{Version: powerThrottlingCurrentVersion})
	}
	throttleSet = false
}
