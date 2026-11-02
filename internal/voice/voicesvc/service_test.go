package voicesvc

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/fx/fxtest"
	"github.com/StephenSHorton/toru/internal/voice"
)

func TestSourceGraph(t *testing.T) {
	if g := sourceGraph(RenderRequest{Stream: 2}); !strings.HasPrefix(g, "[0:a:2]aresample=async=1:first_pts=0,") || !strings.HasSuffix(g, "[src]") {
		t.Fatal(g)
	}
	g := sourceGraph(RenderRequest{Levels: []Level{{Index: 1, Volume: 100}, {Index: 2, Volume: 50}, {Index: 3, Volume: 100, Muted: true}}})
	if !strings.Contains(g, "[0:a:1]") || !strings.Contains(g, "[0:a:2]") || strings.Contains(g, "[0:a:3]") ||
		!strings.Contains(g, "volume=0.50") || !strings.Contains(g, "amix=inputs=2:duration=longest:normalize=0[src]") {
		t.Fatal(g)
	}
	if g := sourceGraph(RenderRequest{Levels: []Level{{Index: 1, Volume: 100}}}); strings.Contains(g, "amix") {
		t.Fatal(g)
	}
	if g := sourceGraph(RenderRequest{Levels: []Level{{Index: 1, Muted: true}}}); !strings.HasPrefix(g, "anullsrc") {
		t.Fatal(g)
	}
}

func TestRenderRejects(t *testing.T) {
	s := New()
	if _, err := s.Render(RenderRequest{Path: "x", Preset: "nope"}); err == nil {
		t.Fatal("unknown preset")
	}
	if _, err := s.Render(RenderRequest{Path: "x", Preset: voice.Light, Start: -1}); err == nil {
		t.Fatal("negative start")
	}
	if _, err := s.Audition(RenderRequest{Path: "x"}); err == nil {
		t.Fatal("audition without a duration")
	}
}

// TestRenderSmoke runs every preset on a noisy test voice (a tone that
// comes and goes over pink noise and hum) with the ffmpeg Toru ships, and
// checks what each promises: the copy is as long as the source and lines up
// with it, the noise between "words" drops, and Voice polish lands near
// -16 LUFS.
func TestRenderSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg")
	}
	bin := fxtest.PinnedFFmpeg(t)
	t.Setenv("TORU_FFMPEG", bin)
	dir := t.TempDir()
	prev := outDir
	outDir = func() string { return filepath.Join(dir, "renders") }
	defer func() { outDir = prev }()

	// 12 s: "speech" (a 220 Hz buzz with harmonics, on 1-3 s, 5-7 s, 9-11 s)
	// over pink noise and 120 Hz hum, as an mp4 with a video stream, like a
	// recording.
	src := filepath.Join(dir, "take.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "color=c=gray:s=160x90:r=30:d=12",
		"-f", "lavfi", "-i", "sine=f=220:r=48000:d=12",
		"-f", "lavfi", "-i", "anoisesrc=c=pink:a=0.02:r=48000:d=12",
		"-f", "lavfi", "-i", "sine=f=120:r=48000:d=12",
		"-filter_complex", "[1:a]aeval=0.5*sin(2*PI*220*t)+0.25*sin(2*PI*440*t)+0.15*sin(2*PI*880*t),volume='if(between(mod(t\\,4)\\,1\\,3)\\,0.6\\,0)':eval=frame[v];[3:a]volume=0.01[h];[v][2:a][h]amix=inputs=3:normalize=0,aformat=channel_layouts=stereo[a]",
		"-map", "0:v", "-map", "[a]", "-c:v", "mpeg4", "-c:a", "aac", "-b:a", "192k", src}); err != nil {
		t.Fatal(err)
	}
	s := New()
	for _, p := range voice.Presets() {
		out, err := s.Render(RenderRequest{Path: src, Preset: p.ID})
		if err != nil {
			t.Fatalf("%s: %v", p.ID, err)
		}
		d := duration(t, bin, out)
		if math.Abs(d-12) > 0.05 {
			t.Errorf("%s: %.3f s, want 12", p.ID, d)
		}
		gap := rms(t, bin, out, 3.4, 4.6)
		voiced := rms(t, bin, out, 1.4, 2.6)
		t.Logf("%-9s noise %6.1f dB  voice %6.1f dB", p.ID, gap, voiced)
		if p.ID != voice.Off {
			off := rms(t, bin, filepath.Join(dir, "renders", mustRender(t, s, src, voice.Off)), 3.4, 4.6)
			if gap > off-3 {
				t.Errorf("%s: noise %.1f dB, off %.1f: not reduced", p.ID, gap, off)
			}
		}
		if voice.NeedsLoudness(p.ID) {
			if lufs := integrated(t, bin, out); math.Abs(lufs-voice.PolishLUFS) > 1.5 {
				t.Errorf("%s: %.1f LUFS, want %.0f", p.ID, lufs, voice.PolishLUFS)
			}
		}
		// Cached: asking again is instant and the same file.
		if again, err := s.Render(RenderRequest{Path: src, Preset: p.ID}); err != nil || again != out {
			t.Errorf("%s: second render %s %v", p.ID, again, err)
		}
	}

	// Off is the source untouched: sample for sample against the export's
	// own reading of the stream (lag 0).
	offPath := filepath.Join(dir, "renders", mustRender(t, s, src, voice.Off))
	a := pcm(t, bin, src, 0, 12)
	b := pcm(t, bin, offPath, 0, 12)
	if lag := bestLag(a, b, 2400); lag != 0 {
		t.Errorf("Off copy is %d samples off the source", lag)
	}

	// Audition: every preset, 3 s from 5 s.
	got, err := s.Audition(RenderRequest{Path: src, Start: 5, Duration: 3})
	if err != nil || len(got) != len(voice.Presets()) {
		t.Fatalf("audition: %v %v", got, err)
	}
	for id, p := range got {
		if d := duration(t, bin, p); math.Abs(d-3) > 0.05 {
			t.Errorf("audition %s: %.3f s", id, d)
		}
	}
}

