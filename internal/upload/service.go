// Package upload shares a video as a public link: it trims/compresses the
// clip to fit the host's size limit, uploads it to catbox.moe (no account,
// links never expire) and remembers the link so sharing the same clip again
// is instant.
//
// JS binding name: UploadService.*
package upload

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventProgress reports a share's progress: payload Progress.
const EventProgress = "upload:progress"

// Progress is one progress update for the share dialog.
type Progress struct {
	Stage string  `json:"stage"` // "compress" | "upload"
	Pct   float64 `json:"pct"`   // 0..100 within the stage
}

// UploadService is the Wails-bound share-as-link API.
type UploadService struct {
	endpoint string // catbox API URL (overridden in tests)

	busy   atomic.Bool
	mu     sync.Mutex
	cancel context.CancelFunc
}

// New returns the service.
func New() *UploadService { return &UploadService{endpoint: catboxEndpoint} }

// ShareLink uploads [startMs, endMs] of path (endMs <= 0 = to the end) and
// returns its public link. Clips that already fit the host are uploaded
// untouched; bigger ones (or a trimmed range) are re-encoded to fit. A clip
// shared before returns its saved link without uploading again.
func (s *UploadService) ShareLink(path string, startMs, endMs int) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("share: %w", err)
	}
	if !s.busy.CompareAndSwap(false, true) {
		return "", errors.New("already sharing a clip")
	}
	defer s.busy.Store(false)

	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.cancel = nil
		s.mu.Unlock()
	}()

	durMs, err := capture.ProbeDurationMs(ctx, path)
	if err != nil {
		return "", fmt.Errorf("share: %w", err)
	}
	if endMs <= 0 || endMs > durMs {
		endMs = durMs
	}
	startMs = max(0, min(startMs, endMs-100))
	whole := startMs == 0 && endMs == durMs

	key := linkKey{Path: path, StartMs: startMs, EndMs: endMs, ModTime: fi.ModTime().UnixNano()}
	if whole {
		key.EndMs = 0 // what LinkFor looks up
	}
	if url := lookupLink(key); url != "" {
		return url, nil
	}

	src := path
	if !whole || fi.Size() > maxUploadBytes {
		tmp, err := os.MkdirTemp("", "toru-share-")
		if err != nil {
			return "", err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		src = filepath.Join(tmp, shareName(path))
		emit(Progress{Stage: "compress"})
		plan := planEncode(endMs - startMs)
		err = capture.RunFFmpegProgress(ctx, compressArgs(path, src, startMs, endMs, plan, pickEncoder()),
			endMs-startMs, func(p float64) { emit(Progress{Stage: "compress", Pct: p * 100}) })
		if err != nil {
			if ctx.Err() != nil {
				return "", errors.New("share cancelled")
			}
			return "", fmt.Errorf("couldn't prepare the clip: %w", err)
		}
		if out, err := os.Stat(src); err == nil && out.Size() > maxUploadBytes {
			return "", fmt.Errorf("compressed clip is still %d MB (limit %d MB); trim it shorter", out.Size()>>20, maxUploadBytes>>20)
		}
	}

	emit(Progress{Stage: "upload"})
	url, err := uploadCatbox(ctx, s.endpoint, src, func(p float64) { emit(Progress{Stage: "upload", Pct: p * 100}) })
	if err != nil {
		if ctx.Err() != nil {
			return "", errors.New("share cancelled")
		}
		return "", err
	}
	saveLink(key, url)
	return url, nil
}

// CancelShare stops a running ShareLink.
func (s *UploadService) CancelShare() {
	s.mu.Lock()
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// LinkFor returns the saved link for the whole of path, or "" if it hasn't
// been shared (or the file changed since).
func (s *UploadService) LinkFor(path string) string {
	fi, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return lookupLink(linkKey{Path: path, EndMs: 0, ModTime: fi.ModTime().UnixNano()})
}

// shareName is the uploaded file's name (it shows in the link's download).
func shareName(path string) string {
	base := filepath.Base(path)
	return base[:len(base)-len(filepath.Ext(base))] + ".mp4"
}

func emit(p Progress) {
	if app := application.Get(); app != nil {
		app.Event.Emit(EventProgress, p)
	}
}
