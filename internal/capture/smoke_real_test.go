//go:build windows

package capture

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

// TestRealDesktopRecordingSmoke records ~2s of the REAL desktop through the
// production path (NewRecorder: ddagrab first, gdigrab fallback) and verifies
// the artifact with ffprobe. It needs a desktop session + ffmpeg, so it only
// runs locally with TORU_SMOKE=1:
//
//	TORU_SMOKE=1 go test ./internal/capture -run RealDesktopRecordingSmoke -v
func TestRealDesktopRecordingSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("real-desktop smoke test; opt in with TORU_SMOKE=1")
	}
	if _, err := LocateFFmpeg(); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	if len(EnumDisplays()) == 0 {
		t.Skip("no displays (headless session)")
	}

	r := NewRecorder()
	req := CaptureRequest{
		Mode: ModeVideo, Sub: SubRegion, MonitorID: 0,
		Rect: Rect{X: 0, Y: 0, W: 640, H: 360}, IncludeCursor: true,
	}

	handle, err := r.StartRecording(req)
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	time.Sleep(2 * time.Second)

	res, err := r.StopRecording(handle)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	t.Logf("recorded: %s", res.VideoPath)

	// Verify the artifact is a decodable video, not just a non-empty file.
	ffprobe := "ffprobe"
	if p, err := exec.LookPath("ffprobe"); err == nil {
		ffprobe = p
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=codec_name,width,height",
		"-of", "default=noprint_wrappers=1",
		res.VideoPath,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe rejected the recording: %v\n%s", err, out)
	}
	t.Logf("ffprobe:\n%s", out)
}
