package vid

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// TestV3EffectsExportInFFmpeg exports a v3 project with one clip per effect
// type (the time-based ones inside their windows) through BuildProjectArgs
// and a real ffmpeg: the quoted expressions must survive the whole
// -filter_complex, and per-frame scaling must feed the overlay. It uses
// $TORU_FFMPEG (point it at the pinned LGPL build) or ffmpeg on PATH, and
// is skipped without either.
func TestV3EffectsExportInFFmpeg(t *testing.T) {
	ff := os.Getenv("TORU_FFMPEG")
	if ff == "" {
		var err error
		if ff, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg (set TORU_FFMPEG or put it on PATH)")
		}
	}
	half := 0.5
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=640x360:r=30:d=2", "-c:v", "ffv1", src).CombinedOutput(); err != nil {
		t.Fatalf("make source: %v\n%s", err, out)
	}
	stacks := [][]EffectJSON{
		{{Type: "zoom", Params: map[string]any{"scale": 1.8, "center": []any{0.3, 0.4}}}},
		{{Type: "shake", Params: map[string]any{"amplitude": 30.0, "rotation": 4.0}}},
		{{Type: "flash", Params: map[string]any{"start": 0.2}}},
		{{Type: "gaussianBlur", Params: map[string]any{"sigma": 6.0}}},
		{{Type: "rgbSplit", Params: map[string]any{"amount": 10.0, "angle": 30.0}}},
		{{Type: "sharpen", Params: map[string]any{}}},
		{{Type: "chromaKey", Params: map[string]any{}}, {Type: "zoom", Params: map[string]any{}}, {Type: "gaussianBlur", Params: map[string]any{}}},
		{{Type: "glitch", Params: map[string]any{"density": 0.8}}},
		{{Type: "glitch", Params: map[string]any{"seed": 41.0, "slices": 6.0}, Group: "g", Amount: &half}, {Type: "zoom", Params: map[string]any{}, Group: "g", Amount: &half}},
	}
	p := ProjectJSON{
		Version: 3,
		Canvas:  CanvasJSON{Width: 1280, Height: 720, FPS: 30},
		Media:   []MediaJSON{{ID: "m", Path: src, Kind: "video", Duration: 2000, Width: 640, Height: 360, FPS: 30}},
	}
	for i, fx := range stacks {
		for k := range fx {
			fx[k].ID, fx[k].Enabled = fmt.Sprintf("e%d_%d", i, k), true
		}
		p.Tracks = append(p.Tracks, TrackJSON{ID: fmt.Sprintf("v%d", i), Kind: "video", Clips: []ClipJSON{{
			ID: fmt.Sprintf("c%d", i), Media: "m", Start: i * 200, In: 0, Out: 1500, Speed: 1, Volume: 1,
			Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 0.6, Opacity: 1}, Effects: fx,
		}}})
	}
	req := ProjectExport{Project: p, Preset: "original", OutPath: filepath.Join(dir, "out.mkv")}
	args, err := BuildProjectArgs(req, "ffv1", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.Index(args, "-filter_complex")
	if i < 0 {
		t.Fatalf("no -filter_complex in %q", args)
	}
	t.Logf("filter_complex: %d bytes for %d clips", len(args[i+1]), len(stacks))
	if out, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("export: %v\n%s\ngraph: %s", err, out, args[i+1])
	}

	// Twice the clips: past maxInlineGraph, so the graph goes in a file.
	p.Tracks = append(p.Tracks, p.Tracks...)
	req.Project = p
	if args, err = BuildProjectArgs(req, "ffv1", "", dir); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, "-/filter_complex") {
		t.Fatalf("a long graph should go in a file: %q", args)
	}
	if out, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("export with a graph file: %v\n%s", err, out)
	}
}
