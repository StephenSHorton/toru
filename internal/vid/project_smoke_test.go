package vid

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// TestProjectExportSmoke runs ExportProject's pipeline through real ffmpeg
// and checks the result by sampling pixels and audio levels:
//
//  1. a red main clip, a green picture-in-picture clip on a higher track, a
//     blue PNG on top, and music on an audio track: layer order, where each
//     sits, the black gap after the main clip, and the music's window.
//  2. two clips at 0.5x and 2x: the timeline length and the colour change
//     inside each clip landing where the speed puts it, with the audio
//     stretched to match.
//
// It needs ffmpeg and ffprobe with lavfi; otherwise it skips.
//
//	go test ./internal/vid -run ProjectExportSmoke -v
func TestProjectExportSmoke(t *testing.T) {
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
	gen := func(name string, args ...string) string {
		t.Helper()
		out := filepath.Join(dir, name)
		if err := capture.RunFFmpeg(ctx, append(append([]string{"-y"}, args...), out)); err != nil {
			t.Skipf("generate %s (needs lavfi + libx264): %v", name, err)
		}
		return out
	}
	red := gen("red.mp4", "-f", "lavfi", "-i", "color=c=red:s=640x360:r=30:d=3", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "3", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest")
	green := gen("green.mp4", "-f", "lavfi", "-i", "color=c=0x00ff00:s=320x240:r=30:d=2", "-c:v", "libx264", "-pix_fmt", "yuv420p")
	blue := gen("blue.png", "-f", "lavfi", "-i", "color=c=blue:s=100x100", "-frames:v", "1")
	music := gen("music.m4a", "-f", "lavfi", "-i", "sine=f=440:duration=10", "-c:a", "aac")

	s := New()
	out1 := filepath.Join(dir, "layers.mp4")
	req := ProjectExport{
		Project: ProjectJSON{
			Version: 2,
			Canvas:  CanvasJSON{Width: 640, Height: 360, FPS: 30},
			Media: []MediaJSON{
				{ID: "red", Path: red, Kind: "video", Duration: 3000, Width: 640, Height: 360, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}},
				{ID: "green", Path: green, Kind: "video", Duration: 2000, Width: 320, Height: 240, FPS: 30},
				{ID: "blue", Path: blue, Kind: "image", Width: 100, Height: 100},
				{ID: "music", Path: music, Kind: "audio", Duration: 10000, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}},
			},
			Tracks: []TrackJSON{
				// Main 0..3s, then a gap to 4s (the music runs on).
				{ID: "v1", Kind: "video", Clips: []ClipJSON{{ID: "m", Media: "red", Start: 0, In: 0, Out: 3000, Speed: 1, Volume: 1,
					Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}}}},
				// PiP 0.5..2.5s, top-right quarter: 320x240 fits 480x360 -> 50% = 240x180 centred at (480, 90).
				{ID: "v2", Kind: "video", Clips: []ClipJSON{{ID: "p", Media: "green", Start: 500, In: 0, Out: 2000, Speed: 1, Volume: 1,
					Transform: TransformJSON{X: 0.75, Y: 0.25, Scale: 0.5, Opacity: 1}}}},
				// Logo 1..2s, overlapping the PiP's left part: 100x100 fits 360x360 -> 1/3 = 120x120 at centre (400, 90).
				{ID: "v3", Kind: "video", Clips: []ClipJSON{{ID: "l", Media: "blue", Start: 1000, In: 0, Out: 1000, Speed: 1, Volume: 1,
					Transform: TransformJSON{X: 0.625, Y: 0.25, Scale: 1.0 / 3, Opacity: 1}}}},
				// Music 1..4s.
				{ID: "a1", Kind: "audio", Clips: []ClipJSON{{ID: "s", Media: "music", Start: 1000, In: 2000, Out: 5000, Speed: 1, Volume: 1}}},
			},
		},
		Preset:  "original",
		OutPath: out1,
	}
	if _, err := s.exportProject(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	if d := probeDur(t, out1); d < 3.9 || d > 4.15 {
		t.Fatalf("layers export is %.3fs, want 4", d)
	}
	for _, c := range []struct {
		at   float64
		x, y int
		want string
	}{
		{0.25, 100, 300, "red"},  // main only
		{0.25, 480, 90, "red"},   // PiP not on yet
		{0.75, 560, 90, "green"}, // PiP over main
		{0.75, 100, 300, "red"},  // main outside the PiP
		{1.5, 400, 90, "blue"},   // logo over PiP
		{1.5, 560, 90, "green"},  // PiP beside the logo
		{2.75, 560, 90, "red"},   // PiP ended: main shows again (eof_action=pass)
		{3.5, 320, 180, "black"}, // gap after the main clip
	} {
		if got := pixelAt(t, out1, c.at, c.x, c.y); got != c.want {
			t.Errorf("t=%.2f (%d,%d): %s, want %s", c.at, c.x, c.y, got, c.want)
		}
	}
	if v := meanVolume(t, out1, 0.2, 0.6); v > -60 {
		t.Errorf("before the music: %.1f dB, want silence", v)
	}
	if v := meanVolume(t, out1, 1.5, 1.0); v < -30 {
		t.Errorf("music window: %.1f dB, want the tone", v)
	}

	// Speed: a 2s source whose first second is red and second blue, at
	// 0.5x (0..4s on the timeline) then at 2x (4..5s). The tone runs the
	// whole source, so the audio covers each stretched clip.
	twoTone := gen("twotone.mp4", "-f", "lavfi", "-i", "color=c=red:s=320x180:r=30:d=1", "-f", "lavfi", "-i", "color=c=blue:s=320x180:r=30:d=1",
		"-f", "lavfi", "-i", "sine=f=660:duration=2",
		"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]", "-map", "[v]", "-map", "2:a",
		"-c:v", "libx264", "-g", "15", "-pix_fmt", "yuv420p", "-c:a", "aac")
	out2 := filepath.Join(dir, "speed.mp4")
	media := MediaJSON{ID: "tt", Path: twoTone, Kind: "video", Duration: 2000, Width: 320, Height: 180, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}}
	full := TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}
	req2 := ProjectExport{
		Project: ProjectJSON{
			Version: 2,
			Canvas:  CanvasJSON{Width: 320, Height: 180, FPS: 30},
			Media:   []MediaJSON{media},
			Tracks: []TrackJSON{{ID: "v1", Kind: "video", Clips: []ClipJSON{
				{ID: "slow", Media: "tt", Start: 0, In: 0, Out: 2000, Speed: 0.5, Volume: 1, Transform: full},
				{ID: "fast", Media: "tt", Start: 4000, In: 0, Out: 2000, Speed: 2, Volume: 1, Transform: full},
			}}},
		},
		Preset:  "original",
		OutPath: out2,
	}
	if _, err := s.exportProject(ctx, req2, nil); err != nil {
		t.Fatal(err)
	}
	if d := probeDur(t, out2); d < 4.9 || d > 5.15 {
		t.Fatalf("speed export is %.3fs, want 5", d)
	}
	for _, c := range []struct {
		at   float64
		want string
	}{
		{1.5, "red"},   // 0.5x: source 0.75s
		{2.5, "blue"},  // 0.5x: source 1.25s (at 1x this would be past the clip)
		{4.25, "red"},  // 2x: source 0.5s
		{4.75, "blue"}, // 2x: source 1.5s
	} {
		if got := pixelAt(t, out2, c.at, 160, 90); got != c.want {
			t.Errorf("speed t=%.2f: %s, want %s", c.at, got, c.want)
		}
	}
	// The 0.5x clip's audio lasts its 4s (at 1x it would stop at 2s), and
	// the 2x clip's fills 4..5s.
	if v := meanVolume(t, out2, 3.2, 0.6); v < -30 {
		t.Errorf("0.5x audio at 3.2s: %.1f dB, want the tone", v)
	}
	if v := meanVolume(t, out2, 4.2, 0.6); v < -30 {
		t.Errorf("2x audio at 4.2s: %.1f dB, want the tone", v)
	}
}