func mustRender(t *testing.T, s *Service, src, preset string) string {
	t.Helper()
	p, err := s.Render(RenderRequest{Path: src, Preset: preset})
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Base(p)
}

func run(t *testing.T, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(bin, append([]string{"-hide_banner", "-nostdin"}, args...)...)
	capture.PrepareFFmpegCmd(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func duration(t *testing.T, bin, path string) float64 {
	out := run(t, bin, "-i", path, "-f", "null", "-")
	m := regexp.MustCompile(`time=(\d+):(\d+):([\d.]+)`).FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		t.Fatalf("no duration for %s", path)
	}
	l := m[len(m)-1]
	h, _ := strconv.ParseFloat(l[1], 64)
	mi, _ := strconv.ParseFloat(l[2], 64)
	s, _ := strconv.ParseFloat(l[3], 64)
	return h*3600 + mi*60 + s
}

func rms(t *testing.T, bin, path string, a, b float64) float64 {
	x := pcm(t, bin, path, a, b-a)
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	if len(x) == 0 || sum == 0 {
		return -120
	}
	return 10 * math.Log10(sum/float64(len(x)))
}

func integrated(t *testing.T, bin, path string) float64 {
	out := run(t, bin, "-i", path, "-af", "ebur128", "-f", "null", "-")
	m := regexp.MustCompile(`I:\s+(-?[\d.]+) LUFS`).FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		t.Fatalf("no loudness for %s", path)
	}
	v, _ := strconv.ParseFloat(m[len(m)-1][1], 64)
	return v
}

// pcm decodes [start, start+dur) of a file's first audio stream to mono
// float samples at 48 kHz. It cuts with atrim on the decoded stream, not
// -ss: seeking an AAC stream to 0 drops its first 1024 samples (the
// encoder's priming, which the edit list already accounts for).
func pcm(t *testing.T, bin, path string, start, dur float64) []float64 {
	t.Helper()
	cmd := exec.Command(bin, "-v", "error", "-nostdin", "-i", path, "-map", "0:a:0",
		"-af", fmt.Sprintf("atrim=start=%s:duration=%s,asetpts=PTS-STARTPTS", ff(start), ff(dur)),
		"-ac", "1", "-ar", "48000", "-f", "f32le", "-")
	capture.PrepareFFmpegCmd(cmd)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	b := buf.Bytes()
	out := make([]float64, len(b)/4)
	for i := range out {
		out[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:])))
	}
	return out
}

// bestLag is the shift of b against a (in samples, within ±max) where they
// match best.
func bestLag(a, b []float64, maxLag int) int {
	best, at := math.Inf(-1), 0
	n := min(len(a), len(b)) - 2*maxLag
	for lag := -maxLag; lag <= maxLag; lag++ {
		var s float64
		for i := maxLag; i < maxLag+n; i += 4 {
			s += a[i] * b[i+lag]
		}
		if s > best {
			best, at = s, lag
		}
	}
	return at
}
