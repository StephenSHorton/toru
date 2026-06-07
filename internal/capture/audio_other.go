//go:build !windows

package capture

import "errors"

// startLoopbackAudio is Windows-only (WASAPI); see audio_windows.go.
// Recordings off Windows are video-only.
func startLoopbackAudio(string) (audioSource, error) {
	return nil, errors.New("system audio capture is Windows-only")
}