func probeDur(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	d, _ := strconv.ParseFloat(string(bytes.TrimSpace(out)), 64)
	return d
}

// pixelAt names the colour of one pixel of the frame at time at.
func pixelAt(t *testing.T, path string, at float64, x, y int) string {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-v", "error", "-ss", fmt.Sprintf("%.3f", at), "-i", path, "-frames:v", "1",
		"-vf", fmt.Sprintf("format=rgb24,crop=1:1:%d:%d", x, y), "-f", "rawvideo", "-pix_fmt", "rgb24", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || len(out) < 3 {
		t.Fatalf("sample %s at %.2f: %v %s", path, at, err, stderr.String())
	}
	r, g, b := int(out[0]), int(out[1]), int(out[2])
	switch {
	case r > 180 && g < 80 && b < 80:
		return "red"
	case g > 180 && r < 80 && b < 80:
		return "green"
	case b > 180 && r < 80 && g < 80:
		return "blue"
	case r < 40 && g < 40 && b < 40:
		return "black"
	}
	return fmt.Sprintf("rgb(%d,%d,%d)", r, g, b)
}

var meanVolumeRe = regexp.MustCompile(`mean_volume: (-?[\d.]+|-inf) dB`)

// meanVolume is the mean level (dB) of the audio from `from` for `dur`
// seconds; -200 for silence.
func meanVolume(t *testing.T, path string, from, dur float64) float64 {
	t.Helper()
	cmd := exec.Command("ffmpeg", "-hide_banner", "-ss", fmt.Sprintf("%.3f", from), "-t", fmt.Sprintf("%.3f", dur), "-i", path,
		"-map", "0:a", "-af", "volumedetect", "-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	m := meanVolumeRe.FindSubmatch(out)
	if m == nil {
		t.Fatalf("volumedetect %s: no mean_volume in\n%s", path, out)
	}
	if string(m[1]) == "-inf" {
		return -200
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}
