//go:build windows

package capture

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRealReplayBufferSmoke runs the replay buffer against the REAL desktop
// with a hardware encoder, saves the last few seconds and checks the MP4. It
// needs a desktop session, ffmpeg and a GPU encoder, so it only runs locally:
//
//	TORU_SMOKE=1 go test ./internal/capture -run RealReplayBufferSmoke -v
func TestRealReplayBufferSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("real-desktop smoke test; opt in with TORU_SMOKE=1")
	}
	if !ReplayEncoderAvailable(ReplayCodecH264) {
		t.Skip("no hardware H.264 encoder")
	}
	dir := t.TempDir()
	b := NewReplayBuffer()
	cfg := ReplayConfig{
		MonitorID: -1, FPS: 60, BitrateKbps: 15000, Codec: ReplayCodecH264,
		IncludeCursor: true, KeepSeconds: 6, Dir: filepath.Join(dir, "segs"),
		// Desktop and Discord as separate sources and tracks: exercises the
		// exclude/include loopback split (or the silence placeholder when
		// Discord isn't running), volume and asplit.
		Audio: ReplayAudio{Desktop: true, Discord: true, SeparateTracks: true, DiscordVolume: 80},
	}
	if err := b.Start(cfg); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer b.Stop()
	time.Sleep(9 * time.Second)

	st := b.Status()
	t.Logf("status: %+v", st)
	if !st.Recording || st.Error != "" {
		t.Fatalf("buffer not healthy: %+v", st)
	}
	if n := len(listReplaySegments(cfg.Dir)); n > 6/replaySegmentSec+2 {
		t.Errorf("prune left %d segments", n)
	}

	out := filepath.Join(dir, "replay.mp4")
	if err := b.Save(4, out); err != nil {
		t.Fatalf("Save: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ms, err := ProbeDurationMs(ctx, out)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("saved %dms", ms)
	if ms < 3500 || ms > 7000 {
		t.Errorf("saved clip is %dms, want ~4-6s", ms)
	}
	probe, err := LocateFFprobe()
	if err != nil {
		t.Skipf("ffprobe not available: %v", err)
	}
	tracks, err := exec.Command(probe, "-v", "error", "-select_streams", "a",
		"-show_entries", "stream_tags=handler_name", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if got := strings.Join(strings.Fields(strings.ReplaceAll(string(tracks), "", "")), " "); got != "All audio Game & desktop Discord" {
		t.Errorf("audio tracks = %q", got)
	}
}
