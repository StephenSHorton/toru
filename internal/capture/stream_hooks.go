package capture

import (
	"fmt"
	"os/exec"
	"strings"
)

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

// StartDesktopAudio captures desktop audio minus excludePID's process tree
// (0 = the whole mix) and says how, for the log. With excludePID set it
// NEVER falls back to the whole mix: that mix holds whatever excludePID
// plays (a call's voices), so an exclusion that can't be set up is an error
// and the caller goes without desktop audio.
//
// When an audio mixer such as SteelSeries Sonar runs, excluding one tree is
// not enough: the mixer's own process plays the user's microphone into a
// virtual device (see audio_mixers_windows.go), so the capture is instead a
// mix of per-app captures that leaves out both Toru and the mixers.
func StartDesktopAudio(excludePID uint32, pipeName string) (AudioCapture, string, error) {
	if excludePID == 0 {
		src, err := startLoopbackAudio(pipeName)
		return src, "whole system mix", err
	}
	if mixers := runningAudioMixers(); len(mixers) > 0 {
		ban := []uint32{excludePID}
		var names []string
		for _, m := range mixers {
			ban = append(ban, m.pid)
			names = append(names, fmt.Sprintf("%s (pid %d)", m.name, m.pid))
		}
		how := "each app except Toru and " + strings.Join(names, ", ")
		src, err := startAppMixAudio(ban, pipeName+"-apps")
		if err != nil {
			return nil, how, fmt.Errorf("per-app audio with %s running: %w", strings.Join(names, ", "), err)
		}
		return src, how, nil
	}
	how := fmt.Sprintf("everything except process tree %d", excludePID)
	src, err := startExcludeProcessLoopbackAudio(excludePID, pipeName+"-x")
	if err != nil {
		return nil, how, fmt.Errorf("leaving Toru out of desktop audio: %w", err)
	}
	return src, how, nil
}

// AudioMixerPIDs lists the root pids of the known audio mixers running now
// (nil off Windows). StartDesktopAudio picks its capture from this set once,
// so a caller polls it to notice a mixer that started (or restarted under a
// new pid) after the capture did: its microphone feed would otherwise reach
// the stream.
func AudioMixerPIDs() []uint32 {
	var out []uint32
	for _, m := range runningAudioMixers() {
		out = append(out, m.pid)
	}
	return out
}
