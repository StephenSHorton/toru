//go:build windows

package capture

import (
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// audio_sessions_windows.go enumerates the applications CURRENTLY producing
// audio (the picker list for per-app capture): default render endpoint →
// IAudioSessionManager2 → session enumerator → per-session PID → process name.

var (
	iidIAudioSessionManager2 = comGUID{0x77aa99a0, 0x1bd6, 0x484f, [8]byte{0x8b, 0xc7, 0x2c, 0x65, 0x4c, 0x9a, 0x9b, 0x6f}}
	iidIAudioSessionControl2 = comGUID{0xbfb7ff88, 0x7239, 0x4fc9, [8]byte{0x8f, 0xa2, 0x07, 0xc9, 0x50, 0xbe, 0x9c, 0x6d}}
)

// EnumAudioSessions lists apps with live audio sessions on the default
// output. Best-effort: any COM failure returns what was gathered so far
// (possibly nil) — the picker simply shows fewer rows.
func EnumAudioSessions() []AudioSession {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(hr) < 0 && uint32(hr) != 0x80010106 {
		return nil
	}

	var enumerator *comObject
	if hr, _, _ := procCoCreateInst.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	); int32(hr) < 0 || enumerator == nil {
		return nil
	}
	defer comRelease(enumerator)

	var device *comObject // GetDefaultAudioEndpoint(eRender, eConsole) = vtbl 4
	if hr := int32(comCall(enumerator, 4, 0, 0, uintptr(unsafe.Pointer(&device)))); hr < 0 || device == nil {
		return nil
	}
	defer comRelease(device)

	var out []AudioSession
	for _, pid := range deviceSessionPIDs(device, map[uint32]bool{}) {
		if name := processName(pid); name != "" {
			out = append(out, AudioSession{PID: pid, Name: name})
		}
	}
	return out
}

// renderSessionPIDs returns the PIDs owning an audio session on ANY active
// render endpoint (not just the default: a game can play straight to a
// headset while the default is a mixer's virtual device). Expired sessions
// (app streams closed for now) are kept, so a paused app keeps its place.
// Skips the system-sounds session. Best-effort, like EnumAudioSessions.
func renderSessionPIDs() []uint32 {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(hr) < 0 && uint32(hr) != 0x80010106 {
		return nil
	}
	var enumerator *comObject
	if hr, _, _ := procCoCreateInst.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	); int32(hr) < 0 || enumerator == nil {
		return nil
	}
	defer comRelease(enumerator)

	var coll *comObject // EnumAudioEndpoints(eRender, DEVICE_STATE_ACTIVE) = vtbl 3
	if hr := int32(comCall(enumerator, 3, 0, 1, uintptr(unsafe.Pointer(&coll)))); hr < 0 || coll == nil {
		return nil
	}
	defer comRelease(coll)
	var count uint32 // IMMDeviceCollection::GetCount = vtbl 3
	if hr := int32(comCall(coll, 3, uintptr(unsafe.Pointer(&count)))); hr < 0 {
		return nil
	}
	seen := map[uint32]bool{}
	var out []uint32
	for i := uint32(0); i < count; i++ {
		var dev *comObject // IMMDeviceCollection::Item = vtbl 4
		if hr := int32(comCall(coll, 4, uintptr(i), uintptr(unsafe.Pointer(&dev)))); hr < 0 || dev == nil {
			continue
		}
		out = append(out, deviceSessionPIDs(dev, seen)...)
		comRelease(dev)
	}
	return out
}

// deviceSessionPIDs lists the PIDs (not already in seen, which it updates)
// owning a non-system audio session on one render endpoint.
func deviceSessionPIDs(device *comObject, seen map[uint32]bool) []uint32 {
	var mgr *comObject // IMMDevice::Activate = vtbl 3
	if hr := int32(comCall(device, 3, uintptr(unsafe.Pointer(&iidIAudioSessionManager2)), clsctxAll, 0, uintptr(unsafe.Pointer(&mgr)))); hr < 0 || mgr == nil {
		return nil
	}
	defer comRelease(mgr)

	var sessEnum *comObject // IAudioSessionManager2::GetSessionEnumerator = vtbl 5
	if hr := int32(comCall(mgr, 5, uintptr(unsafe.Pointer(&sessEnum)))); hr < 0 || sessEnum == nil {
		return nil
	}
	defer comRelease(sessEnum)

	var count int32 // IAudioSessionEnumerator::GetCount = vtbl 3
	if hr := int32(comCall(sessEnum, 3, uintptr(unsafe.Pointer(&count)))); hr < 0 {
		return nil
	}

	var out []uint32
	for i := int32(0); i < count; i++ {
		var ctl *comObject // GetSession = vtbl 4
		if hr := int32(comCall(sessEnum, 4, uintptr(i), uintptr(unsafe.Pointer(&ctl)))); hr < 0 || ctl == nil {
			continue
		}
		var ctl2 *comObject // QueryInterface for IAudioSessionControl2
		hrQI := int32(comCall(ctl, 0, uintptr(unsafe.Pointer(&iidIAudioSessionControl2)), uintptr(unsafe.Pointer(&ctl2))))
		comRelease(ctl)
		if hrQI < 0 || ctl2 == nil {
			continue
		}
		// Skip the system-sounds session (no meaningful process to capture).
		// IsSystemSoundsSession = vtbl 15: returns S_OK (0) when it IS.
		isSystem := comCall(ctl2, 15) == 0
		var pid uint32 // GetProcessId = vtbl 14
		hrPID := int32(comCall(ctl2, 14, uintptr(unsafe.Pointer(&pid))))
		comRelease(ctl2)
		if isSystem || hrPID < 0 || pid == 0 || seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
	}
	return out
}

// processName resolves a PID to its executable's display name ("Discord").
func processName(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var buf [windows.MAX_PATH]uint16
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	base := filepath.Base(windows.UTF16ToString(buf[:size]))
	return strings.TrimSuffix(base, filepath.Ext(base))
}
