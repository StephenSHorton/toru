package capture

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Clips Toru writes start and stop wherever the person pressed save or set
// the trim, usually mid-sound: the first sample jumps from silence straight
// to the waveform (60x a normal sample step on a real replay), which every
// player renders as a click (Irori #131). So every cut Toru writes gets a
// short fade at its sound's edges: too short to hear as a fade, long enough
// to take the click out. Video is still stream-copied; only the audio is
// re-encoded (AAC, or Opus in WebM), which takes about a second.

// EdgeFadeMs is the fade at each edge of a written cut.
const EdgeFadeMs = 10

// EdgeAudioBitrate is the AAC bitrate of re-encoded edge-faded audio: above
// what Toru records at (160k), so the extra generation stays transparent.
const EdgeAudioBitrate = "192k"

// EdgeFadeFilter is the -af chain for a cut durMs long: a fade in at 0 and,
// when the length is known (durMs > 0), a fade out at the end. "" for a cut
// shorter than four fades: too short to fade sensibly.
func EdgeFadeFilter(durMs int) string {
	if durMs > 0 && durMs < 4*EdgeFadeMs {
		return ""
	}
	d := msSec(EdgeFadeMs)
	f := "afade=t=in:st=0:d=" + d
	if durMs > 0 {
		f += fmt.Sprintf(",afade=t=out:st=%s:d=%s", msSec(durMs-EdgeFadeMs), d)
	}
	return f
}

// edgeAudioArgs are the output args that copy the video and re-encode the
// audio with edge fades, for an output at outPath durMs long (0 = unknown:
// fade in only).
func edgeAudioArgs(outPath string, durMs int) []string {
	codec := []string{"-c:a", "aac", "-b:a", EdgeAudioBitrate}
	if strings.EqualFold(filepath.Ext(outPath), ".webm") { // WebM carries Opus, not AAC
		codec = []string{"-c:a", "libopus", "-b:a", EdgeAudioBitrate}
	}
	args := append([]string{"-c:v", "copy"}, codec...)
	if f := EdgeFadeFilter(durMs); f != "" {
		args = append(args, "-af", f)
	}
	return args
}

func msSec(ms int) string { return fmt.Sprintf("%d.%03d", ms/1000, ms%1000) }

// replayEndMargin is how far before a replay's probed end its fade out
// ends: the probed length can be a frame or an AAC packet off, and a fade
// that ends a little early only leaves a few ms of silence.
const replayEndMargin = 40

// replayMs is how long a replay stitched from segs plays, in ms (0 when
// ffprobe can't tell). Segments of one run carry on each other's
// timestamps, so a run lasts from its first segment's start to its last
// one's end; runs play back to back.
func replayMs(segs []replaySegment) int {
	total := 0.0
	for i := 0; i < len(segs); {
		j := i
		for j+1 < len(segs) && segs[j+1].run == segs[i].run {
			j++
		}
		s0, _, ok1 := probeTimes(segs[i].path)
		s1, d1, ok2 := probeTimes(segs[j].path)
		if !ok1 || !ok2 || s1+d1 <= s0 {
			return 0
		}
		total += s1 + d1 - s0
		i = j + 1
	}
	return int(total * 1000)
}

// probeTimes reads the start time and duration (s) of a file's first audio
// stream: the format's span also covers the video's tail, which runs past
// the sound.
func probeTimes(path string) (start, dur float64, ok bool) {
	bin, err := LocateFFprobe()
	if err != nil {
		return 0, 0, false
	}
	cmd := exec.Command(bin, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=start_time,duration", "-of", "csv=p=0", path)
	configureSysProcAttr(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}
	// MPEG-TS lists the stream under its program too: the first line will do.
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	f := strings.Split(strings.TrimSpace(line), ",")
	if len(f) != 2 {
		return 0, 0, false
	}
	start, e1 := strconv.ParseFloat(f[0], 64)
	dur, e2 := strconv.ParseFloat(f[1], 64)
	return start, dur, e1 == nil && e2 == nil && dur > 0
}
