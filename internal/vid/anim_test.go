package vid

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestAnimExportInFFmpeg exports a white clip on the black canvas with each
// anim target keyed and measures the frames (and the audio) at t = 0.5 s:
// where the clip's centre is, how big it is, how bright, how loud. Skipped
// without ffmpeg ($TORU_FFMPEG or PATH).
func TestAnimExportInFFmpeg(t *testing.T) {
	ff := os.Getenv("TORU_FFMPEG")
	if ff == "" {
		var err error
		if ff, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg (set TORU_FFMPEG or put it on PATH)")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "white.mkv")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "color=white:s=320x180:r=30:d=1.2", "-f", "lavfi", "-i", "sine=f=1000:d=1.2",
		"-c:v", "ffv1", "-c:a", "pcm_s16le", src).CombinedOutput(); err != nil {
		t.Fatalf("make source: %v\n%s", err, out)
	}
	ramp := func(a, b float64) []KeyframeJSON {
		return []KeyframeJSON{{T: 0, V: a, Ease: "linear"}, {T: 1000, V: b, Ease: "linear"}}
	}
	const W, H = 640, 360
	export := func(name string, mod func(*ClipJSON)) (frame []byte, audio []float32) {
		c := ClipJSON{ID: "c", Media: "m", Start: 0, In: 0, Out: 1200, Speed: 1, Volume: 1,
			Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 0.5, Opacity: 1}, Effects: []EffectJSON{}}
		mod(&c)
		p := ProjectJSON{Version: 3, Canvas: CanvasJSON{Width: W, Height: H, FPS: 30},
			Media:  []MediaJSON{{ID: "m", Path: src, Kind: "video", Duration: 1200, Width: 320, Height: 180, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}}},
			Tracks: []TrackJSON{{ID: "v", Kind: "video", Clips: []ClipJSON{c}}}}
		out := filepath.Join(dir, name+".mkv")
		args, err := BuildProjectArgs(ProjectExport{Project: p, Preset: "original", OutPath: out}, "ffv1", "", dir)
		if err != nil {
			t.Fatal(err)
		}
		args = append(args[:len(args)-1], "-c:a", "pcm_f32le", out)
		if b, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: export: %v\n%s\n%q", name, err, b, args)
		}
		frame, err = exec.Command(ff, "-v", "error", "-i", out, "-vf", "select=eq(n\\,15)", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "gray", "-").Output()
		if err != nil || len(frame) != W*H {
			t.Fatalf("%s: read frame: %v (%d bytes)", name, err, len(frame))
		}
		raw, err := exec.Command(ff, "-v", "error", "-i", out, "-map", "0:a", "-ac", "1", "-f", "f32le", "-").Output()
		if err != nil {
			t.Fatalf("%s: read audio: %v", name, err)
		}
		for i := 0; i+4 <= len(raw); i += 4 {
			audio = append(audio, math.Float32frombits(binary.LittleEndian.Uint32(raw[i:])))
		}
		return frame, audio
	}
	// box is the bounding box of the bright pixels and their mean level.
	box := func(f []byte) (x0, y0, x1, y1 int, mean float64) {
		x0, y0, x1, y1 = W, H, -1, -1
		n, sum := 0, 0
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				if v := f[y*W+x]; v > 40 {
					x0, y0, x1, y1 = min(x0, x), min(y0, y), max(x1, x), max(y1, y)
					n, sum = n+1, sum+int(v)
				}
			}
		}
		if n > 0 {
			mean = float64(sum) / float64(n)
		}
		return
	}
	near := func(name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %v, want %v ± %v", name, got, want, tol)
		}
	}

	f, _ := export("x", func(c *ClipJSON) { c.Anim = map[string][]KeyframeJSON{"x": ramp(0.25, 0.55)} })
	x0, _, x1, _, _ := box(f)
	near("x keys: centre at t=0.5", float64(x0+x1+1)/2, 0.4*W, 1.5)

	f, _ = export("scale", func(c *ClipJSON) { c.Anim = map[string][]KeyframeJSON{"scale": ramp(0.5, 1)} })
	x0, y0, x1, y1, _ := box(f)
	near("scale keys: width at t=0.5", float64(x1-x0+1), 480, 2)
	near("scale keys: height at t=0.5", float64(y1-y0+1), 270, 2)
	near("scale keys: centre x", float64(x0+x1+1)/2, W/2, 1.5)

	f, _ = export("rotation", func(c *ClipJSON) { c.Transform.Rotation = 90 })
	x0, y0, x1, y1, _ = box(f)
	near("rotation 90: width", float64(x1-x0+1), 180, 2)
	near("rotation 90: height", float64(y1-y0+1), 320, 2)
	near("rotation 90: centre y", float64(y0+y1+1)/2, H/2, 1.5)

	f, _ = export("rotkeys", func(c *ClipJSON) { c.Anim = map[string][]KeyframeJSON{"rotation": ramp(0, 180)} })
	x0, y0, x1, y1, _ = box(f)
	near("rotation keys at 90°: width", float64(x1-x0+1), 180, 3)
	near("rotation keys at 90°: height", float64(y1-y0+1), 320, 3)

	f, _ = export("opacity", func(c *ClipJSON) { c.Anim = map[string][]KeyframeJSON{"opacity": ramp(0, 1)} })
	_, _, _, _, mean := box(f)
	near("opacity keys: level at t=0.5", mean, 127.5, 3)

	_, a := export("volume", func(c *ClipJSON) { c.Anim = map[string][]KeyframeJSON{"volume": ramp(0, 1)} })
	peak := func(from, to float64) float64 {
		p := 0.0
		for i := int(from * 48000); i < int(to*48000) && i < len(a); i++ {
			p = math.Max(p, math.Abs(float64(a[i])))
		}
		return p
	}
	full := peak(1.05, 1.15)
	if full < 0.05 {
		t.Fatalf("volume keys: no signal at the end (peak %v of %d samples)", full, len(a))
	}
	near("volume keys: level at t=0.5 (of full)", peak(0.49, 0.51)/full, 0.5, 0.03)
	near("volume keys: level at t=0.1 (of full)", peak(0.09, 0.11)/full, 0.1, 0.03)
}
