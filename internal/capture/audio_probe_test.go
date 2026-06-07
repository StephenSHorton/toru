//go:build windows

package capture

import (
	"os"
	"testing"
)

func TestLoopbackProbe(t *testing.T) {
	if os.Getenv("TORU_AUDIO_PROBE") != "1" {
		t.Skip("opt-in")
	}
	a, err := startLoopbackAudio("toru-audio-probe")
	if err != nil {
		t.Fatalf("startLoopbackAudio: %v", err)
	}
	t.Logf("loopback OK: %+v", a.Input())
	a.Stop()
}
