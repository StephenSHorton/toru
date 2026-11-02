package vid

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"math"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/voice"
)

// TestVoiceTakeExport puts a mic voiceover take, written by internal/voice
// exactly as the editor streams it, on a "Voice" audio track under a silent
// video and exports: the tone must sound where the take sits on the
// timeline (start 1.5 s, its first 40 ms trimmed for latency) and nowhere
// else (Irori #129).
//
//	go test ./internal/vid -run VoiceTakeExport -v
func TestVoiceTakeExport(t *testing.T) {
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
	vid := filepath.Join(dir, "silent.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y", "-f", "lavfi", "-i", "color=c=red:s=320x180:r=30:d=4", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo",
		"-t", "4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", vid}); err != nil {
		t.Skipf("generate the video (needs lavfi + libx264): %v", err)
	}

	// A 1 s 1 kHz take at 48 kHz, sent in 0.25 s chunks like the editor.
	const rate = 48000
	vs := voice.NewAt(filepath.Join(dir, "voice"))
	id, err := vs.Begin("proj_test", rate)
	if err != nil {
		t.Fatal(err)
	}
	for c := 0; c < 4; c++ {
		b := make([]byte, rate/4*2)
		for i := 0; i < rate/4; i++ {
			n := c*rate/4 + i
			binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(0.5*32767*math.Sin(2*math.Pi*1000*float64(n)/rate))))
		}
		if err := vs.Append(id, base64.StdEncoding.EncodeToString(b)); err != nil {
			t.Fatal(err)
		}
	}
	take, err := vs.Finish(id)
	if err != nil {
		t.Fatal(err)
	}
	if d := probeDur(t, take); d < 0.99 || d > 1.01 {
		t.Fatalf("take is %.3fs, want 1", d)
	}

	out := filepath.Join(dir, "voiceover.mp4")
	full := TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}
	req := ProjectExport{
		Project: ProjectJSON{
			Version: 3,
			Canvas:  CanvasJSON{Width: 320, Height: 180, FPS: 30},
			Media: []MediaJSON{
				{ID: "v", Path: vid, Kind: "video", Duration: 4000, Width: 320, Height: 180, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}},
				{ID: "take", Path: take, Kind: "audio", Duration: 1000, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}},
			},
			Tracks: []TrackJSON{
				{ID: "v1", Kind: "video", Clips: []ClipJSON{{ID: "m", Media: "v", Start: 0, In: 0, Out: 4000, Speed: 1, Volume: 1, Transform: full}}},
				{ID: "a1", Kind: "audio"},
				{ID: "voice", Kind: "audio", Clips: []ClipJSON{{ID: "t", Media: "take", Start: 1500, In: 40, Out: 1000, Speed: 1, Volume: 1}}},
			},
		},
		Preset:  "original",
		OutPath: out,
	}
	if _, err := New().exportProject(ctx, req, nil); err != nil {
		t.Fatal(err)
	}
	if d := probeDur(t, out); d < 3.9 || d > 4.15 {
		t.Fatalf("export is %.3fs, want 4", d)
	}
	if v := meanVolume(t, out, 0.2, 1.1); v > -60 {
		t.Errorf("before the take: %.1f dB, want silence", v)
	}
	if v := meanVolume(t, out, 1.6, 0.8); v < -20 {
		t.Errorf("during the take: %.1f dB, want the tone", v)
	}
	if v := meanVolume(t, out, 2.6, 1.2); v > -60 {
		t.Errorf("after the take: %.1f dB, want silence", v)
	}
}
