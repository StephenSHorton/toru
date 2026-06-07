package capture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ffmpeg_resolve.go locates and runs the FFmpeg binary. FFmpeg is NOT embedded
// (//go:embed of a ~100MB binary bloats the build); it is staged beside the app
// by the installer, with a first-run download+SHA256-verify fallback handled
// here later.
//
// Resolution order:
//  1. $TORU_FFMPEG (explicit override; dev convenience)
//  2. ffmpeg.exe next to the running executable (installer-staged)
//  3. ffmpeg(.exe) on PATH

var (
	ffmpegOnce sync.Once
	ffmpegPath string
	ffmpegErr  error
)

func ffmpegName() string {
	if os.PathListSeparator == ';' { // Windows
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

// LocateFFmpeg returns the path to a usable ffmpeg binary (cached).
func LocateFFmpeg() (string, error) {
	ffmpegOnce.Do(func() {
		if p := os.Getenv("TORU_FFMPEG"); p != "" {
			if _, err := os.Stat(p); err == nil {
				ffmpegPath = p
				return
			}
		}
		if exe, err := os.Executable(); err == nil {
			beside := filepath.Join(filepath.Dir(exe), ffmpegName())
			if _, err := os.Stat(beside); err == nil {
				ffmpegPath = beside
				return
			}
		}
		if p, err := exec.LookPath(ffmpegName()); err == nil {
			ffmpegPath = p
			return
		}
		// TODO(installer): first-run download from a pinned LGPL build + SHA256 verify.
		ffmpegErr = fmt.Errorf("ffmpeg not found (set TORU_FFMPEG, stage it beside the app, or put it on PATH)")
	})
	return ffmpegPath, ffmpegErr
}

// ffprobeName returns the platform binary name for ffprobe.
func ffprobeName() string {
	if os.PathListSeparator == ';' { // Windows
		return "ffprobe.exe"
	}
	return "ffprobe"
}

// LocateFFprobe returns the path to a usable ffprobe binary (cached), using
// the same resolution order as ffmpeg: next to the resolved ffmpeg (covers
// both the TORU_FFMPEG override and the installer-staged layout), then PATH.
func LocateFFprobe() (string, error) {
	ffprobeOnce.Do(func() {
		if ff, err := LocateFFmpeg(); err == nil {
			beside := filepath.Join(filepath.Dir(ff), ffprobeName())
			if _, err := os.Stat(beside); err == nil {
				ffprobePath = beside
				return
			}
		}
		if p, err := exec.LookPath(ffprobeName()); err == nil {
			ffprobePath = p
			return
		}
		ffprobeErr = fmt.Errorf("ffprobe not found (expected beside ffmpeg or on PATH)")
	})
	return ffprobePath, ffprobeErr
}

var (
	ffprobeOnce sync.Once
	ffprobePath string
	ffprobeErr  error
)

// ProbeDurationMs returns path's container duration in milliseconds.
func ProbeDurationMs(ctx context.Context, path string) (int, error) {
	bin, err := LocateFFprobe()
	if err != nil {
		return 0, err
	}
	cmd := exec.CommandContext(ctx, bin,
		"-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path)
	configureSysProcAttr(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe duration: %w", err)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || sec <= 0 {
		return 0, fmt.Errorf("ffprobe duration: unparseable %q", strings.TrimSpace(string(out)))
	}
	return int(sec * 1000), nil
}

// RunFFmpeg locates ffmpeg and runs it with args, returning combined output on error.
func RunFFmpeg(ctx context.Context, args []string) error {
	bin, err := LocateFFmpeg()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	configureSysProcAttr(cmd) // no console flash from a -H windowsgui app
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w\n%s", err, out)
	}
	return nil
}
