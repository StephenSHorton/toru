package replay

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Clip is one saved instant replay, for the Library's Clips view.
type Clip struct {
	Path      string    `json:"path"`
	Name      string    `json:"name"`      // file name without .mp4
	Game      string    `json:"game"`      // game folder, "" = save folder root
	SizeBytes int64     `json:"sizeBytes"` //
	SavedAt   time.Time `json:"savedAt"`   // file modification time
	ThumbURL  string    `json:"thumbUrl"`  // served JPEG thumbnail
}

// maxListedClips bounds a listing so a huge archive can't stall the page.
const maxListedClips = 500

const (
	thumbPrefix = "/__replaythumb/"
	clipPrefix  = "/__replayclip/"
)

// ListClips returns the saved clips in the save folder and its game folders
// (one level deep), newest first.
func (s *ReplayService) ListClips() []Clip {
	return listClips(s.GetSaveDir())
}

func listClips(root string) []Clip {
	out := []Clip{}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		if !e.IsDir() {
			addClip(&out, path, "", root)
			continue
		}
		// <root>\<Game>\*.mp4
		sub, _ := os.ReadDir(path)
		for _, f := range sub {
			if !f.IsDir() {
				addClip(&out, filepath.Join(path, f.Name()), e.Name(), root)
			}
		}
	}
	slices.SortFunc(out, func(a, b Clip) int { return b.SavedAt.Compare(a.SavedAt) })
	if len(out) > maxListedClips {
		out = out[:maxListedClips]
	}
	return out
}

func addClip(out *[]Clip, path, game, root string) {
	if !strings.EqualFold(filepath.Ext(path), ".mp4") {
		return // also skips in-progress .mp4.part files
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return
	}
	*out = append(*out, Clip{
		Path:      path,
		Name:      strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Game:      game,
		SizeBytes: fi.Size(),
		SavedAt:   fi.ModTime(),
		ThumbURL:  thumbPrefix + url.PathEscape(filepath.ToSlash(rel)) + fmt.Sprintf("?m=%d", fi.ModTime().UnixNano()),
	})
}

// clipInSaveDir resolves path and checks it is an .mp4 inside the save
// folder, so the bound open/reveal/delete calls can't touch other files.
func (s *ReplayService) clipInSaveDir(path string) (string, error) {
	root, err := filepath.Abs(s.GetSaveDir())
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) ||
		!strings.EqualFold(filepath.Ext(abs), ".mp4") {
		return "", fmt.Errorf("not a saved clip: %s", path)
	}
	return abs, nil
}

// SetClipOpener registers how OpenClip shows a clip: main passes the window
// opener for Toru's clip editor (path + the served URL its player loads).
//
//wails:ignore
func (s *ReplayService) SetClipOpener(fn func(path, servedURL string)) {
	s.mu.Lock()
	s.clipOpener = fn
	s.mu.Unlock()
}

// ClipURL returns the URL Toru's player streams a saved clip from, or "" if
// path isn't in the save folder.
func (s *ReplayService) ClipURL(path string) string {
	p, err := s.clipInSaveDir(path)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(filepath.Clean(s.GetSaveDir()), p)
	if err != nil {
		return ""
	}
	return clipPrefix + url.PathEscape(filepath.ToSlash(rel))
}

// OpenClip opens a clip in Toru's clip editor (play, trim, save, share).
func (s *ReplayService) OpenClip(path string) error {
	p, err := s.clipInSaveDir(path)
	if err != nil {
		return err
	}
	s.mu.Lock()
	opener := s.clipOpener
	s.mu.Unlock()
	if opener == nil {
		return s.OpenClipExternal(p)
	}
	opener(p, s.ClipURL(p))
	return nil
}

// OpenClipExternal opens a clip in the default video player.
func (s *ReplayService) OpenClipExternal(path string) error {
	p, err := s.clipInSaveDir(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", p).Start()
	}
	return exec.Command("xdg-open", p).Start()
}

// RevealClip shows a clip selected in Explorer.
func (s *ReplayService) RevealClip(path string) error {
	p, err := s.clipInSaveDir(path)
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		return exec.Command("explorer.exe", "/select,", p).Start()
	}
	return exec.Command("xdg-open", filepath.Dir(p)).Start()
}

// DeleteClip deletes a clip (and its cached thumbnail). An emptied game
// folder is removed too.
func (s *ReplayService) DeleteClip(path string) error {
	p, err := s.clipInSaveDir(path)
	if err != nil {
		return err
	}
	fi, statErr := os.Stat(p)
	if err := os.Remove(p); err != nil {
		return err
	}
	if statErr == nil {
		if t, err := thumbPath(p, fi.ModTime()); err == nil {
			_ = os.Remove(t)
		}
	}
	if dir := filepath.Dir(p); !strings.EqualFold(dir, filepath.Clean(s.GetSaveDir())) {
		_ = os.Remove(dir) // only succeeds when empty
	}
	return nil
}

// thumbPath is the cached thumbnail for a clip version (path + mtime).
func thumbPath(clip string, mod time.Time) (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha1.Sum([]byte(strings.ToLower(clip) + "|" + mod.UTC().Format(time.RFC3339Nano)))
	return filepath.Join(cache, "toru", "thumbs", hex.EncodeToString(sum[:])+".jpg"), nil
}

// thumbSem limits concurrent thumbnail encodes when a page of clips loads.
var thumbSem = make(chan struct{}, 2)

// thumbLocks serializes generation per thumbnail so parallel requests for the
// same clip don't run ffmpeg twice.
var thumbLocks sync.Map

// ensureThumb returns the cached thumbnail for clip, generating it with ffmpeg
// (one frame ~1s in, 480px wide) on first request.
func ensureThumb(clip string) (string, error) {
	fi, err := os.Stat(clip)
	if err != nil {
		return "", err
	}
	out, err := thumbPath(clip, fi.ModTime())
	if err != nil {
		return "", err
	}
	mu, _ := thumbLocks.LoadOrStore(out, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	thumbSem <- struct{}{}
	defer func() { <-thumbSem }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tmp := out + ".tmp.jpg"
	err = capture.RunFFmpeg(ctx, []string{
		"-hide_banner", "-loglevel", "error", "-y",
		"-ss", "1", "-i", clip, "-frames:v", "1",
		"-vf", "scale=480:-2", "-q:v", "4", tmp,
	})
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return out, os.Rename(tmp, out)
}

// ClipMiddleware serves saved clips at /__replayclip/<path relative to the
// save folder> (with range requests, so the player can seek) and their
// thumbnails at /__replaythumb/<same>. Paths are confined to the save folder.
//
//wails:ignore
func (s *ReplayService) ClipMiddleware() application.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if rel, ok := strings.CutPrefix(r.URL.Path, clipPrefix); ok {
				clip, err := s.clipInSaveDir(filepath.Join(s.GetSaveDir(), filepath.FromSlash(rel)))
				if err != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Cache-Control", "no-store")
				http.ServeFile(w, r, clip)
				return
			}
			if !strings.HasPrefix(r.URL.Path, thumbPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			rel := filepath.FromSlash(strings.TrimPrefix(r.URL.Path, thumbPrefix))
			clip, err := s.clipInSaveDir(filepath.Join(s.GetSaveDir(), rel))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			thumb, err := ensureThumb(clip)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			// The ?m=<mtime> in the URL changes whenever the clip does.
			w.Header().Set("Cache-Control", "max-age=86400")
			http.ServeFile(w, r, thumb)
		})
	}
}
