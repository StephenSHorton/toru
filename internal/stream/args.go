package stream

import (
	"fmt"
	"strconv"
	"strings"
)

// args.go builds the encoder ffmpeg command lines. Kept pure for tests.

// rtpPacketSize leaves room for SRTP, the TURN/ICE headers and IPv6 inside a
// 1500-byte Ethernet MTU.
const rtpPacketSize = 1200

// videoArgs is the low-latency capture → hardware encode → RTP command.
// zeroCopy keeps frames on the GPU from ddagrab to the encoder; it is only
// possible at the monitor's own size (scaling goes through system memory).
func videoArgs(ddaIdx int, s Settings, enc string, zeroCopy bool, url string) []string {
	graph := fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:draw_mouse=%s",
		ddaIdx, s.FPS, boolArg(s.Cursor))
	if !zeroCopy {
		graph += ",hwdownload,format=bgra"
		if s.Width > 0 && s.Height > 0 {
			graph += fmt.Sprintf(",scale=%d:%d:flags=bicubic", s.Width, s.Height)
		}
		if strings.HasSuffix(enc, "_qsv") {
			graph += ",format=nv12" // QSV won't take BGRA system frames
		}
	}
	args := []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-filter_complex", graph,
	}
	args = append(args, encoderArgs(enc, s)...)
	if s.Codec == CodecAV1 {
		// ffmpeg still labels its AV1 RTP packetizer experimental.
		args = append(args, "-strict", "experimental")
	}
	return append(args,
		"-flush_packets", "1",
		"-f", "rtp", "-pkt_size", strconv.Itoa(rtpPacketSize),
		url,
	)
}

// encoderArgs tunes each hardware encoder for the lowest latency: no
// B-frames (they hold frames back), no lookahead, constant bitrate, and a
// full frame every KeyframeSec so new viewers (and ones that lost packets)
// can start decoding.
func encoderArgs(enc string, s Settings) []string {
	rate := fmt.Sprintf("%dk", s.BitrateKbps)
	// A quarter second of VBV keeps keyframes sharp without letting the rate
	// controller bank a long burst.
	buf := fmt.Sprintf("%dk", max(s.BitrateKbps/4, 1))
	args := []string{"-c:v", enc}
	switch {
	case strings.HasSuffix(enc, "_nvenc"):
		preset := "p4"
		if s.FPS > 120 {
			preset = "p2" // keep the encoder ahead of very high frame rates
		}
		args = append(args,
			"-preset", preset, "-tune", "ull",
			"-rc", "cbr", "-multipass", "disabled",
			"-zerolatency", "1", "-delay", "0",
			"-bf", "0", "-forced-idr", "1",
		)
		if enc == "h264_nvenc" {
			args = append(args, "-profile:v", "high")
		}
	case strings.HasSuffix(enc, "_amf"):
		args = append(args, "-usage", "ultralowlatency", "-quality", "speed", "-rc", "cbr", "-bf", "0")
	case strings.HasSuffix(enc, "_qsv"):
		args = append(args, "-preset", "veryfast", "-async_depth", "1", "-bf", "0")
	}
	return append(args,
		"-b:v", rate, "-maxrate", rate, "-bufsize", buf,
		"-g", strconv.Itoa(s.FPS*s.KeyframeSec),
	)
}

// audioArgs encodes the desktop-audio loopback pipe to Opus RTP with 10 ms
// frames (Opus's low-delay mode; WebRTC's default is 20 ms).
func audioArgs(fmtName string, rate, channels, kbps int, pipe, url string) []string {
	return []string{
		"-hide_banner", "-loglevel", "warning", "-nostdin",
		"-f", fmtName, "-ar", strconv.Itoa(rate), "-ac", strconv.Itoa(channels),
		"-i", pipe,
		"-af", "aformat=sample_fmts=flt:sample_rates=48000:channel_layouts=stereo",
		"-c:a", "libopus", "-b:a", fmt.Sprintf("%dk", kbps),
		"-application", "lowdelay", "-frame_duration", "10",
		"-flush_packets", "1",
		"-f", "rtp", "-pkt_size", strconv.Itoa(rtpPacketSize),
		url,
	}
}

func boolArg(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
