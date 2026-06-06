package capture

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// args.go is the SOLE owner of translating a CaptureRequest's virtual-desktop
// Rect into FFmpeg command-line arguments. It is the single place where the
// gdigrab (virtual-desktop coords) vs ddagrab (monitor-relative + output_idx)
// difference is handled. Keep these functions PURE (request -> []string) so they
// are golden-testable per the plan's testing strategy.

// findScreen returns the ScreenInfo whose ID matches id.
func findScreen(screens []ScreenInfo, id int) (ScreenInfo, error) {
	for _, s := range screens {
		if s.ID == id {
			return s, nil
		}
	}
	return ScreenInfo{}, fmt.Errorf("no screen with id %d", id)
}

// BuildVideoArgsDDA builds ffmpeg args for the GPU ddagrab path.
//
// ddagrab offsets are MONITOR-RELATIVE and the monitor is chosen by output_idx,
// so the virtual-desktop Rect MUST be rebased: offset = Rect - screen origin.
func BuildVideoArgsDDA(req CaptureRequest, screens []ScreenInfo, enc VideoEncoder, outPath string) ([]string, error) {
	screen, err := findScreen(screens, req.MonitorID)
	if err != nil {
		return nil, err
	}
	relX := req.Rect.X - screen.X // <-- THE rebasing. Do not remove.
	relY := req.Rect.Y - screen.Y
	dda := fmt.Sprintf(
		"ddagrab=output_idx=%d:framerate=60:video_size=%dx%d:offset_x=%d:offset_y=%d:draw_mouse=%s",
		req.MonitorID, req.Rect.W, req.Rect.H, relX, relY, boolToInt(req.IncludeCursor),
	)
	args := []string{
		"-y",
		"-filter_complex", dda + ",hwdownload,format=bgra",
		"-c:v", enc.Name, // selected by the codec policy in encoders.go
	}
	args = append(args, enc.Args...)
	args = append(args, "-pix_fmt", "yuv420p")
	args = append(args, containerFlags(outPath)...)
	args = append(args,
		"-g", "60", // keyframe every 60f so -c copy trims land <=1s off
		outPath,
	)
	return args, nil
}

// BuildVideoArgsGDI builds ffmpeg args for the software gdigrab fallback.
//
// gdigrab offsets ARE virtual-desktop coordinates (negatives allowed), so the
// Rect is used DIRECTLY with no rebasing.
func BuildVideoArgsGDI(req CaptureRequest, enc VideoEncoder, outPath string) []string {
	args := []string{
		"-y",
		"-f", "gdigrab",
		"-framerate", "60",
		"-offset_x", strconv.Itoa(req.Rect.X), // <-- direct, NOT rebased.
		"-offset_y", strconv.Itoa(req.Rect.Y),
		"-video_size", fmt.Sprintf("%dx%d", req.Rect.W, req.Rect.H),
		"-draw_mouse", boolToInt(req.IncludeCursor),
		"-i", "desktop",
		"-c:v", enc.Name, // selected by the codec policy in encoders.go
	}
	args = append(args, enc.Args...)
	args = append(args, "-pix_fmt", "yuv420p")
	args = append(args, containerFlags(outPath)...)
	args = append(args, "-g", "60", outPath)
	return args
}

// containerFlags returns muxer-specific flags for outPath's container.
// `-movflags +faststart` is a mov/mp4 PRIVATE option: passing it to the WebM
// muxer is an error, so it must be gated on the extension, not always-on.
func containerFlags(outPath string) []string {
	switch strings.ToLower(filepath.Ext(outPath)) {
	case ".mp4", ".mov", ".m4v":
		return []string{"-movflags", "+faststart"}
	default:
		return nil
	}
}

// BuildStillFallbackArgs builds ffmpeg args for a single-frame gdigrab grab,
// used only when DXGI duplication is unavailable (some RDP/VM sessions). The
// default still path is in-process DXGI (still_dxgi.go), not this.
func BuildStillFallbackArgs(req CaptureRequest, outPath string) []string {
	return []string{
		"-y",
		"-f", "gdigrab",
		"-offset_x", strconv.Itoa(req.Rect.X),
		"-offset_y", strconv.Itoa(req.Rect.Y),
		"-video_size", fmt.Sprintf("%dx%d", req.Rect.W, req.Rect.H),
		"-i", "desktop",
		"-frames:v", "1",
		outPath,
	}
}

// BuildTrimArgs builds ffmpeg args for Developer 2's trim. Precise=false uses
// stream-copy (fast, snaps to nearest preceding keyframe); Precise=true
// re-encodes for frame accuracy.
//
// The precise re-encode codec follows the OUTPUT container: WebM (the default
// recording container, see encoders.go) can only carry VP8/VP9/AV1 + Opus/
// Vorbis — emitting H.264/AAC into it is an error.
func BuildTrimArgs(req TrimRequest) []string {
	ss := msToTimecode(req.StartMs)
	to := msToTimecode(req.EndMs)
	if req.Precise {
		args := []string{"-y", "-i", req.VideoPath, "-ss", ss, "-to", to}
		if strings.EqualFold(filepath.Ext(req.OutPath), ".webm") {
			args = append(args, "-c:v", "libvpx-vp9", "-b:v", "0", "-crf", "30", "-c:a", "libopus")
		} else {
			args = append(args, "-c:v", "libx264", "-c:a", "aac")
		}
		args = append(args, containerFlags(req.OutPath)...)
		return append(args, req.OutPath)
	}
	// -ss before -i for fast seek; -c copy for lossless stream copy.
	args := []string{"-y", "-ss", ss, "-to", to, "-i", req.VideoPath, "-c", "copy"}
	args = append(args, containerFlags(req.OutPath)...)
	return append(args, req.OutPath)
}

func boolToInt(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// msToTimecode formats milliseconds as HH:MM:SS.mmm for ffmpeg -ss/-to.
func msToTimecode(ms int) string {
	if ms < 0 {
		ms = 0
	}
	h := ms / 3_600_000
	ms -= h * 3_600_000
	m := ms / 60_000
	ms -= m * 60_000
	s := ms / 1000
	ms -= s * 1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}
