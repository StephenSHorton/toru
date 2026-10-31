package vid

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// TestProjectExportVerticalFill exports a 16:9 clip on a 9:16 canvas the way
// the editor's frame change leaves it (frontend src/trim/canvas.ts): "fill"
// sets transform.scale to (16/9)^2, which must cover the whole frame with
// the picture's middle; "fit" (scale 1) leaves bars above and below.
//
//	go test ./internal/vid -run ProjectExportVerticalFill -v
func TestProjectExportVerticalFill(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video; skipped with -short")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	// 640x360: left third red, middle green, right third blue.
	src := filepath.Join(dir, "thirds.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "color=c=red:s=214x360:r=30:d=1", "-f", "lavfi", "-i", "color=c=0x00ff00:s=212x360:r=30:d=1",
		"-f", "lavfi", "-i", "color=c=blue:s=214x360:r=30:d=1", "-filter_complex", "[0:v][1:v][2:v]hstack=inputs=3[v]", "-map", "[v]",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src}); err != nil {
		t.Skipf("generate source (needs lavfi + libx264): %v", err)
	}
	s := New()
	export := func(name string, scale float64) string {
		out := filepath.Join(dir, name)
		req := ProjectExport{
			Project: ProjectJSON{
				Version: 3,
				Canvas:  CanvasJSON{Width: 360, Height: 640, FPS: 30},
				Media:   []MediaJSON{{ID: "m", Path: src, Kind: "video", Duration: 1000, Width: 640, Height: 360, FPS: 30}},
				Tracks: []TrackJSON{{ID: "v1", Kind: "video", Clips: []ClipJSON{{ID: "c", Media: "m", Start: 0, In: 0, Out: 1000, Speed: 1, Volume: 1,
					Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: scale, Opacity: 1}}}}},
			},
			Preset:  "original",
			OutPath: out,
		}
		if _, err := s.exportProject(ctx, req, nil); err != nil {
			t.Fatal(err)
		}
		return out
	}
	fill := export("fill.mp4", (640.0/360)*(640.0/360))
	if w, h := probeSize(t, fill); w != 360 || h != 640 {
		t.Fatalf("fill export is %dx%d, want 360x640", w, h)
	}
	for _, p := range [][2]int{{4, 4}, {355, 4}, {180, 320}, {4, 635}, {355, 635}} {
		if got := pixelAt(t, fill, 0.5, p[0], p[1]); got != "green" {
			t.Errorf("fill (%d,%d): %s, want green (the picture's middle, edge to edge)", p[0], p[1], got)
		}
	}
	fit := export("fit.mp4", 1)
	for _, c := range []struct {
		x, y int
		want string
	}{{180, 320, "green"}, {10, 320, "red"}, {350, 320, "blue"}, {180, 40, "black"}, {180, 600, "black"}} {
		if got := pixelAt(t, fit, 0.5, c.x, c.y); got != c.want {
			t.Errorf("fit (%d,%d): %s, want %s", c.x, c.y, got, c.want)
		}
	}
}

func probeSize(t *testing.T, path string) (int, int) {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height", "-of", "csv=p=0:s=x", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	var w, h int
	_, _ = fmt.Sscanf(strings.TrimSpace(string(out)), "%dx%d", &w, &h)
	return w, h
}
