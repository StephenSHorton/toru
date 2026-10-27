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

// startExcludeProcessLoopbackAudio is Windows-only; see audio_process_windows.go.
func startExcludeProcessLoopbackAudio(uint32, string) (audioSource, error) {
	return startLoopbackAudio("")
}

// runningAudioMixers is Windows-only; see audio_mixers_windows.go.
func runningAudioMixers() []audioMixer { return nil }

// audioMixer is one running mixer root process.
type audioMixer struct {
	name string
	pid  uint32
}

// startAppMixAudio is Windows-only; see audio_appmix_windows.go.
func startAppMixAudio([]uint32, string) (audioSource, error) {
	return startLoopbackAudio("")
}
