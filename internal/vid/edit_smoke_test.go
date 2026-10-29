package vid

import (
	"bytes"
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// TestEditExportSmoke runs BuildEditArgs through real ffmpeg: two synthetic
// sources of different size, rate and audio layout, three segments out of
// order, a muted track, a crop and a preset, then checks the result with
// ffprobe. It needs ffmpeg and ffprobe with lavfi; otherwise it skips.
//
//	go test ./internal/vid -run EditExportSmoke -v
func TestEditExportSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video; skipped with -short")
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skipf("ffprobe not on PATH: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := capture.RunFFmpeg(ctx, []string{
		"-hide_banner", "-f", "lavfi", "-i", "nullsrc=size=16x16", "-frames:v", "1", "-f", "null", "-",
	}); err != nil {
		t.Skipf("ffmpeg has no lavfi device: %v", err)
	}

	dir := t.TempDir()
	// a: 640x360@60, 4s, three tracks like a separate-tracks clip (mix,
	// game = 440Hz, mic = 3kHz). b: 320x240@30, 2s, no audio.
	a := filepath.Join(dir, "a.mp4")
	b := filepath.Join(dir, "b.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=60:duration=4",
		"-f", "lavfi", "-i", "sine=f=440:duration=4",
		"-f", "lavfi", "-i", "sine=f=3000:duration=4",
		"-filter_complex", "[1:a][2:a]amix=inputs=2:normalize=0[mix]",
		"-map", "0:v", "-map", "[mix]", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-g", "30", "-pix_fmt", "yuv420p", "-c:a", "aac", a,
	}); err != nil {
		t.Skipf("generate source a (needs libx264): %v", err)
	}
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=30:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", b,
	}); err != nil {
		t.Fatalf("generate source b: %v", err)
	}
	before := hashFiles(t, a, b)

	info := []MediaInfo{
		{DurationMs: 4000, Width: 640, Height: 360, FPS: 60, HasAudio: true,
			Tracks: []AudioTrack{{Index: 0, IsMix: true}, {Index: 1, Title: "Game"}, {Index: 2, Title: "Mic"}}},
		{DurationMs: 2000, Width: 320, Height: 240, FPS: 30},
	}
	cases := []struct {
		name         string
		req          EditRequest
		wantW, wantH int
		wantMs       int
		mutedMic     bool // check the 3kHz mic is gone and the 440Hz game stays
	}{
		{
			name: "cuts and join, mic muted",
			req: EditRequest{Sources: []string{a, b},
				Segments: []Segment{{0, 2500, 3500}, {1, 500, 1500}, {0, 0, 1000}},
				Tracks:   []TrackLevel{{Source: 0, Index: 1, Volume: 120}, {Source: 0, Index: 2, Volume: 100, Muted: true}},
				Preset:   "original", OutPath: filepath.Join(dir, "join.mp4")},
			wantW: 640, wantH: 360, wantMs: 3000, mutedMic: true,
		},
		{
			name: "crop then custom height",
			req: EditRequest{Sources: []string{a}, Segments: []Segment{{0, 1000, 3000}},
				Crop:   &capture.Rect{X: 100, Y: 40, W: 400, H: 300},
				Preset: "custom", Height: 150, Kbps: 800, OutPath: filepath.Join(dir, "crop.webm")},
			wantW: 200, wantH: 150, wantMs: 2000,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.req.Validate(info[:len(c.req.Sources)]); err != nil {
				t.Fatal(err)
			}
			args := BuildEditArgs(c.req, info[:len(c.req.Sources)], capture.TrimH264Encoder(), "")
			t.Logf("ffmpeg %s", strings.Join(args, " "))
			if err := capture.RunFFmpegProgress(ctx, args, EditDurationMs(c.req, info), nil); err != nil {
				t.Fatalf("export: %v", err)
			}
			v := probe(t, ctx, ffprobe, c.req.OutPath, "v:0", "stream=width,height,r_frame_rate")
			want := strconv.Itoa(c.wantW) + "," + strconv.Itoa(c.wantH) + ",60/1"
			if v != want {
				t.Errorf("video stream = %q, want %q", v, want)
			}
			if n := strings.Count(probe(t, ctx, ffprobe, c.req.OutPath, "a", "stream=index"), "\n") + 1; n != 1 {
				t.Errorf("%d audio streams, want 1", n)
			}
			durS, err := strconv.ParseFloat(probe(t, ctx, ffprobe, c.req.OutPath, "", "format=duration"), 64)
			if err != nil {
				t.Fatal(err)
			}
			// Within a frame or two of the kept length (AAC priming adds a few ms).
			if got := int(durS * 1000); got < c.wantMs-40 || got > c.wantMs+80 {
				t.Errorf("duration %d ms, want %d", got, c.wantMs)
			}
			if c.mutedMic {
				if db := peakDB(t, ctx, c.req.OutPath, "highpass=f=1500,highpass=f=1500"); db > -35 {
					t.Errorf("mic band peaks at %.1f dB: the muted track is in the export", db)
				}
				if db := peakDB(t, ctx, c.req.OutPath, "lowpass=f=1000,lowpass=f=1000"); db < -25 {
					t.Errorf("game band peaks at %.1f dB: the game track is missing", db)
				}
			}
		})
	}
	if after := hashFiles(t, a, b); !bytes.Equal(before, after) {
		t.Error("a source file changed")
	}
}

func probe(t *testing.T, ctx context.Context, ffprobe, path, stream, entries string) string {
	t.Helper()
	args := []string{"-v", "error"}
	if stream != "" {
		args = append(args, "-select_streams", stream)
	}
	args = append(args, "-show_entries", entries, "-of", "csv=p=0", path)
	out, err := exec.CommandContext(ctx, ffprobe, args...).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	return strings.TrimSpace(strings.ReplaceAll(string(out), "\r", ""))
}

// peakDB is the loudest sample of path's audio after filter, in dBFS.
func peakDB(t *testing.T, ctx context.Context, path, filter string) float64 {
	t.Helper()
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, ffmpeg, "-hide_banner", "-i", path, "-map", "0:a",
		"-af", filter+",volumedetect", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("volumedetect: %v\n%s", err, out)
	}
	_, rest, ok := strings.Cut(string(out), "max_volume: ")
	if !ok {
		t.Fatalf("no max_volume in:\n%s", out)
	}
	db, err := strconv.ParseFloat(strings.Fields(rest)[0], 64)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s peak after %s: %.1f dB", filepath.Base(path), filter, db)
	return db
}

func hashFiles(t *testing.T, paths ...string) []byte {
	t.Helper()
	h := sha256.New()
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(b)
	}
	return h.Sum(nil)
}
