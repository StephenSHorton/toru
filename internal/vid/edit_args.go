package vid

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/StephenSHorton/toru/internal/capture"
)

// edit_args.go turns an EditRequest into ffmpeg args. PURE: no ffmpeg calls,
// no file access, so every shape is table-testable.
//
// Each segment is its OWN input ("-ss start -t len -i source"), not a trim
// filter on one shared input. Input seeking jumps straight to the cut (a cut
// at minute 25 of a long recording doesn't decode the first 25 minutes), and
// segments reordered against source time don't make ffmpeg buffer raw frames
// for a later segment while concat still waits on an earlier one. When
// re-encoding, input -ss is frame-accurate (ffmpeg decodes from the keyframe
// before and drops frames up to the cut).

// silentFormat is the one audio format every segment is brought to before
// concat, the same as the replay mix.
const silentFormat = "aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo"

// BuildEditArgs builds the whole export: per-segment inputs, scale/pad/fps to
// Sources[0], the per-track audio mix, concat, crop, textFilter (a filter
// chain from TextFilter, "" for none) and the preset's scale. encoder is the
// H.264 encoder for MP4 output (capture.TrimH264Encoder()); WebM output uses
// VP9/Opus like trims. The "discord" preset encodes like "original": the
// caller then runs ExportForDiscord on the result.
//
// req must have passed req.Validate(info).
func BuildEditArgs(req EditRequest, info []MediaInfo, encoder string, textFilter string) []string {
	w, h := evenDim(info[0].Width), evenDim(info[0].Height)

	args := []string{"-y"}
	for _, seg := range req.Segments {
		args = append(args,
			"-ss", msSeconds(seg.StartMs),
			"-t", msSeconds(segmentMs(seg, info)),
			"-i", req.Sources[seg.Source])
	}

	var graph []string
	var concatIn strings.Builder
	for i, seg := range req.Segments {
		v := fmt.Sprintf("[%d:v]setpts=PTS-STARTPTS,scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2,setsar=1", i, w, h, w, h)
		if fps := info[0].FPS; fps > 0 {
			v += ",fps=" + formatFPS(fps)
		}
		graph = append(graph, v+fmt.Sprintf(",format=yuv420p[v%d]", i))
		graph = append(graph, segmentAudio(i, seg, req.Tracks, info)...)
		fmt.Fprintf(&concatIn, "[v%d][a%d]", i, i)
	}
	graph = append(graph, fmt.Sprintf("%sconcat=n=%d:v=1:a=1[cv][ca]", concatIn.String(), len(req.Segments)))

	var post []string
	frameH := h
	if req.Crop != nil {
		post = append(post, capture.CropFilter(*req.Crop))
		frameH = evenDim(req.Crop.H)
	}
	if textFilter != "" {
		post = append(post, textFilter)
	}
	if target := presetHeight(req); target > 0 && frameH > target {
		post = append(post, fmt.Sprintf("scale=-2:%d", target))
	}
	videoOut := "[cv]"
	if len(post) > 0 {
		graph = append(graph, "[cv]"+strings.Join(post, ",")+"[vout]")
		videoOut = "[vout]"
	}

	args = append(args, "-filter_complex", strings.Join(graph, ";"), "-map", videoOut, "-map", "[ca]")
	args = append(args, editCodecArgs(req, encoder)...)
	args = append(args, capture.ContainerFlags(req.OutPath)...)
	return append(args, req.OutPath)
}

// EditFrameSize is the output frame before the preset's scale: Sources[0]'s
// size, or the crop's. Text overlays are laid out in it.
func EditFrameSize(req EditRequest, info []MediaInfo) (w, h int) {
	if req.Crop != nil {
		return evenDim(req.Crop.W), evenDim(req.Crop.H)
	}
	return evenDim(info[0].Width), evenDim(info[0].Height)
}

// EditDurationMs is the export's length: the kept segments summed (for
// progress).
func EditDurationMs(req EditRequest, info []MediaInfo) int {
	total := 0
	for _, seg := range req.Segments {
		total += segmentMs(seg, info)
	}
	return total
}

