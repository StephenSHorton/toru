// Package vid is DEVELOPER 2's territory: the video/trim service. It builds
// FFmpeg argument lists via the shared internal/capture arg-builders (so the
// coordinate/seam logic stays in one place) and runs them through the shared
// ffmpeg resolver.
//
// JS binding name: VideoService.*
package vid

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// Service is the Wails-bound video API (Developer 2).
type VideoService struct{}

// New returns the video service.
func New() *VideoService { return &VideoService{} }

// Trim cuts [StartMs, EndMs] from req.VideoPath into req.OutPath. Precise=false
// is a fast stream-copy (snaps to nearest preceding keyframe); Precise=true
// re-encodes for frame accuracy.
func (s *VideoService) Trim(req capture.TrimRequest) (string, error) {
	if req.OutPath == "" {
		ext := filepath.Ext(req.VideoPath)
		req.OutPath = strings.TrimSuffix(req.VideoPath, ext) + "-trimmed" + ext
	}
	args := capture.BuildTrimArgs(req)
	if err := capture.RunFFmpeg(context.Background(), args); err != nil {
		return "", fmt.Errorf("trim: %w", err)
	}
	return req.OutPath, nil
}

// GenerateThumbnails extracts `count` evenly-spaced frames for the trim
// filmstrip and returns their file paths.
//
// TODO(dev2): compute timestamps from duration (ffprobe) and emit a tiled
// filmstrip; this stub wires the resolver + output dir only.
func (s *VideoService) GenerateThumbnails(videoPath string, count int) ([]string, error) {
	if count <= 0 {
		count = 10
	}
	if _, err := os.Stat(videoPath); err != nil {
		return nil, fmt.Errorf("thumbnails: %w", err)
	}
	return nil, fmt.Errorf("vid.GenerateThumbnails(%d): not implemented yet (Phase 0 stub)", count)
}

// discordTargetBytes is the size we aim for: Discord's free-tier cap is 10MB,
// and two-pass rate control lands NEAR the target, not exactly on it — the
// 1MB headroom absorbs mux overhead and rate-control overshoot.
const discordTargetBytes = 9_000_000

// minDiscordBps is the bitrate floor below which desktop-resolution VP9 turns
// to mush; under it the export also downscales to 720p so the budget is spent
// on fewer pixels.
const minDiscordBps = 600_000

// ExportForDiscord re-encodes videoPath to fit Discord's 10MB free-tier cap
// using two-pass VP9 targeting ~9MB, with the bitrate computed from the
// clip's real duration. Sources already under the target are returned as-is
// (sharing the original beats a pointless re-encode).
func (s *VideoService) ExportForDiscord(videoPath string) (string, error) {
	fi, err := os.Stat(videoPath)
	if err != nil {
		return "", fmt.Errorf("discord export: %w", err)
	}
	if fi.Size() <= discordTargetBytes {
		return videoPath, nil
	}

	// Long re-encodes are expected (two full passes); bound, don't hang.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	durMs, err := capture.ProbeDurationMs(ctx, videoPath)
	if err != nil {
		return "", fmt.Errorf("discord export: %w", err)
	}
	bps := discordBitrateBps(durMs)

	ext := filepath.Ext(videoPath)
	outPath := strings.TrimSuffix(videoPath, ext) + "-discord.webm"
	// Pass logs go to the temp dir (ffmpeg writes <passlogfile>-0.log); never
	// pollute the working directory and always clean up.
	passlog := filepath.Join(os.TempDir(), "toru", fmt.Sprintf("ffpass-%d", time.Now().UnixNano()))
	defer func() { _ = os.Remove(passlog + "-0.log") }()

	common := []string{
		"-y", "-i", videoPath,
		"-c:v", "libvpx-vp9",
		"-b:v", fmt.Sprintf("%dk", bps/1000),
		"-row-mt", "1",
		"-passlogfile", passlog,
	}
	if bps <= minDiscordBps {
		common = append(common, "-vf", "scale=-2:720")
	}
	// Pass 1: fast analysis, no output file. Pass 2: the real encode (slower
	// cpu-used buys quality at the locked bitrate).
	pass1 := append(append([]string{}, common...), "-pass", "1", "-cpu-used", "4", "-an", "-f", "null", nullDevice())
	pass2 := append(append([]string{}, common...), "-pass", "2", "-cpu-used", "2", outPath)
	if err := capture.RunFFmpeg(ctx, pass1); err != nil {
		return "", fmt.Errorf("discord export (pass 1): %w", err)
	}
	if err := capture.RunFFmpeg(ctx, pass2); err != nil {
		return "", fmt.Errorf("discord export (pass 2): %w", err)
	}

	// The whole point is the cap — verify rather than hope.
	out, err := os.Stat(outPath)
	if err != nil {
		return "", fmt.Errorf("discord export: %w", err)
	}
	if out.Size() > 10_000_000 {
		return "", fmt.Errorf("discord export: result is %.1fMB, still over Discord's 10MB cap", float64(out.Size())/1e6)
	}
	return outPath, nil
}

// discordBitrateBps spreads the ~9MB byte budget across the clip's duration.
// No audio track exists yet (mic capture lands v1.1), so video gets it all.
func discordBitrateBps(durMs int) int64 {
	if durMs <= 0 {
		return minDiscordBps
	}
	return int64(discordTargetBytes) * 8 * 1000 / int64(durMs)
}

// nullDevice is the discard sink for ffmpeg pass-1 output.
func nullDevice() string {
	if runtime.GOOS == "windows" {
		return "NUL"
	}
	return "/dev/null"
}
