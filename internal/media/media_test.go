package media

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestKindByExt(t *testing.T) {
	for in, want := range map[string]Kind{
		`C:\a\clip.MP4`: KindVideo, "b.mkv": KindVideo, "c.mov": KindVideo, "d.webm": KindVideo,
		"song.mp3": KindAudio, "fx.WAV": KindAudio, "e.m4a": KindAudio, "f.flac": KindAudio, "g.ogg": KindAudio, "h.aac": KindAudio,
		"logo.png": KindImage, "i.JPG": KindImage, "j.jpeg": KindImage, "k.webp": KindImage, "meme.gif": KindImage,
		"notes.txt": "", "noext": "", "x.exe": "",
	} {
		if got := kindByExt(in); got != want {
			t.Errorf("kindByExt(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMediaIDStable(t *testing.T) {
	a := mediaID(`C:\Users\Me\Music\Song.mp3`)
	if a != mediaID(`c:\users\me\music\song.mp3`) || a != mediaID(`C:\Users\Me\Music\.\Song.mp3`) {
		t.Fatal("same file, different ids")
	}
	if a == mediaID(`C:\Users\Me\Music\Other.mp3`) || len(a) != 12 {
		t.Fatalf("id %q", a)
	}
	if u := mediaURL(a, `C:\x\my song #1.mp3`); u != "/__media/"+a+"/my%20song%20%231.mp3" {
		t.Fatalf("url %q", u)
	}
}

func TestParseProbe(t *testing.T) {
	// An MP3 with cover art: the picture is not the video.
	got, err := parseProbe([]byte(`{"streams":[
		{"codec_type":"audio","disposition":{"default":1},"duration":"184.2"},
		{"codec_type":"video","width":600,"height":600,"avg_frame_rate":"0/0","disposition":{"attached_pic":1}}
	],"format":{"duration":"184.25"}}`))
	if err != nil || got.width != 0 || got.duration != 184.25 || len(got.tracks) != 1 || got.tracks[0].IsMix {
		t.Fatalf("mp3 with cover: %+v, %v", got, err)
	}

	// A separate-tracks clip: titled tracks, default is the mix.
	got, err = parseProbe([]byte(`{"streams":[
		{"codec_type":"video","width":2560,"height":1440,"avg_frame_rate":"144/1","duration":"30.0"},
		{"codec_type":"audio","disposition":{"default":1},"tags":{"name":"All audio"}},
		{"codec_type":"audio","tags":{"title":"Microphone","handler_name":"SoundHandler"}}
	],"format":{}}`))
	if err != nil || got.width != 2560 || got.fps != 144 || got.duration != 30 {
		t.Fatalf("clip: %+v, %v", got, err)
	}
	if len(got.tracks) != 2 || !got.tracks[0].IsMix || got.tracks[0].Title != "All audio" || got.tracks[1].Title != "Microphone" {
		t.Fatalf("clip tracks: %+v", got.tracks)
	}
	if _, err := parseProbe([]byte("not json")); err == nil {
		t.Fatal("bad json: want an error")
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterImageAndServeRange(t *testing.T) {
	dir := t.TempDir()
	logo := filepath.Join(dir, "My Logo.png")
	writePNG(t, logo, 64, 32)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := New()
	got := s.Register([]string{logo, filepath.Join(dir, "notes.txt"), filepath.Join(dir, "missing.png"), logo})
	if len(got) != 1 {
		t.Fatalf("registered %+v; want only the png, once", got)
	}
	m := got[0]
	if m.Kind != KindImage || m.Width != 64 || m.Height != 32 || m.Duration != 0 || m.HasAudio || m.Tracks == nil {
		t.Fatalf("png = %+v", m)
	}
	if thumb, err := s.Thumbnail(m.ID); err != nil || thumb != m.URL {
		t.Fatalf("image thumbnail %q, %v", thumb, err)
	}

	want, _ := os.ReadFile(logo)
	srv := httptest.NewServer(s.Middleware()(http.NotFoundHandler()))
	defer srv.Close()

	// Whole file.
	body, code, _ := get(t, srv.URL+m.URL, "")
	if code != http.StatusOK || string(body) != string(want) {
		t.Fatalf("GET %s: %d, %d bytes", m.URL, code, len(body))
	}
	// A byte range, as a <video> seeking would ask for.
	body, code, hdr := get(t, srv.URL+m.URL, "bytes=8-15")
	if code != http.StatusPartialContent || string(body) != string(want[8:16]) || !strings.HasPrefix(hdr.Get("Content-Range"), "bytes 8-15/") {
		t.Fatalf("range: %d %q %q", code, body, hdr.Get("Content-Range"))
	}
	if hdr.Get("Accept-Ranges") != "bytes" && hdr.Get("Content-Range") == "" {
		t.Fatal("no range support")
	}
	// Only registered ids are served; other paths fall through.
	if _, code, _ := get(t, srv.URL+"/__media/000000000000/x.png", ""); code != http.StatusNotFound {
		t.Fatalf("unregistered id: %d", code)
	}
	if _, code, _ := get(t, srv.URL+"/__media/"+m.ID+"/..%2F..%2Fwindows%2Fwin.ini", ""); code != http.StatusOK {
		// The name part is ignored: still the registered file, never another.
		t.Fatalf("name ignored: %d", code)
	}
	if _, code, _ := get(t, srv.URL+"/__file/x.png", ""); code != http.StatusNotFound {
		t.Fatalf("passthrough: %d", code)
	}
	// A file moved away after import is a 404, not a crash.
	if err := os.Remove(logo); err != nil {
		t.Fatal(err)
	}
	if _, code, _ := get(t, srv.URL+m.URL, ""); code != http.StatusNotFound {
		t.Fatalf("deleted file: %d", code)
	}
	if _, err := s.Thumbnail("nope"); err == nil {
		t.Fatal("thumbnail of an unknown id: want an error")
	}
}

func get(t *testing.T, u, rng string) ([]byte, int, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, u, nil)
	if rng != "" {
		req.Header.Set("Range", rng)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	b, _ := io.ReadAll(res.Body)
	return b, res.StatusCode, res.Header
}

// TestRegisterSmoke registers a generated MP4 (2 audio tracks), MP3 and WAV
// through real ffprobe and fetches the video's thumbnail. It needs ffmpeg
// with lavfi; otherwise it skips.
//
//	go test ./internal/media -run RegisterSmoke -v
func TestRegisterSmoke(t *testing.T) {
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
	clip := filepath.Join(dir, "clip.mp4")
	song := filepath.Join(dir, "song.mp3")
	fx := filepath.Join(dir, "fx.wav")
	silent := filepath.Join(dir, "music-only.mp4") // audio in a video container
	for _, args := range [][]string{
		{"-y", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=30:duration=2",
			"-f", "lavfi", "-i", "sine=f=440:duration=2", "-f", "lavfi", "-i", "sine=f=880:duration=2",
			"-map", "0:v", "-map", "1:a", "-map", "2:a", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", clip},
		{"-y", "-f", "lavfi", "-i", "sine=f=440:duration=3", "-c:a", "libmp3lame", song},
		{"-y", "-f", "lavfi", "-i", "sine=f=440:duration=1", fx},
		{"-y", "-f", "lavfi", "-i", "sine=f=440:duration=1", "-c:a", "aac", silent},
	} {
		if err := capture.RunFFmpeg(ctx, args); err != nil {
			t.Skipf("generate test media (needs lavfi, libx264, libmp3lame): %v", err)
		}
	}
	prev := thumbDir
	thumbDir = func() string { return filepath.Join(dir, "thumbs") }
	defer func() { thumbDir = prev }()

	s := New()
	got := s.Register([]string{clip, song, fx, silent})
	if len(got) != 4 {
		t.Fatalf("registered %d of 4: %+v", len(got), got)
	}
	v, mp3, wav, m4 := got[0], got[1], got[2], got[3]
	if v.Kind != KindVideo || v.Width != 320 || v.Height != 180 || v.FPS != 30 || !v.HasAudio || len(v.Tracks) != 2 || !v.Tracks[0].IsMix {
		t.Fatalf("mp4 = %+v", v)
	}
	if v.Duration < 1.9 || v.Duration > 2.2 {
		t.Fatalf("mp4 duration %v", v.Duration)
	}
	for _, a := range []Media{mp3, wav, m4} {
		if a.Kind != KindAudio || a.Width != 0 || !a.HasAudio || len(a.Tracks) != 1 || a.Duration <= 0.5 {
			t.Fatalf("audio = %+v", a)
		}
	}
	if th, _ := s.Thumbnail(mp3.ID); th != "" {
		t.Fatalf("audio thumbnail %q", th)
	}

	th, err := s.Thumbnail(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Middleware()(http.NotFoundHandler()))
	defer srv.Close()
	body, code, hdr := get(t, srv.URL+th, "")
	if code != http.StatusOK || !strings.HasPrefix(hdr.Get("Content-Type"), "image/jpeg") || len(body) < 100 {
		t.Fatalf("thumbnail %s: %d %s %d bytes", th, code, hdr.Get("Content-Type"), len(body))
	}
	// Cached: one file, reused on the second request.
	if _, code, _ := get(t, srv.URL+th, ""); code != http.StatusOK {
		t.Fatalf("second thumbnail fetch: %d", code)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "thumbs")); len(entries) != 1 {
		t.Fatalf("thumb cache holds %d files", len(entries))
	}
	// The video seeks: a range from the middle.
	if _, code, _ := get(t, srv.URL+v.URL, "bytes=1000-1999"); code != http.StatusPartialContent {
		t.Fatalf("video range: %d", code)
	}
}
