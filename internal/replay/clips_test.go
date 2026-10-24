package replay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func writeClip(t *testing.T, path string, age time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	_ = os.Chtimes(path, when, when)
}

func TestListClips(t *testing.T) {
	root := t.TempDir()
	writeClip(t, filepath.Join(root, "VALORANT", "VALORANT 1.mp4"), time.Hour)
	writeClip(t, filepath.Join(root, "Desktop", "Replay 2.mp4"), time.Minute)
	writeClip(t, filepath.Join(root, "old.mp4"), 2*time.Hour)
	writeClip(t, filepath.Join(root, "VALORANT", "saving.mp4.part"), 0)
	writeClip(t, filepath.Join(root, "notes.txt"), 0)
	writeClip(t, filepath.Join(root, "a", "b", "too-deep.mp4"), 0)

	got := listClips(root)
	if len(got) != 3 {
		t.Fatalf("got %d clips: %+v", len(got), got)
	}
	if got[0].Name != "Replay 2" || got[0].Game != "Desktop" || got[1].Game != "VALORANT" || got[2].Game != "" {
		t.Errorf("order/games wrong: %+v", got)
	}
	if got[1].ThumbURL == "" || got[1].SizeBytes != 1 {
		t.Errorf("clip fields: %+v", got[1])
	}
}

func TestClipInSaveDirConfines(t *testing.T) {
	root := t.TempDir()
	s := &ReplayService{settings: Settings{SaveDir: root}}
	if _, err := s.clipInSaveDir(filepath.Join(root, "Game", "a.mp4")); err != nil {
		t.Errorf("clip inside the save folder rejected: %v", err)
	}
	for _, bad := range []string{
		filepath.Join(root, "..", "evil.mp4"),
		filepath.Join(root, "Game", "notes.txt"),
		root,
		`C:\Windows\explorer.exe`,
	} {
		if _, err := s.clipInSaveDir(bad); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
}

func TestClipThumbnailMiddleware(t *testing.T) {
	if _, err := capture.LocateFFmpeg(); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	cache := t.TempDir()
	t.Setenv("LocalAppData", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	root := t.TempDir()
	clip := filepath.Join(root, "Game", "Game 1.mp4")
	_ = os.MkdirAll(filepath.Dir(clip), 0o755)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := capture.RunFFmpeg(ctx, []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "testsrc2=s=640x360:r=30", "-t", "2", "-pix_fmt", "yuv420p", clip}); err != nil {
		t.Fatal(err)
	}
	s := &ReplayService{settings: Settings{SaveDir: root}}
	clips := s.ListClips()
	if len(clips) != 1 {
		t.Fatalf("clips = %+v", clips)
	}
	h := s.ClipMiddleware()(http.NotFoundHandler())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", clips[0].ThumbURL, nil))
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Body.Len() < 1000 {
		t.Fatalf("thumb: %d %q %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/__replaythumb/..%2F..%2Fwin.ini", nil))
	if rec.Code != 404 {
		t.Errorf("traversal got %d", rec.Code)
	}

	if err := s.DeleteClip(clip); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(clip)); !os.IsNotExist(err) {
		t.Error("empty game folder should be removed with its last clip")
	}
}
