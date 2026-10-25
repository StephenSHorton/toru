package capture

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// The FFmpeg build downloaded when none is installed: BtbN's LGPL shared
// build (no GPL encoders; recording only uses hardware encoders, libvpx and
// libopus). Month-end autobuild releases are kept for years, so the URL stays
// valid; the SHA-256 pins the exact bytes. Bump all three together.
const (
	ffmpegBuild  = "n9.0.2-17-g2a571b6068-win64-lgpl-shared-9.0"
	ffmpegURL    = "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-09-30-13-08/ffmpeg-" + ffmpegBuild + ".zip"
	ffmpegSHA256 = "7157177b8a6cb2174c1650ba8c71b363f2c78cba5330f88c4c02cf5b2b880646"

	// FFmpegDownloadBytes is the pinned archive's size, for progress before
	// the server reports one.
	FFmpegDownloadBytes = 76972461
)

// ManagedFFmpegDir is where the downloaded FFmpeg lives
// (%LOCALAPPDATA%\toru\ffmpeg\<build>), or "" where downloads aren't offered.
func ManagedFFmpegDir() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "toru", "ffmpeg", ffmpegBuild)
}

// DownloadFFmpeg fetches the pinned FFmpeg build into ManagedFFmpegDir,
// verifying its SHA-256 before anything is unpacked. onProgress (may be nil)
// gets the bytes received so far and the total. LocateFFmpeg finds the new
// copy as soon as this returns nil.
func DownloadFFmpeg(ctx context.Context, onProgress func(done, total int64)) error {
	dir := ManagedFFmpegDir()
	if dir == "" {
		return errors.New("ffmpeg download is only available on Windows")
	}
	return fetchFFmpeg(ctx, ffmpegURL, ffmpegSHA256, dir, onProgress)
}

func fetchFFmpeg(ctx context.Context, url, sum, dir string, onProgress func(done, total int64)) error {
	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, "download-*.zip")
	if err != nil {
		return err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download ffmpeg: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download ffmpeg: %s", resp.Status)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = FFmpegDownloadBytes
	}
	h := sha256.New()
	pw := &progressWriter{total: total, onProgress: onProgress}
	if _, err := io.Copy(io.MultiWriter(tmp, h, pw), resp.Body); err != nil {
		return fmt.Errorf("download ffmpeg: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("download ffmpeg: checksum mismatch (got %s)", got)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	// Unpack into a staging folder and swap it in, so a half-extracted copy
	// is never picked up.
	stage := dir + ".partial"
	_ = os.RemoveAll(stage)
	if err := extractFFmpegBin(tmp.Name(), stage); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(stage, dir); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	pruneOtherBuilds(parent, filepath.Base(dir))
	return nil
}

// extractFFmpegBin copies the archive's bin/ folder (ffmpeg, ffprobe and the
// DLLs they load) flat into dst. ffplay is skipped.
func extractFFmpegBin(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open ffmpeg archive: %w", err)
	}
	defer func() { _ = zr.Close() }()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	found := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || path.Base(path.Dir(f.Name)) != "bin" {
			continue
		}
		name := path.Base(f.Name)
		lower := strings.ToLower(name)
		if lower == "ffplay.exe" || (!strings.HasSuffix(lower, ".exe") && !strings.HasSuffix(lower, ".dll")) {
			continue
		}
		if err := extractZipFile(f, filepath.Join(dst, name)); err != nil {
			return err
		}
		found = found || lower == ffmpegName()
	}
	if !found {
		return errors.New("ffmpeg archive has no " + ffmpegName())
	}
	return nil
}

func extractZipFile(f *zip.File, out string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	w, err := os.Create(out)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, rc); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// pruneOtherBuilds removes earlier downloaded builds once a new one is in.
func pruneOtherBuilds(parent, keep string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep && !strings.HasSuffix(e.Name(), ".partial") {
			_ = os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
}

type progressWriter struct {
	done, total int64
	onProgress  func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.onProgress != nil {
		p.onProgress(p.done, max(p.total, p.done))
	}
	return len(b), nil
}
