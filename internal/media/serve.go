package media

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/vid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

const (
	filePrefix  = "/__media/"
	thumbPrefix = "/__media/thumb/"
)

// thumbDir is where video thumbnails are cached; a var so tests can move it.
var thumbDir = func() string { return filepath.Join(os.TempDir(), "toru", "media-thumbs") }

// Middleware serves registered files at /__media/<id>/<name> (Range
// requests included, so the player can seek) and video thumbnails at
// /__media/thumb/<id>.jpg, and installed fonts at /__media/font/<name>.
// Anything else passes to next. Go-only.
//
//wails:ignore
func (s *Service) Middleware() application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, filePrefix) {
				next.ServeHTTP(w, r)
				return
			}
			s.ServeHTTP(w, r)
		})
	}
}

// ServeHTTP handles the /__media/ paths.
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if name, ok := vid.FontNameFromURLPath(r.URL.EscapedPath()); ok {
		// An installed font for the text preview (vid.Font.URL); only names
		// in the installed list resolve, so no other file can be asked for.
		f := vid.FontFile(name)
		if f == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "max-age=3600")
		http.ServeFile(w, r, f)
		return
	}
	if rest, ok := strings.CutPrefix(r.URL.Path, thumbPrefix); ok {
		s.serveThumb(w, r, strings.TrimSuffix(rest, ".jpg"))
		return
	}
	// <id>/<name>: the name is only for the browser (type, downloads); the
	// id alone picks the file.
	id, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, filePrefix), "/")
	m, ok := s.get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(m.Path)
	if err != nil {
		http.NotFound(w, r) // moved or deleted since it was imported
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, filepath.Base(m.Path), fi.ModTime(), f)
}

func (s *Service) serveThumb(w http.ResponseWriter, r *http.Request, id string) {
	m, ok := s.get(id)
	if !ok || m.Kind != KindVideo {
		http.NotFound(w, r)
		return
	}
	thumb, err := ensureThumb(m)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, thumb)
}

// thumbLocks keeps two requests for one thumbnail from both running ffmpeg.
var thumbLocks sync.Map // path -> *sync.Mutex

// ensureThumb returns a cached JPEG frame from 10% into m, making it on the
// first request. The cache key includes the file's size and mtime, so a
// replaced file gets a new thumbnail.
func ensureThumb(m Media) (string, error) {
	fi, err := os.Stat(m.Path)
	if err != nil {
		return "", err
	}
	out := filepath.Join(thumbDir(), fmt.Sprintf("%s-%x-%x.jpg", m.ID, fi.Size(), fi.ModTime().UnixNano()))
	lk, _ := thumbLocks.LoadOrStore(out, &sync.Mutex{})
	mu := lk.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	at := m.Duration * 0.1
	tmp := strings.TrimSuffix(out, ".jpg") + ".part.jpg"
	defer func() { _ = os.Remove(tmp) }()
	if err := capture.RunFFmpeg(ctx, []string{"-y", "-ss", fmt.Sprintf("%.3f", at), "-i", m.Path,
		"-frames:v", "1", "-vf", "scale=320:-2", "-q:v", "4", tmp}); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}
