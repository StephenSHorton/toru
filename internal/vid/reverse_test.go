package vid

import (
	"context"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestTrimReverseCacheDropsOldestFirst(t *testing.T) {
	dir := t.TempDir()
	old := reverseCacheDir
	reverseCacheDir = func() string { return dir }
	defer func() { reverseCacheDir = old }()
	now := time.Now()
	for i, name := range []string{"a.mp4", "b.mp4", "c.mp4"} {
		f := filepath.Join(dir, name)
		if err := os.WriteFile(f, make([]byte, 100), 0o644); err != nil {
			t.Fatal(err)
		}
		at := now.Add(time.Duration(i-3) * time.Hour) // a oldest
		_ = os.Chtimes(f, at, at)
	}
	trimReverseCache(200, filepath.Join(dir, "a.mp4"))
	_, errA := os.Stat(filepath.Join(dir, "a.mp4"))
	_, errB := os.Stat(filepath.Join(dir, "b.mp4"))
	_, errC := os.Stat(filepath.Join(dir, "c.mp4"))
	if errA != nil || errB == nil || errC != nil {
		t.Fatalf("kept a (just made) and c (newest), dropped b: a=%v b=%v c=%v", errA, errB, errC)
	}
	if n, _ := (&VideoService{}).ReverseCacheSize(); n != 200 {
		t.Fatalf("cache size %d, want 200", n)
	}
	if err := (&VideoService{}).ClearReverseCache(); err != nil {
		t.Fatal(err)
	}
	if len(reverseCacheFiles()) != 0 {
		t.Fatal("clear left files")
	}
}

// TestReverseProxyAndExport renders a proxy of a red→blue source through
// real ffmpeg (it must be blue→red, the same length, and cached on a second
// ask), then exports a project with that clip reversed under a speed curve.
func TestReverseProxyAndExport(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video; skipped with -short")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	dir := t.TempDir()
	old := reverseCacheDir
	reverseCacheDir = func() string { return filepath.Join(dir, "cache") }
	defer func() { reverseCacheDir = old }()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	// 3 s: red, blue, green, a tone throughout (so the chunks join).
	src := filepath.Join(dir, "rgb.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y", "-f", "lavfi", "-i", "color=c=red:s=320x180:r=30:d=1", "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d=1",
		"-f", "lavfi", "-i", "color=c=0x00ff00:s=320x180:r=30:d=1", "-f", "lavfi", "-i", "sine=f=660:duration=3",
		"-filter_complex", "[0:v][1:v][2:v]concat=n=3:v=1:a=0[v]", "-map", "[v]", "-map", "3:a",
		"-c:v", "libx264", "-g", "15", "-pix_fmt", "yuv420p", "-c:a", "aac", src}); err != nil {
		t.Skipf("generate source (needs lavfi + libx264): %v", err)
	}
	var last float64
	proxy, err := reverseProxy(ctx, src, 500, 3000, func(_ string, p float64) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if last != 1 {
		t.Errorf("progress ended at %v", last)
	}
	if d := probeDur(t, proxy); math.Abs(d-2.5) > 0.08 {
		t.Errorf("proxy is %.3fs, want 2.5", d)
	}
	// Proxy t shows source 3 - t: green, then blue, then red.
	for _, c := range []struct {
		at   float64
		want string
	}{{0.5, "green"}, {1.5, "blue"}, {2.3, "red"}} {
		if got := pixelAt(t, proxy, c.at, 160, 90); got != c.want {
			t.Errorf("proxy t=%.1f: %s, want %s", c.at, got, c.want)
		}
	}
	if v := meanVolume(t, proxy, 0.2, 2); v < -30 {
		t.Errorf("proxy audio %.1f dB, want the tone", v)
	}
	again, err := reverseProxy(ctx, src, 500, 3000, nil)
	if err != nil || again != proxy {
		t.Fatalf("second ask: %q %v, want the cached %q", again, err, proxy)
	}

	// Export: the whole source reversed, the first half (green) at 2x.
	full := TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}
	clip := ClipJSON{ID: "r", Media: "src", Start: 0, In: 0, Out: 3000, Speed: 1, Volume: 1, Transform: full, Reversed: true,
		SpeedCurve: &SpeedCurveJSON{Points: []SpeedPointJSON{{0, 2}, {1000, 2}, {1001, 1}, {3000, 1}}, Audio: "follow"}}
	out := filepath.Join(dir, "rev.mp4")
	req := ProjectExport{
		Project: ProjectJSON{
			Version: 3,
			Canvas:  CanvasJSON{Width: 320, Height: 180, FPS: 30},
			Media:   []MediaJSON{{ID: "src", Path: src, Kind: "video", Duration: 3000, Width: 320, Height: 180, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}}},
			Tracks:  []TrackJSON{{ID: "v1", Kind: "video", Clips: []ClipJSON{clip}}},
		},
		Preset:  "original",
		OutPath: out,
	}
	if _, err := New().exportProject(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	want := float64(ClipLengthMs(clip)) / 1000 // ~0.5 + 2
	if d := probeDur(t, out); math.Abs(d-want) > 0.1 {
		t.Errorf("export %.3fs, want %.3f", d, want)
	}
	for _, c := range []struct {
		at   float64
		want string
	}{{0.25, "green"}, {1.0, "blue"}, {2.2, "red"}} {
		if got := pixelAt(t, out, c.at, 160, 90); got != c.want {
			t.Errorf("export t=%.2f: %s, want %s", c.at, got, c.want)
		}
	}
}
