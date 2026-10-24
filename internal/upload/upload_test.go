package upload

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestPlanEncodeFitsTheLimit(t *testing.T) {
	for _, sec := range []int{10, 60, 300, 1200} {
		p := planEncode(sec * 1000)
		bytes := float64(p.VideoKbps+audioKbps) * 1000 / 8 * float64(sec)
		if bytes > targetBytes {
			t.Errorf("%ds: plan %+v makes %.0f MB, over the target", sec, p, bytes/1e6)
		}
		t.Logf("%4ds -> %dp%d @ %d kbps (~%.0f MB)", sec, p.Height, p.FPS, p.VideoKbps, bytes/1e6)
	}
	if p := planEncode(60_000); p.Height != 1080 || p.FPS != 60 || p.VideoKbps != 16000 {
		t.Errorf("a 1-minute clip should get full 1080p60 quality, got %+v", p)
	}
}

func TestCompressArgs(t *testing.T) {
	got := strings.Join(compressArgs("in.mp4", "out.mp4", 5000, 35000, encodePlan{1080, 60, 16000}, "h264_nvenc"), " ")
	for _, want := range []string{
		"-ss 5.000 -i in.mp4 -t 30.000",
		"-map 0:v:0 -map 0:a:0?",
		"fps=60,scale=-2:'min(1080,ih)'",
		"-c:v h264_nvenc -preset p5 -rc vbr -b:v 16000k",
		"-movflags +faststart out.mp4",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(strings.Join(compressArgs("a", "b", 0, 1000, encodePlan{720, 30, 500}, "libx264"), " "), "-ss") {
		t.Error("no -ss when starting at 0")
	}
}

func TestUploadCatboxMultipart(t *testing.T) {
	var gotType, gotField, gotName string
	var gotBytes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		gotField = r.FormValue("reqtype")
		f, h, err := r.FormFile("fileToUpload")
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		b, _ := io.ReadAll(f)
		gotBytes, gotName, gotType = len(b), h.Filename, h.Header.Get("Content-Type")
		_, _ = io.WriteString(w, "https://files.catbox.moe/abc123.mp4")
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "Clip 1.mp4")
	_ = os.WriteFile(path, make([]byte, 300_000), 0o644)
	var last float64
	url, err := uploadCatbox(context.Background(), srv.URL, path, func(p float64) { last = p })
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://files.catbox.moe/abc123.mp4" || gotField != "fileupload" || gotName != "Clip 1.mp4" ||
		gotBytes != 300_000 || gotType != "video/mp4" || last != 1 {
		t.Errorf("url=%q field=%q name=%q bytes=%d type=%q progress=%v", url, gotField, gotName, gotBytes, gotType, last)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "File too large")
	}))
	defer bad.Close()
	if _, err := uploadCatbox(context.Background(), bad.URL, path, nil); err == nil || !strings.Contains(err.Error(), "File too large") {
		t.Errorf("host error not surfaced: %v", err)
	}
}

// TestShareLinkEndToEnd trims a real clip, uploads it to a fake host and
// checks the saved link is reused. Needs ffmpeg.
func TestShareLinkEndToEnd(t *testing.T) {
	if _, err := capture.LocateFFmpeg(); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	cfg := t.TempDir()
	t.Setenv("AppData", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	uploads := 0
	var uploaded int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads++
		_ = r.ParseMultipartForm(32 << 20)
		f, _, _ := r.FormFile("fileToUpload")
		n, _ := io.Copy(io.Discard, f)
		uploaded = n
		_, _ = io.WriteString(w, "https://files.catbox.moe/xyz.mp4")
	}))
	defer srv.Close()

	clip := filepath.Join(t.TempDir(), "Game 1.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := capture.RunFFmpeg(ctx, []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=s=1280x720:r=30", "-f", "lavfi", "-i", "sine",
		"-t", "4", "-pix_fmt", "yuv420p", "-c:a", "aac", clip}); err != nil {
		t.Fatal(err)
	}
	s := &UploadService{endpoint: srv.URL}
	url, err := s.ShareLink(clip, 1000, 3000) // a range forces the re-encode
	if err != nil {
		t.Fatal(err)
	}
	if url != "https://files.catbox.moe/xyz.mp4" || uploads != 1 || uploaded < 10_000 {
		t.Fatalf("url=%q uploads=%d bytes=%d", url, uploads, uploaded)
	}
	if again, _ := s.ShareLink(clip, 1000, 3000); again != url || uploads != 1 {
		t.Errorf("re-share should reuse the link without uploading (uploads=%d)", uploads)
	}
	if s.LinkFor(clip) != "" {
		t.Error("only a range was shared; the whole clip has no link yet")
	}
	if _, err := s.ShareLink(clip, 0, 0); err != nil || uploads != 2 || s.LinkFor(clip) == "" {
		t.Errorf("whole-clip share: err=%v uploads=%d link=%q", err, uploads, s.LinkFor(clip))
	}
}
