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

// TestTrimCropIntegration runs a real crop through BuildTrimArgs + RunFFmpeg
// (the same pair vid.VideoService.Trim uses) and verifies the output
// dimensions with ffprobe. It needs ffmpeg and ffprobe on PATH; otherwise it
// skips, so CI without the tools stays green.
//
//	go test ./internal/capture -run TrimCropIntegration -v
func TestTrimCropIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skipf("ffprobe not on PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The source is synthesised with lavfi, which a stripped ffmpeg build may
	// lack; probe for it and skip rather than fail on such a build.
	if err := RunFFmpeg(ctx, []string{
		"-hide_banner", "-f", "lavfi", "-i", "nullsrc=size=16x16", "-frames:v", "1", "-f", "null", "-",
	}); err != nil {
		t.Skipf("ffmpeg has no lavfi device: %v", err)
	}

	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	out := filepath.Join(dir, "out.mp4")

	// 2 s of the synthetic test pattern at 640x360.
	if err := RunFFmpeg(ctx, []string{
		"-y", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=30",
		"-t", "2", "-pix_fmt", "yuv420p", src,
	}); err != nil {
		t.Fatalf("generate source: %v", err)
	}

	// Odd W/H/X/Y on purpose: the args builder must even-align all four to
	// crop=300:200:100:50 (yuv420p chroma grid) and ffmpeg must accept it.
	req := TrimRequest{
		VideoPath: src, StartMs: 0, EndMs: 2000, Precise: false,
		Crop:    &Rect{X: 101, Y: 51, W: 301, H: 201},
		OutPath: out,
	}
	args := BuildTrimArgs(req)
	t.Logf("ffmpeg args: %s", strings.Join(args, " "))
	if got := strings.Join(args, " "); !strings.Contains(got, "crop=300:200:100:50") {
		t.Fatalf("expected even-aligned crop filter in args: %s", got)
	}
	if err := RunFFmpeg(ctx, args); err != nil {
		t.Fatalf("crop trim: %v", err)
	}
	if fi, err := os.Stat(out); err != nil || fi.Size() == 0 {
		t.Fatalf("output missing or empty: %v", err)
	}

	probe := exec.CommandContext(ctx, ffprobe, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", out)
	raw, err := probe.Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	got := strings.TrimSpace(string(raw))
	t.Logf("ffprobe width,height: %q", got)
	if got != "300,200" {
		t.Fatalf("cropped output is %q, want \"300,200\"", got)
	}
}
