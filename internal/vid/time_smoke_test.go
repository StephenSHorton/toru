package vid

import (
	"context"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// TestTimeEffectsExportSmoke exports a project with a speed ramp, a 0.1x
// ramp (muted by the audio rule), a freeze frame and smooth slow motion
// through real ffmpeg, then checks the length, where the source's colour
// change lands in each clip, and where there's sound.
//
//	go test ./internal/vid -run TimeEffectsExportSmoke -v
func TestTimeEffectsExportSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video; skipped with -short")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	dir := t.TempDir()
	// 2 s: red for the first second, blue for the second, a tone throughout.
	src := filepath.Join(dir, "twotone.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y", "-f", "lavfi", "-i", "color=c=red:s=320x180:r=30:d=1", "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d=1",
		"-f", "lavfi", "-i", "sine=f=660:duration=2",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-map", "2:a",
		"-c:v", "libx264", "-g", "15", "-pix_fmt", "yuv420p", "-c:a", "aac", src}); err != nil {
		t.Skipf("generate source (needs lavfi + libx264): %v", err)
	}
	full := TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}
	curve := func(audio string, pts ...SpeedPointJSON) *SpeedCurveJSON { return &SpeedCurveJSON{Points: pts, Audio: audio} }
	clips := []ClipJSON{
		// Red at 0.5x (2 s), then blue at 2x (~0.5 s).
		{ID: "ramp", In: 0, Out: 2000, SpeedCurve: curve("follow", SpeedPointJSON{0, 0.5}, SpeedPointJSON{1000, 0.5}, SpeedPointJSON{1001, 2}, SpeedPointJSON{2000, 2})},
		// 0.2 s of blue at 0.1x: 2 s, silent (below 0.25x).
		{ID: "bullet", In: 1000, Out: 1200, SpeedCurve: curve("follow", SpeedPointJSON{0, 0.1}, SpeedPointJSON{200, 0.1})},
		// The red frame at 0.5 s, held for 1 s, silent.
		{ID: "hold", In: 500, Out: 1500, Hold: true},
		// The whole source at 0.5x, motion-interpolated: 4 s, red → blue at 2 s.
		{ID: "smooth", In: 0, Out: 2000, Speed: 0.5, BlendFrames: "smooth"},
	}
	at := 0
	starts := map[string]float64{}
	for i := range clips {
		c := &clips[i]
		c.Media, c.Start, c.Volume, c.Transform = "tt", at, 1, full
		if c.Speed == 0 {
			c.Speed = 1
		}
		starts[c.ID] = float64(at) / 1000
		at += ClipLengthMs(*c)
	}
	total := float64(at) / 1000
	out := filepath.Join(dir, "time.mp4")
	req := ProjectExport{
		Project: ProjectJSON{
			Version: 3,
			Canvas:  CanvasJSON{Width: 320, Height: 180, FPS: 30},
			Media:   []MediaJSON{{ID: "tt", Path: src, Kind: "video", Duration: 2000, Width: 320, Height: 180, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}}},
			Tracks:  []TrackJSON{{ID: "v1", Kind: "video", Clips: clips}},
		},
		Preset:  "original",
		OutPath: out,
	}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := New().exportProject(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	if d := probeDur(t, out); math.Abs(d-total) > 0.1 {
		t.Fatalf("export is %.3fs, want %.3f", d, total)
	}
	for _, c := range []struct {
		clip string
		dt   float64
		want string
	}{
		{"ramp", 1.7, "red"},    // 0.5x: source 0.85 s
		{"ramp", 2.2, "blue"},   // past the switch at 2 s
		{"bullet", 1.0, "blue"}, //
		{"hold", 0.2, "red"},    // the held frame
		{"hold", 0.9, "red"},    // still held
		{"smooth", 1.7, "red"},  // 0.5x: source 0.85 s
		{"smooth", 2.4, "blue"}, // source 1.2 s
	} {
		if got := pixelAt(t, out, starts[c.clip]+c.dt, 160, 90); got != c.want {
			t.Errorf("%s +%.2fs: %s, want %s", c.clip, c.dt, got, c.want)
		}
	}
	if v := meanVolume(t, out, starts["ramp"]+0.5, 1.0); v < -30 {
		t.Errorf("0.5x ramp: %.1f dB, want the tone", v)
	}
	if v := meanVolume(t, out, starts["bullet"]+0.3, 1.4); v > -60 {
		t.Errorf("0.1x ramp: %.1f dB, want silence", v)
	}
	if v := meanVolume(t, out, starts["hold"]+0.2, 0.6); v > -60 {
		t.Errorf("freeze: %.1f dB, want silence", v)
	}
	if v := meanVolume(t, out, starts["smooth"]+0.5, 2.5); v < -30 {
		t.Errorf("smooth 0.5x: %.1f dB, want the tone", v)
	}
}
