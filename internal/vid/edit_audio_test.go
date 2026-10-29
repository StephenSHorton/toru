package vid

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestPreviewTrackFile(t *testing.T) {
	for u, want := range map[string]string{
		"/__file/editaudio-0a1b2c3d4e5f-2.m4a": "editaudio-0a1b2c3d4e5f-2.m4a",
		"/__file/editaudio-x-0.m4a":            "editaudio-x-0.m4a",
		"/__file/clip.mp4":                     "",
		"/__file/editaudio-x-0.mp4":            "",
		"/__file/..%5Ceditaudio-x-0.m4a":       "",
		"/__file/editaudio-..%2F..%2Fboot.m4a": "",
		"/__replayclip/editaudio-x-0.m4a":      "",
		"C:\\Users\\me\\editaudio-x-0.m4a":     "",
	} {
		got, ok := previewTrackFile(u)
		if (want == "") == ok || got != want {
			t.Errorf("previewTrackFile(%q) = %q, %v; want %q", u, got, ok, want)
		}
	}
}

func TestPreviewTrackNameChangesWithSource(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi1, _ := os.Stat(p)
	n0, n1 := previewTrackName(p, fi1, 0), previewTrackName(p, fi1, 1)
	if n0 == n1 || !strings.HasPrefix(n0, previewTrackPrefix) || !strings.HasSuffix(n1, "-1.m4a") {
		t.Fatalf("names %q %q", n0, n1)
	}
	if err := os.WriteFile(p, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi2, _ := os.Stat(p)
	if previewTrackName(p, fi2, 0) == n0 {
		t.Fatal("a changed source kept its old preview file")
	}
}

func TestSweepPreviewTracks(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	old := now.Add(-previewTrackMaxAge - time.Minute)
	files := map[string]time.Time{
		"editaudio-old-0.m4a":      old,
		"editaudio-old-1.part.m4a": old,
		"editaudio-new-0.m4a":      now,
		"recording-old.mp4":        old, // not ours
	}
	for name, mt := range files {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
	sweepPreviewTracks(dir, now)
	assertOnly(t, dir, "editaudio-new-0.m4a", "recording-old.mp4")
}

// TestPreviewTrackSmoke extracts each track of a generated separate-tracks
// clip, checks reuse and release, through real ffmpeg (skips without lavfi).
//
//	go test ./internal/vid -run PreviewTrackSmoke -v
func TestPreviewTrackSmoke(t *testing.T) {
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
	src := filepath.Join(dir, "clip.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=f=440:duration=2",
		"-f", "lavfi", "-i", "sine=f=3000:duration=2",
		"-map", "0:v", "-map", "1:a", "-map", "2:a",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", src,
	}); err != nil {
		t.Skipf("generate source (needs lavfi + libx264): %v", err)
	}

	out := filepath.Join(dir, "tmp")
	prev := previewDir
	previewDir = func() string { return out }
	defer func() { previewDir = prev }()

	s := New()
	u0, err := s.PreviewTrack(src, 0)
	if err != nil {
		t.Fatal(err)
	}
	u1, err := s.PreviewTrack(src, 1)
	if err != nil {
		t.Fatal(err)
	}
	if u0 == u1 || !strings.HasPrefix(u0, "/__file/") {
		t.Fatalf("urls %q %q", u0, u1)
	}
	if again, err := s.PreviewTrack(src, 1); err != nil || again != u1 {
		t.Fatalf("second call: %q, %v; want the same file %q", again, err, u1)
	}
	n1, _ := previewTrackFile(u1)
	info, err := probeMedia(ctx, filepath.Join(out, n1))
	if err == nil || !strings.Contains(err.Error(), "no video") {
		// Audio-only: probeMedia rejects it; check the stream with ffprobe.
		t.Fatalf("probe of the track file: %+v, %v", info, err)
	}
	if b, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type", "-of", "csv=p=0", filepath.Join(out, n1)).Output(); err != nil || strings.TrimSpace(string(b)) != "audio" {
		t.Fatalf("track file streams: %q, %v", b, err)
	}
	if _, err := s.PreviewTrack(src, 5); err == nil {
		t.Fatal("track 5 of a 2-track clip: want an error")
	}

	if err := s.ReleasePreviewTracks([]string{u0, u1, "/__file/clip.mp4"}); err != nil {
		t.Fatal(err)
	}
	assertOnly(t, out)
}

func TestReleaseAllPreviewTracks(t *testing.T) {
	dir := t.TempDir()
	prev := previewDir
	previewDir = func() string { return dir }
	defer func() { previewDir = prev }()
	for _, name := range []string{"editaudio-a-0.m4a", "editaudio-b-1.m4a", "editaudio-c-0.part.m4a", "clip.mp4"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ReleaseAllPreviewTracks()
	assertOnly(t, dir, "clip.mp4")
}
