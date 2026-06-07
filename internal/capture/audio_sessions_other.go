//go:build !windows

package capture

// EnumAudioSessions is Windows-only (audio sessions API); see
// audio_sessions_windows.go.
func EnumAudioSessions() []AudioSession { return nil }

// startProcessLoopbackAudio is Windows-only (process loopback activation);
// see audio_process_windows.go.
func startProcessLoopbackAudio(uint32, string) (audioSource, error) {
	return startLoopbackAudio("") // returns the platform error
}
