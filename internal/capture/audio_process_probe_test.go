//go:build windows

package capture

import (
	"io"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestProcessLoopbackProbe captures audio from ONE process (an ffplay playing
// a tone) and asserts the captured PCM is non-silent. Opt-in: needs a desktop
// session + speakers (TORU_APP_AUDIO_PROBE=1).
func TestProcessLoopbackProbe(t *testing.T) {
	if os.Getenv("TORU_APP_AUDIO_PROBE") != "1" {
		t.Skip("opt-in")
	}
	tone := exec.Command("ffplay", "-hide_banner", "-loglevel", "error", "-nodisp", "-autoexit",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8")
	if err := tone.Start(); err != nil {
		t.Skipf("ffplay unavailable: %v", err)
	}
	defer func() { _ = tone.Process.Kill() }()
	time.Sleep(800 * time.Millisecond)

	src, err := startProcessLoopbackAudio(uint32(tone.Process.Pid), "toru-app-audio-probe")
	if err != nil {
		t.Fatalf("startProcessLoopbackAudio: %v", err)
	}
	defer src.Stop()
	in := src.Input()
	t.Logf("process loopback up: %+v", in)

	// Be ffmpeg: open the pipe and read ~1.5s of PCM.
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(in.PipePath),
		windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatalf("open pipe: %v", err)
	}
	pipe := os.NewFile(uintptr(h), "pipe")
	defer func() { _ = pipe.Close() }()

	want := in.SampleRate * in.Channels * 2 * 3 / 2 // 1.5s of s16
	buf := make([]byte, want)
	if _, err := io.ReadFull(pipe, buf); err != nil {
		t.Fatalf("read pcm: %v", err)
	}

	// Non-silence check: peak |sample| over the window.
	peak := 0
	for i := 0; i+1 < len(buf); i += 2 {
		v := int(int16(uint16(buf[i]) | uint16(buf[i+1])<<8))
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	t.Logf("peak sample: %d / 32767", peak)
	if peak < 500 {
		t.Fatalf("captured PCM is silent (peak=%d) — process loopback not delivering", peak)
	}
}
