package capture

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// encoders.go owns the CODEC POLICY (docs/PLAN.md §10) and runtime encoder
// probing for the video recording path.
//
// Committed licensing decision (§10): the OUT-OF-BOX recording output is VP9
// in WebM — royalty-free, zero AVC patent obligation, and present in LGPL
// FFmpeg builds (libvpx is BSD). H.264/MP4 is OPT-IN (TORU_CODEC=h264) and
// uses HARDWARE encoders ONLY (nvenc/qsv/amf) on machines that already have
// them; libx264 is never used for recording (GPL + AVC royalty exposure).

// VideoEncoder is one fully-resolved codec choice for a recording: the -c:v
// name, the container extension that matches it (WebM cannot legally carry
// H.264; MP4 carrying VP9 has poor player support), and the rate/tuning args.
// Args are baked per-request because the bitrate target scales with rect area.
type VideoEncoder struct {
	Name string   // ffmpeg -c:v value
	Ext  string   // output container extension: ".webm" | ".mp4"
	Args []string // tuning args, inserted after `-c:v <Name>`
}

// hwH264Encoders are probed IN ORDER when H.264 is requested. All are
// hardware: we never select a software H.264 encoder (see policy above).
var hwH264Encoders = []string{"h264_nvenc", "h264_qsv", "h264_amf"}

// SelectEncoder resolves the codec policy for one recording request.
//
// Default: VP9/WebM (no probing needed — libvpx ships in every FFmpeg build
// we target). TORU_CODEC=h264 opts into MP4 via the first USABLE hardware
// encoder; if none probes usable the policy falls back to VP9/WebM rather
// than failing the recording.
func SelectEncoder(req CaptureRequest) VideoEncoder {
	if os.Getenv("TORU_CODEC") == "h264" {
		if enc, ok := hwH264For(req); ok {
			return enc
		}
	}
	return vp9For(req)
}

// vp9For returns the default VP9/WebM encoder tuned for real-time capture.
// libvpx-vp9 is NOT realtime-safe with default settings; deadline=realtime +
// cpu-used=8 + row-mt are required to hold 60fps at desktop resolutions.
func vp9For(req CaptureRequest) VideoEncoder {
	return VideoEncoder{
		Name: "libvpx-vp9",
		Ext:  ".webm",
		Args: []string{
			"-b:v", targetBitrate(req.Rect.W, req.Rect.H),
			"-deadline", "realtime",
			"-cpu-used", "8",
			"-row-mt", "1",
		},
	}
}

// hwH264For returns the first hardware H.264 encoder that probes usable.
func hwH264For(req CaptureRequest) (VideoEncoder, bool) {
	for _, name := range hwH264Encoders {
		if EncoderUsable(name) {
			return VideoEncoder{
				Name: name,
				Ext:  ".mp4",
				Args: []string{"-b:v", targetBitrate(req.Rect.W, req.Rect.H)},
			}, true
		}
	}
	return VideoEncoder{}, false
}

// targetBitrate computes a screen-recording bitrate target of ~0.1 bits per
// pixel at 60fps (1080p ≈ 12.4 Mbps, 1440p ≈ 22 Mbps), clamped to [2, 30]
// Mbps so tiny crops stay legible and 4K doesn't produce unshareable files.
func targetBitrate(w, h int) string {
	bps := int64(w) * int64(h) * 60 / 10
	const minBps, maxBps = 2_000_000, 30_000_000
	if bps < minBps {
		bps = minBps
	}
	if bps > maxBps {
		bps = maxBps
	}
	return fmt.Sprintf("%dk", bps/1000)
}

// encoder probe cache — an encoder can be LISTED by `ffmpeg -encoders` yet
// unusable at runtime (nvenc without an NVIDIA GPU, qsv without QuickSync),
// so the only trustworthy check is a tiny real encode.
var (
	probeMu    sync.Mutex
	probeCache = map[string]bool{}
)

// EncoderUsable reports whether `name` can actually encode on this machine,
// verified by a ~3-frame lavfi test encode to the null muxer (cached).
func EncoderUsable(name string) bool {
	probeMu.Lock()
	defer probeMu.Unlock()
	if ok, seen := probeCache[name]; seen {
		return ok
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := RunFFmpeg(ctx, []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=256x256:r=30",
		"-frames:v", "3",
		"-c:v", name,
		"-f", "null", "-",
	})
	probeCache[name] = err == nil
	return err == nil
}
