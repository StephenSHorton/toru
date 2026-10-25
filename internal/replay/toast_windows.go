//go:build windows

package replay

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	winmm         = windows.NewLazySystemDLL("winmm.dll")
	procPlaySound = winmm.NewProc("PlaySoundW")
)

// playSaveSound plays Toru's own chime asynchronously. The WAV buffers are
// package-level, so they outlive the async playback. The on-screen toast that
// goes with it lives in internal/toast (showToast wraps it); exclusive-
// fullscreen games bypass the compositor, so the sound is what gets through.
func playSaveSound(ok bool) {
	const sndAsync, sndNoDefault, sndMemory = 0x0001, 0x0002, 0x0004
	data := savedSound
	if !ok {
		data = failedSound
	}
	_, _, _ = procPlaySound.Call(uintptr(unsafe.Pointer(&data[0])), 0, sndAsync|sndNoDefault|sndMemory)
}
