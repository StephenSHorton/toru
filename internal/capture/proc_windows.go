//go:build windows

package capture

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// createNoWindow prevents the child from allocating a console.
// (syscall.CREATE_NO_WINDOW is not defined in the stdlib syscall package.)
const createNoWindow = 0x08000000

// configureSysProcAttr hides the console of spawned ffmpeg processes: the
// production app is built with `-H windowsgui`, and without CREATE_NO_WINDOW
// every recording/probe/trim would flash a black console box over the user's
// screen — which would also be CAPTURED by the recording itself.
func configureSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

// ffmpegJob is a KILL_ON_JOB_CLOSE job object owning every long-lived ffmpeg
// we spawn. When the Toru process dies — including a hard kill, where no Go
// cleanup runs — the OS closes the job handle and terminates the children.
// Without this, a crashed/killed app leaves an ORPHANED ffmpeg recording the
// screen forever (observed during E2E verification).
var (
	jobOnce   sync.Once
	jobHandle windows.Handle
)

func ffmpegJob() windows.Handle {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			return // best-effort: recording still works, just unguarded
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(
			h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(h)
			return
		}
		jobHandle = h
	})
	return jobHandle
}

// tieToProcessLifetime assigns a started child to the kill-on-close job so it
// cannot outlive the app. Best-effort: failures (e.g. the child already
// exited) leave the recording functional, only without the orphan guard.
func tieToProcessLifetime(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	job := ffmpegJob()
	if job == 0 {
		return
	}
	ph, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return
	}
	defer func() { _ = windows.CloseHandle(ph) }()
	_ = windows.AssignProcessToJobObject(job, ph)
}
