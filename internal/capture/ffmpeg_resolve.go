package capture

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ffmpeg_resolve.go locates and runs the FFmpeg binary. FFmpeg is NOT embedded
// (//go:embed of a ~100MB binary bloats the build) or shipped in the
// installer (every auto-update would re-download it); when none is found, the
// app downloads a pinned build once (ffmpeg_fetch.go).
//
// Resolution order:
//  1. $TORU_FFMPEG (explicit override; dev convenience)
//  2. ffmpeg.exe next to the running executable (portable layout)
//  3. the downloaded copy (ManagedFFmpegDir)
//  4. ffmpeg(.exe) on PATH
//
// Only a successful lookup is cached, so a download that lands while the app
// runs is picked up without a restart.

var (
	ffmpegMu   sync.Mutex
	ffmpegPath string
)

// errFFmpegMissing is LocateFFmpeg's error when no binary is found; callers
// test for it with IsFFmpegMissing.
var errFFmpegMissing = errors.New("ffmpeg not found (set TORU_FFMPEG, stage it beside the app, or put it on PATH)")

// IsFFmpegMissing reports whether err means there is no ffmpeg to run.
func IsFFmpegMissing(err error) bool { return errors.Is(err, errFFmpegMissing) }

func ffmpegName() string {
	if os.PathListSeparator == ';' { // Windows
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

// LocateFFmpeg returns the path to a usable ffmpeg binary (cached once found).
func LocateFFmpeg() (string, error) {
	ffmpegMu.Lock()
	defer ffmpegMu.Unlock()
	if ffmpegPath != "" {
		return ffmpegPath, nil
	}
	var candidates []string
	if p := os.Getenv("TORU_FFMPEG"); p != "" {
		candidates = append(candidates, p)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Join(filepath.Dir(exe), ffmpegName()))
	}
	if dir := ManagedFFmpegDir(); dir != "" {
		candidates = append(candidates, filepath.Join(dir, ffmpegName()))
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			ffmpegPath = p
			return p, nil
		}
	}
	if p, err := exec.LookPath(ffmpegName()); err == nil {
		ffmpegPath = p
		return p, nil
	}
	return "", errFFmpegMissing
}

// ffprobeName returns the platform binary name for ffprobe.
func ffprobeName() string {
	if os.PathListSeparator == ';' { // Windows
		return "ffprobe.exe"
	}
	return "ffprobe"
}

// LocateFFprobe returns the path to a usable ffprobe binary (cached once
// found), using the same resolution order as ffmpeg: next to the resolved
// ffmpeg (covers the TORU_FFMPEG override, the portable layout and the
// downloaded copy), then PATH.
func LocateFFprobe() (string, error) {
	ffprobeMu.Lock()
	defer ffprobeMu.Unlock()
	if ffprobePath != "" {
		return ffprobePath, nil
	}
	if ff, err := LocateFFmpeg(); err == nil {
		beside := filepath.Join(filepath.Dir(ff), ffprobeName())
		if _, err := os.Stat(beside); err == nil {
			ffprobePath = beside
			return beside, nil
		}
	}
	if p, err := exec.LookPath(ffprobeName()); err == nil {
		ffprobePath = p
		return p, nil
	}
	return "", fmt.Errorf("ffprobe not found (expected beside ffmpeg or on PATH)")
}

var (
	ffprobeMu   sync.Mutex
	ffprobePath string
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

// ProbeVideoSize returns the first video stream's width and height.
func ProbeVideoSize(ctx context.Context, path string) (int, int, error) {
	bin, err := LocateFFprobe()
	if err != nil {
		return 0, 0, err
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error", "-select_streams", "v:0",
		"-show_entries", "stream=width,height", "-of", "csv=p=0", path)
	configureSysProcAttr(cmd)
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, fmt.Errorf("ffprobe size: %w", err)
	}
	var w, h int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d,%d", &w, &h); err != nil || w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("ffprobe size: unparseable %q", strings.TrimSpace(string(out)))
	}
	return w, h, nil
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
