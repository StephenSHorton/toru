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
	"strings"

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

// ExportForDiscord re-encodes to a ~9MB size target (Discord free cap is 10MB).
//
// TODO(dev2): two-pass bitrate targeting based on duration.
func (s *VideoService) ExportForDiscord(videoPath string) (string, error) {
	return "", fmt.Errorf("vid.ExportForDiscord: not implemented yet (Phase 0 stub)")
}
