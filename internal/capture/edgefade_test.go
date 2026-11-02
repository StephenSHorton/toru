package capture

import (
	"context"
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestEdgeFadeFilter(t *testing.T) {
	for _, c := range []struct {
		ms   int
		want string
	}{
		{0, "afade=t=in:st=0:d=0.010"},
		{1000, "afade=t=in:st=0:d=0.010,afade=t=out:st=0.990:d=0.010"},
		{61234, "afade=t=in:st=0:d=0.010,afade=t=out:st=61.224:d=0.010"},
		{30, ""},
	} {
		if got := EdgeFadeFilter(c.ms); got != c.want {
			t.Errorf("EdgeFadeFilter(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// edgeJumps decodes a file's first audio track and returns how big its first
// and last samples are next to its typical sample step: a cut mid-sound
// starts with a jump many times the step (a click); a faded edge doesn't.
func edgeJumps(t *testing.T, bin, path string) (first, last float64) {
	t.Helper()
	out, err := exec.Command(bin, "-v", "error", "-i", path, "-map", "0:a:0", "-ac", "1", "-ar", "48000", "-f", "f32le", "-").Output()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	x := make([]float64, len(out)/4)
	for i := range x {
		x[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(out[4*i:])))
	}
	if len(x) < 4800 {
		t.Fatalf("%s: only %d samples", path, len(x))
	}
	steps := make([]float64, 0, len(x))
	for i := 1; i < len(x); i++ {
		steps = append(steps, math.Abs(x[i]-x[i-1]))
	}
	sort.Float64s(steps)
	med := steps[len(steps)/2]
	return math.Abs(x[0]) / med, math.Abs(x[len(x)-1]) / med
}

// TestEdgeFadesOnSavedCuts writes a replay (stitched from segments cut
// mid-tone) and a stream-copy trim of it, and checks both start and end
// without a click: edge sample / median step < 3 (Irori #131). Without the
// fades the replay starts at ~0.4 of full scale, hundreds of steps.
func TestEdgeFadesOnSavedCuts(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	bin, err := LocateFFmpeg()
	if err != nil {
		t.Skip("no ffmpeg")
	}
	if _, err := LocateFFprobe(); err != nil {
		t.Skip("no ffprobe")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// 2 s MPEG-TS segments of one run, cut by the segment muxer like the
	// replay buffer (timestamps carry on), the tone at full swing (cos) so
	// the first sample is far from 0. The save takes the first three: like a
	// live buffer, the last one stops mid-sound.
	if err := RunFFmpeg(ctx, []string{"-y", "-f", "lavfi", "-i", "color=c=gray:s=160x90:r=30:d=8",
		"-f", "lavfi", "-i", "aevalsrc='0.4*cos(2*PI*300*t)':s=48000:d=8",
		"-c:v", "libx264", "-g", "30", "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "160k",
		"-force_key_frames", "expr:gte(t,n_forced*2)", "-f", "segment", "-segment_time", "2", "-segment_format", "mpegts",
		filepath.Join(dir, "r0001-%08d.ts")}); err != nil {
		t.Skipf("make segments (needs lavfi + libx264): %v", err)
	}
	segs := listReplaySegments(dir)
	if len(segs) < 4 {
		t.Fatalf("want 4 segments, got %d", len(segs))
	}
	segs = segs[:3]
	// The first segment runs ~3 s (the muxer cuts at 2 s of video time,
	// which starts after the audio), so the three hold ~6.95 s of sound.
	if ms := replayMs(segs); ms < 6850 || ms > 6960 {
		t.Errorf("replayMs = %d, want ~6930 (a little under the sound's 6955)", ms)
	}
	replay := filepath.Join(dir, "replay.mp4")
	if err := stitchReplay(bin, segs, replay, []string{"Mix"}); err != nil {
		t.Fatal(err)
	}
	if f, l := edgeJumps(t, bin, replay); f >= 3 || l >= 3 {
		t.Errorf("replay edges: first %.1fx, last %.1fx the median step, want < 3", f, l)
	}
	trim := filepath.Join(dir, "trim.mp4")
	if err := RunFFmpeg(ctx, BuildTrimArgs(TrimRequest{VideoPath: replay, StartMs: 1234, EndMs: 4321, OutPath: trim})); err != nil {
		t.Fatal(err)
	}
	if f, l := edgeJumps(t, bin, trim); f >= 3 || l >= 3 {
		t.Errorf("trim edges: first %.1fx, last %.1fx the median step, want < 3", f, l)
	}
}
