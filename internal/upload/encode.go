package upload

import (
	"fmt"
	"strconv"

	"github.com/StephenSHorton/toru/internal/capture"
)

// maxUploadBytes is catbox.moe's per-file limit (200 MB).
const maxUploadBytes = 200 << 20

// targetBytes leaves headroom under the limit for mux overhead and rate
// control overshoot.
const targetBytes = 185 << 20

const audioKbps = 128

// encodePlan is the output format for a shared clip.
type encodePlan struct {
	Height, FPS, VideoKbps int
}

// planEncode picks the best quality that fits targetBytes for a clip of
// durMs: 1080p60 at up to 16 Mbps, stepping down to 720p60 and then 720p30
// as the clip gets longer and the per-second budget shrinks.
func planEncode(durMs int) encodePlan {
	sec := max(float64(durMs)/1000, 1)
	budget := int(float64(targetBytes)*8/1000/sec) - audioKbps // kbps for video
	switch {
	case budget >= 6000:
		return encodePlan{1080, 60, min(budget, 16000)}
	case budget >= 2500:
		return encodePlan{720, 60, budget}
	default:
		return encodePlan{720, 30, max(budget, 300)}
	}
}

// pickEncoder prefers the GPU H.264 encoder (fast, no CPU load) and falls
// back to x264.
func pickEncoder() string {
	for _, enc := range []string{"h264_nvenc", "h264_amf", "h264_qsv"} {
		if capture.EncoderUsable(enc) {
			return enc
		}
	}
	return "libx264"
}

// compressArgs re-encodes [startMs, endMs] of in to an H.264/AAC MP4 that
// plays everywhere (Discord embeds, phones, browsers). Only the first audio
// track (the mix) is kept. Never upscales.
func compressArgs(in, out string, startMs, endMs int, p encodePlan, enc string) []string {
	rate := fmt.Sprintf("%dk", p.VideoKbps)
	args := []string{"-hide_banner", "-loglevel", "error", "-y"}
	if startMs > 0 {
		args = append(args, "-ss", msToSec(startMs))
	}
	args = append(args, "-i", in, "-t", msToSec(endMs-startMs),
		"-map", "0:v:0", "-map", "0:a:0?",
		"-vf", fmt.Sprintf("fps=%d,scale=-2:'min(%d,ih)':flags=bicubic,format=yuv420p", p.FPS, p.Height),
		"-c:v", enc)
	switch enc {
	case "h264_nvenc":
		args = append(args, "-preset", "p5", "-rc", "vbr")
	case "h264_amf":
		args = append(args, "-quality", "quality", "-rc", "vbr_peak")
	case "h264_qsv":
		args = append(args, "-preset", "medium")
	default:
		args = append(args, "-preset", "veryfast")
	}
	return append(args,
		"-b:v", rate, "-maxrate", rate, "-bufsize", fmt.Sprintf("%dk", p.VideoKbps*2),
		"-profile:v", "high", "-g", strconv.Itoa(p.FPS*2),
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", audioKbps),
		"-movflags", "+faststart", out)
}

func msToSec(ms int) string { return strconv.FormatFloat(float64(ms)/1000, 'f', 3, 64) }
