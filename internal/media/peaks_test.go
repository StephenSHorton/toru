package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestPeakByte(t *testing.T) {
	for _, c := range []struct {
		v    int
		want byte
	}{{0, 0}, {32768, 255}, {32767, 255}, {33, 0}, {3277, 170}, {328, 85}} {
		if got := peakByte(c.v); got != c.want {
			t.Errorf("peakByte(%d) = %d, want %d", c.v, got, c.want)
		}
	}
}

// oneByteReader hands out a byte at a time, so samples split across reads.
type oneByteReader struct{ r io.Reader }

func (o oneByteReader) Read(p []byte) (int, error) { return o.r.Read(p[:1]) }

func TestPeaksFromPCM(t *testing.T) {
	var pcm bytes.Buffer
	for _, v := range []int16{1, -32768, 5, 2, 0, 0, 3277, -3277, 7} { // bins of 4: max 32768, 3277, 7
		_ = binary.Write(&pcm, binary.LittleEndian, v)
	}
	for _, r := range []io.Reader{bytes.NewReader(pcm.Bytes()), oneByteReader{bytes.NewReader(pcm.Bytes())}} {
		got, err := peaksFromPCM(r, 4)
		if err != nil {
			t.Fatal(err)
		}
		want := []byte{255, 170, peakByte(7)}
		if !bytes.Equal(got, want) {
			t.Fatalf("peaks = %v, want %v", got, want)
		}
	}
}

// TestPeaksLong times the peaks of a real long file (skipped unless
// TORU_PEAKS_FILE names one):
//
//	TORU_PEAKS_FILE=long.mp4 go test ./internal/media -run PeaksLong -v
func TestPeaksLong(t *testing.T) {
	f := os.Getenv("TORU_PEAKS_FILE")
	if f == "" {
		t.Skip("TORU_PEAKS_FILE not set")
	}
	t0 := time.Now()
	p, err := decodePeaks(context.Background(), f, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: %d bins (%.0f s of sound) in %s", filepath.Base(f), len(p), float64(len(p))/PeaksRate, time.Since(t0).Round(time.Millisecond))
}

// TestPeaksSmoke decodes 1 s of tone then 1 s of silence through ffmpeg and
// serves it. Run alone:
//
//	go test ./internal/media -run PeaksSmoke -v
func TestPeaksSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("runs ffmpeg; skipped with -short")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dir := t.TempDir()
	wav := filepath.Join(dir, "tone.wav")
	if err := capture.RunFFmpeg(ctx, []string{"-y", "-f", "lavfi", "-i", "sine=f=440:duration=1:sample_rate=48000",
		"-af", "apad=pad_dur=1,volume=0.5", wav}); err != nil {
		t.Skipf("generate test media: %v", err)
	}
	prev := peaksDir
	peaksDir = func() string { return filepath.Join(dir, "peaks") }
	defer func() { peaksDir = prev }()

	s := New()
	m := s.Register([]string{wav})
	if len(m) != 1 {
		t.Fatalf("register: %+v", m)
	}
	srv := httptest.NewServer(s.Middleware()(http.NotFoundHandler()))
	defer srv.Close()
	res, err := http.Get(srv.URL + peaksPrefix + m[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("X-Peaks-Rate") != "100" {
		t.Fatalf("status %d rate %q: %s", res.StatusCode, res.Header.Get("X-Peaks-Rate"), body)
	}
	if len(body) < 195 || len(body) > 205 {
		t.Fatalf("%d bins for 2 s, want ~200", len(body))
	}
	// sine at volume 0.5 is about -6 dB (ffmpeg's sine is at 1/8 amplitude:
	// -18 dB, then -6: about -24 dB → (60-24)/60*255 ≈ 153).
	for i := 10; i < 90; i++ {
		if body[i] < 130 || body[i] > 175 {
			t.Fatalf("tone bin %d = %d", i, body[i])
		}
	}
	for i := 110; i < len(body); i++ {
		if body[i] != 0 {
			t.Fatalf("silent bin %d = %d", i, body[i])
		}
	}
	// Cached: a second request doesn't decode again.
	files, _ := filepath.Glob(filepath.Join(dir, "peaks", "*.peaks"))
	if len(files) != 1 {
		t.Fatalf("cache files %v", files)
	}
	st, _ := os.Stat(files[0])
	res, err = http.Get(srv.URL + peaksPrefix + m[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	st2, _ := os.Stat(files[0])
	if !st2.ModTime().Equal(st.ModTime()) {
		t.Fatal("peaks were made again")
	}
	if res, _ = http.Get(srv.URL + peaksPrefix + "nope"); res.StatusCode != 404 {
		t.Fatalf("unknown id: %d", res.StatusCode)
	}
}
