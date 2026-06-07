package capture

import (
	"context"
	"os/exec"
	"regexp"
	"time"
)

// audio_devices.go enumerates microphone (dshow audio input) devices for the
// audio-sources picker. ffmpeg's device listing goes to stderr and the probe
// "fails" by design (dummy input), so the exit code is ignored — only the
// output matters.

var dshowAudioLine = regexp.MustCompile(`"([^"]+)"\s+\(audio\)`)

// ListMicrophones returns the dshow audio input device names ("Microphone
// (Realtek(R) Audio)", …). Best-effort: an empty list just means the mic row
// of the picker is empty.
func ListMicrophones() []string {
	bin, err := LocateFFmpeg()
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-hide_banner", "-list_devices", "true", "-f", "dshow", "-i", "dummy")
	configureSysProcAttr(cmd)
	out, _ := cmd.CombinedOutput() // always "fails" — the listing is on stderr
	var mics []string
	for _, m := range dshowAudioLine.FindAllStringSubmatch(string(out), -1) {
		mics = append(mics, m[1])
	}
	return mics
}