// segmentAudio returns the filter chains that leave segment i's audio on
// [a<i>]: the source's listed tracks at their levels (summed when several),
// its mix track at 100% when none are listed, or silence of the segment's
// length when the source has no audio or every listed track is muted.
func segmentAudio(i int, seg Segment, levels []TrackLevel, info []MediaInfo) []string {
	src := info[seg.Source]
	type track struct{ index, volume int }
	var picked []track
	listed := false
	for _, l := range levels {
		if l.Source != seg.Source {
			continue
		}
		listed = true
		if !l.Muted && l.Volume > 0 {
			picked = append(picked, track{l.Index, l.Volume})
		}
	}
	if !listed && src.HasAudio && len(src.Tracks) > 0 {
		picked = []track{{mixTrack(src), 100}}
	}
	out := fmt.Sprintf("[a%d]", i)
	if len(picked) == 0 {
		return []string{fmt.Sprintf("anullsrc=r=48000:cl=stereo:d=%s,%s%s", msSeconds(segmentMs(seg, info)), silentFormat, out)}
	}

	chains := make([]string, 0, len(picked)+1)
	var mixIn strings.Builder
	for j, t := range picked {
		chain := fmt.Sprintf("[%d:a:%d]asetpts=PTS-STARTPTS,%s", i, t.index, silentFormat)
		if t.volume != 100 {
			chain += fmt.Sprintf(",volume=%.2f", float64(t.volume)/100)
		}
		if len(picked) == 1 {
			chains = append(chains, chain+out)
			continue
		}
		label := fmt.Sprintf("[a%d_%d]", i, j)
		chains = append(chains, chain+label)
		mixIn.WriteString(label)
	}
	if len(picked) > 1 {
		chains = append(chains, fmt.Sprintf("%samix=inputs=%d:duration=longest:normalize=0%s", mixIn.String(), len(picked), out))
	}
	return chains
}

// mixTrack is the track a source plays when the request lists none of its
// tracks: the one marked as the mix, else the first.
func mixTrack(m MediaInfo) int {
	for _, t := range m.Tracks {
		if t.IsMix {
			return t.Index
		}
	}
	return m.Tracks[0].Index
}

// editCodecArgs picks codecs from the output container: WebM can only carry
// VP8/VP9/AV1 + Opus/Vorbis, MP4 gets H.264 + AAC. Custom with a bitrate sets
// -b:v instead of the encoder's quality mode.
func editCodecArgs(req EditRequest, encoder string) []string {
	kbps := 0
	if req.Preset == "custom" {
		kbps = req.Kbps
	}
	var args []string
	if strings.EqualFold(filepath.Ext(req.OutPath), ".webm") {
		if kbps > 0 {
			args = []string{"-c:v", "libvpx-vp9", "-b:v", fmt.Sprintf("%dk", kbps), "-row-mt", "1"}
		} else {
			args = []string{"-c:v", "libvpx-vp9", "-b:v", "0", "-crf", "30", "-row-mt", "1"}
		}
		return append(args, "-pix_fmt", "yuv420p", "-c:a", "libopus", "-b:a", "128k")
	}
	if kbps > 0 {
		args = []string{"-c:v", encoder, "-b:v", fmt.Sprintf("%dk", kbps)}
	} else {
		args = capture.TrimH264Args(encoder)
	}
	return append(args, "-pix_fmt", "yuv420p", "-c:a", "aac", "-b:a", "192k")
}

// presetHeight is the height the preset scales down to (0 = keep).
func presetHeight(req EditRequest) int {
	switch req.Preset {
	case "1080p":
		return 1080
	case "720p":
		return 720
	case "custom":
		return req.Height
	}
	return 0
}

// segmentMs is the segment's length, cut short at the source's end when the
// probe knows it (so silence for a video-only source matches its frames).
func segmentMs(seg Segment, info []MediaInfo) int {
	end := seg.EndMs
	if d := info[seg.Source].DurationMs; d > 0 && end > d {
		end = d
	}
	return max(end-seg.StartMs, 0)
}

// msSeconds formats ms as seconds for -ss/-t ("12.345").
func msSeconds(ms int) string {
	return fmt.Sprintf("%d.%03d", ms/1000, ms%1000)
}

// formatFPS keeps three decimals at most (59.94005994 -> "59.94").
func formatFPS(fps float64) string {
	return strconv.FormatFloat(math.Round(fps*1000)/1000, 'f', -1, 64)
}

// evenDim rounds a frame dimension down to even (yuv420p needs it).
func evenDim(n int) int {
	n &^= 1
	return max(n, 2)
}

// sameFile reports whether a and b name the same path (case-insensitively:
// Windows paths).
func sameFile(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
