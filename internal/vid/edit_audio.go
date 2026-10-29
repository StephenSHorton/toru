package vid

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// edit_audio.go backs the editor's live per-track audio preview: each audio
// track of a source is stream-copied to its own temp file, which the window
// plays in sync with the muted video at the level the user set.
//
// The files live in %TEMP%/toru, so ShotMiddleware serves them at
// /__file/<basename>. The window releases them when it closes; files a
// crashed window left behind are swept after previewTrackMaxAge.

// previewTrackPrefix names every preview track file (the sweep only ever
// touches these).
const previewTrackPrefix = "editaudio-"

// previewTrackMaxAge is how old a preview file gets before any later
// PreviewTrack call deletes it.
const previewTrackMaxAge = 12 * time.Hour

// previewMu serializes extraction, so two calls for one track don't both
// write it.
var previewMu sync.Mutex

// previewDir is where preview tracks go; a var so tests can redirect it.
var previewDir = func() string { return filepath.Join(os.TempDir(), "toru") }

// PreviewTrack extracts audio track index (0 = first audio stream) of path
// to a temp file and returns the URL the window plays it from. The same
// unchanged source and track reuse the file already extracted.
func (s *VideoService) PreviewTrack(path string, index int) (string, error) {
	if index < 0 {
		return "", fmt.Errorf("preview track: bad index %d", index)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("preview track: %w", err)
	}
	dir := previewDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("preview track: %w", err)
	}

	previewMu.Lock()
	defer previewMu.Unlock()
	sweepPreviewTracks(dir, time.Now())

	out := filepath.Join(dir, previewTrackName(path, fi, index))
	if _, err := os.Stat(out); err == nil {
		now := time.Now()
		_ = os.Chtimes(out, now, now) // in use again: keep it from the sweep
		return previewTrackURL(out), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tmp := strings.TrimSuffix(out, ".m4a") + ".part.m4a"
	defer func() { _ = os.Remove(tmp) }()
	// Stream copy is instant; a codec MP4 can't hold falls back to AAC.
	var runErr error
	for _, codec := range [][]string{{"-c:a", "copy"}, {"-c:a", "aac", "-b:a", "192k"}} {
		args := append([]string{"-y", "-i", path, "-map", "0:a:" + strconv.Itoa(index), "-vn", "-sn", "-dn"}, codec...)
		args = append(args, "-movflags", "+faststart", "-f", "mp4", tmp)
		if runErr = capture.RunFFmpeg(ctx, args); runErr == nil {
			break
		}
	}
	if runErr != nil {
		return "", fmt.Errorf("preview track %d of %s: %w", index, filepath.Base(path), runErr)
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", fmt.Errorf("preview track: %w", err)
	}
	return previewTrackURL(out), nil
}

// ReleasePreviewTracks deletes preview files the window no longer needs, by
// the URLs PreviewTrack returned. Anything that isn't a preview track URL is
// ignored.
func (s *VideoService) ReleasePreviewTracks(urls []string) error {
	dir := previewDir()
	previewMu.Lock()
	defer previewMu.Unlock()
	for _, u := range urls {
		name, ok := previewTrackFile(u)
		if !ok {
			continue
		}
		_ = os.Remove(filepath.Join(dir, name))
	}
	return nil
}

// ReleaseAllPreviewTracks deletes every preview track file. Go calls it when
// the last editor window closes: the page's own release on unload is an
// async call WebView2 tears down before it finishes.
//
//wails:ignore
func ReleaseAllPreviewTracks() {
	previewMu.Lock()
	defer previewMu.Unlock()
	removePreviewTracks(previewDir(), func(os.FileInfo) bool { return true })
}

// previewTrackName keys the file on the source's path, size and mtime (an
// edited or replaced source gets a fresh extract) plus the track.
func previewTrackName(path string, fi os.FileInfo, index int) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	h := sha1.Sum([]byte(fmt.Sprintf("%s|%d|%d", strings.ToLower(abs), fi.Size(), fi.ModTime().UnixNano())))
	return fmt.Sprintf("%s%s-%d.m4a", previewTrackPrefix, hex.EncodeToString(h[:6]), index)
}

func previewTrackURL(file string) string {
	return "/__file/" + url.PathEscape(filepath.Base(file))
}

// previewTrackFile returns the file name a preview URL points at, if it is
// one.
func previewTrackFile(u string) (string, bool) {
	rest, ok := strings.CutPrefix(u, "/__file/")
	if !ok {
		return "", false
	}
	name, err := url.PathUnescape(rest)
	if err != nil || name != filepath.Base(name) || !strings.HasPrefix(name, previewTrackPrefix) || !strings.HasSuffix(name, ".m4a") {
		return "", false
	}
	return name, true
}

// sweepPreviewTracks deletes preview files older than previewTrackMaxAge
// (and stale .part files), left by windows that never released them.
func sweepPreviewTracks(dir string, now time.Time) {
	removePreviewTracks(dir, func(info os.FileInfo) bool { return now.Sub(info.ModTime()) >= previewTrackMaxAge })
}

// removePreviewTracks deletes the preview files (and .part files) in dir
// that match.
func removePreviewTracks(dir string, match func(os.FileInfo) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), previewTrackPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || !match(info) {
			continue
		}
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}
