// Package fxtest helps the effect tests run against the FFmpeg build Toru
// ships: the pinned BtbN LGPL build (internal/capture/ffmpeg_fetch.go), not
// whatever ffmpeg is on PATH (often a GPL "full" build with eq, boxblur and
// libx264, which would hide an LGPL-only failure).
package fxtest

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

var (
	mu   sync.Mutex
	path string
)

// PinnedFFmpeg is the ffmpeg the effect tests should run: $TORU_FFMPEG when
// set, else (with TORU_FFMPEG_PINNED=1) the pinned build, downloaded once and
// SHA-256 verified by capture.DownloadFFmpeg. It skips the test otherwise, so
// a plain `go test` never downloads 77 MB.
//
// The download goes to a test cache (%LOCALAPPDATA%\toru-test\toru\ffmpeg\
// <build>), not the app's own folder: LocateFFmpeg prefers that folder over
// PATH, so filling it would change which ffmpeg the installed Toru uses.
func PinnedFFmpeg(t testing.TB) string {
	t.Helper()
	if p := os.Getenv("TORU_FFMPEG"); p != "" {
		return p
	}
	if os.Getenv("TORU_FFMPEG_PINNED") != "1" {
		t.Skip("set TORU_FFMPEG_PINNED=1 to download and run the pinned LGPL ffmpeg (or TORU_FFMPEG to a binary)")
	}
	mu.Lock()
	defer mu.Unlock()
	if path != "" {
		return path
	}
	p, err := fetch()
	if err != nil {
		t.Fatalf("pinned ffmpeg: %v", err)
	}
	path = p
	return p
}

// fetch points the user cache dir at the test cache for the length of the
// download, so ManagedFFmpegDir and DownloadFFmpeg (with its SHA check) run
// unchanged.
func fetch() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	const env = "LOCALAPPDATA"
	old, had := os.LookupEnv(env)
	if err := os.Setenv(env, filepath.Join(cache, "toru-test")); err != nil {
		return "", err
	}
	defer func() {
		if had {
			_ = os.Setenv(env, old)
		} else {
			_ = os.Unsetenv(env)
		}
	}()
	exe := filepath.Join(capture.ManagedFFmpegDir(), "ffmpeg.exe")
	if _, err := os.Stat(exe); err == nil {
		return exe, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := capture.DownloadFFmpeg(ctx, nil); err != nil {
		return "", err
	}
	return exe, nil
}
