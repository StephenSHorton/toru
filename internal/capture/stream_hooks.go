package capture

import "os/exec"

// stream_hooks.go exposes the pieces of the replay pipeline that live
// streaming (internal/stream) reuses: hardware encoder selection, monitor →
// ddagrab output mapping, hidden ffmpeg children tied to Toru's lifetime, and
// desktop-audio loopback.

// AudioCapture is one live loopback capture that ffmpeg reads from Input's pipe.
type AudioCapture interface {
	Input() AudioInput
	Stop()
}

// HardwareEncoderFor returns the first usable hardware encoder for codec
// (ReplayCodecH264 | ReplayCodecHEVC | ReplayCodecAV1).
func HardwareEncoderFor(codec string) (string, error) { return replayEncoderFor(codec) }

// ScreenForCapture resolves a ScreenInfo.ID (unknown or <0 = primary) to its
// screen and ddagrab output index.
func ScreenForCapture(monitorID int) (ScreenInfo, int, error) {
	return replayScreen(enumScreens(), monitorID)
}

// PrepareFFmpegCmd hides cmd's console window. Call before Start.
func PrepareFFmpegCmd(cmd *exec.Cmd) { configureSysProcAttr(cmd) }

// TieFFmpegToToru makes a started ffmpeg die with Toru, even on a crash.
func TieFFmpegToToru(cmd *exec.Cmd) { tieToProcessLifetime(cmd) }

// StartDesktopAudio captures the system mix minus excludePID's process tree
// (0 = the whole mix), falling back to the whole mix if the exclusion can't be
// set up. pipeName must be unique per capture.
func StartDesktopAudio(excludePID uint32, pipeName string) (AudioCapture, error) {
	if excludePID != 0 {
		if src, err := startExcludeProcessLoopbackAudio(excludePID, pipeName+"-x"); err == nil {
			return src, nil
		}
	}
	return startLoopbackAudio(pipeName)
}
