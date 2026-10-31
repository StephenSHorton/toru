package vid

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestHoldSeek(t *testing.T) {
	for _, tc := range []struct {
		inMs int
		fps  float64
		want string
	}{
		{510, 30, "0.491667"},     // inside frame 15 (0.500): just before it
		{500, 30, "0.491667"},     // exactly on frame 15
		{533, 30, "0.491667"},     // still frame 15 (frame 16 starts at 0.5333)
		{534, 30, "0.525000"},     // frame 16
		{0, 30, "0.000000"},       // never negative
		{1001, 60, "0.995833"},    // frame 60 at 60 fps
		{1000, 0, "1.000"},        // fps unknown: as before
		{2002, 29.97, "1.960294"}, // NTSC: 2.002 is still frame 59 (frame 60 starts at 2.002002)
		{2003, 29.97, "1.993660"}, // frame 60
	} {
		if got := holdSeek(tc.inMs, tc.fps); got != tc.want {
			t.Errorf("holdSeek(%d, %g) = %s, want %s", tc.inMs, tc.fps, got, tc.want)
		}
	}
}

// TestHoldFrameMatchesPreview runs the freeze frame's input through real
// ffmpeg and checks the frame it holds is the one a video element shows at
// currentTime = in: the frame that started at or before in.
//
//	go test ./internal/vid -run HoldFrameMatchesPreview -v
func TestHoldFrameMatchesPreview(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg; skipped with -short")
	}
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	src := filepath.Join(t.TempDir(), "src.mp4")
	// mpeg4 is in every build (the pinned LGPL one has no libx264); a GOP of
	// 30 makes the seeks land between keyframes.
	if out, err := exec.CommandContext(ctx, ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=30:duration=2",
		"-c:v", "mpeg4", "-g", "30", src).CombinedOutput(); err != nil {
		t.Skipf("make source: %v: %s", err, out)
	}
	pts := regexp.MustCompile(`pts_time:([0-9.]+)`)
	for _, inMs := range []int{0, 500, 510, 533, 534, 990, 1517} {
		c := ClipJSON{In: inMs, Out: inMs + 1000, Speed: 1, Hold: true}
		args := append([]string{"-hide_banner"}, holdInputArgs(c, src, 30)...)
		args = append(args, "-copyts", "-vf", "trim=end_frame=1,showinfo", "-f", "null", "-")
		// -copyts keeps the source's timestamps, so pts_time is the frame's
		// own time. It must come before -i, so put it there.
		args = moveBeforeInput(args, "-copyts")
		out, err := exec.CommandContext(ctx, ff, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("in=%d: %v: %s", inMs, err, out)
		}
		m := pts.FindSubmatch(out)
		if m == nil {
			t.Fatalf("in=%d: no frame: %s", inMs, out)
		}
		got, _ := strconv.ParseFloat(string(m[1]), 64)
		want := math.Floor(float64(inMs)/1000*30+1e-6) / 30
		if math.Abs(got-want) > 1e-3 {
			t.Errorf("in=%d ms: held the frame at %.4f s, the preview shows %.4f s", inMs, got, want)
		}
	}
}

func moveBeforeInput(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == flag {
			continue
		}
		if a == "-i" {
			out = append(out, flag)
		}
		out = append(out, a)
	}
	return out
}
