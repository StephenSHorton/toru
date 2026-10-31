package vid

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ExportOptions are the export screen's settings. When a ProjectExport
// carries them, they replace the preset's size and codec choice; without
// them (nil) the preset path is unchanged. The Discord preset still applies
// on top (render re-encodes the result to fit 10 MB).
type ExportOptions struct {
	// Format is the output: "mp4" (H.264 + AAC), "hevc" (H.265 + AAC in
	// MP4), "webm" (VP9 + Opus), "gif" (no audio) or "m4a" (audio only,
	// AAC). OutPath's extension must match it.
	Format string `json:"format"`
	// Width x Height is the output frame (even numbers); 0 x 0 keeps the
	// canvas (or crop). Width 0 with a height scales to that height and
	// keeps the aspect. Ignored by gif (GIF.Width) and m4a.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Fit is how a picture with another aspect goes into Width x Height:
	// "fit" (whole picture, black bars; the default) or "fill" (cropped to
	// fill the frame).
	Fit string `json:"fit,omitempty"`
	// FPS is the output frame rate; 0 keeps the canvas rate.
	FPS float64 `json:"fps,omitempty"`
	// Quality is "low", "medium", "high" (the default) or "best": the
	// encoder's own quality mode. Kbps > 0 sets a bitrate instead.
	Quality string `json:"quality,omitempty"`
	Kbps    int    `json:"kbps,omitempty"`
	// Encoder is a video encoder for the format (ExportCapabilities lists
	// the usable ones); "" picks the best one.
	Encoder string `json:"encoder,omitempty"`
	// NoAudio leaves the sound out (mp4, hevc, webm).
	NoAudio bool `json:"noAudio,omitempty"`
	// AudioKbps (default 160) and SampleRate (44100 or 48000, the default;
	// webm is always 48000).
	AudioKbps  int `json:"audioKbps,omitempty"`
	SampleRate int `json:"sampleRate,omitempty"`
	// RangeIn..RangeOut (ms on the timeline) is the part to export;
	// RangeOut 0 means the end.
	RangeIn  int `json:"rangeIn,omitempty"`
	RangeOut int `json:"rangeOut,omitempty"`
	// GIF settings, used when Format is "gif".
	GIF GIFOptions `json:"gif,omitempty"`
}

// GIFOptions shape an animated GIF: a palette made for the clip
// (palettegen), then each frame mapped onto it (paletteuse).
type GIFOptions struct {
	FPS    float64 `json:"fps,omitempty"`    // default 15, 1..50
	Width  int     `json:"width,omitempty"`  // default 480; height follows the aspect
	Loop   int     `json:"loop,omitempty"`   // 0 forever (default), -1 play once, n repeats n more times
	Dither string  `json:"dither,omitempty"` // sierra2_4a (default), floyd_steinberg, bayer, none
	Colors int     `json:"colors,omitempty"` // 2..256, default 256
}

// Export formats.
const (
	FormatMP4  = "mp4"
	FormatHEVC = "hevc"
	FormatWebM = "webm"
	FormatGIF  = "gif"
	FormatM4A  = "m4a"
)

var formatExt = map[string]string{FormatMP4: ".mp4", FormatHEVC: ".mp4", FormatWebM: ".webm", FormatGIF: ".gif", FormatM4A: ".m4a"}

// Encoder candidates per format, best first: the software encoder when the
// ffmpeg build has it, then the GPU's, then Windows' own.
var formatEncoders = map[string][]string{
	FormatMP4:  {"libx264", "h264_nvenc", "h264_amf", "h264_qsv", "h264_mf"},
	FormatHEVC: {"libx265", "hevc_nvenc", "hevc_amf", "hevc_qsv", "hevc_mf"},
	FormatWebM: {"libvpx-vp9"},
}

var gifDithers = []string{"sierra2_4a", "floyd_steinberg", "bayer", "none"}

// ExportCapabilities is what this PC's ffmpeg can export: the usable video
// encoders per format (best first). A format with no encoder is missing
// from Encoders; gif and m4a need none.
type ExportCapabilities struct {
	Formats  []string            `json:"formats"`
	Encoders map[string][]string `json:"encoders"`
}

// encoderUsable is capture.EncoderUsable, a var so tests don't depend on the
// machine's GPU.
var encoderUsable = capture.EncoderUsable

// ExportCapabilities probes the encoders (each probe is cached for the
// session).
func (s *VideoService) ExportCapabilities() ExportCapabilities {
	return exportCapabilities()
}

func exportCapabilities() ExportCapabilities {
	c := ExportCapabilities{Encoders: map[string][]string{}}
	for _, f := range []string{FormatMP4, FormatHEVC, FormatWebM} {
		for _, e := range formatEncoders[f] {
			if encoderUsable(e) {
				c.Encoders[f] = append(c.Encoders[f], e)
			}
		}
		if len(c.Encoders[f]) > 0 {
			c.Formats = append(c.Formats, f)
		}
	}
	c.Formats = append(c.Formats, FormatGIF, FormatM4A)
	return c
}

// validate checks the options against the project's length (ms) and the
// output path.
func (o ExportOptions) validate(totalMs int, outPath string) error {
	ext, ok := formatExt[o.Format]
	if !ok {
		return fmt.Errorf("export: unknown format %q (mp4, hevc, webm, gif or m4a)", o.Format)
	}
	if !strings.EqualFold(filepath.Ext(outPath), ext) {
		return fmt.Errorf("export: a %s export needs a %s file name", o.Format, ext)
	}
	if o.Width < 0 || o.Height < 0 || o.Width%2 != 0 || o.Height%2 != 0 || o.Width > 7680 || o.Height > 4320 {
		return fmt.Errorf("export: size %dx%d must be even numbers up to 7680x4320", o.Width, o.Height)
	}
	if o.Width > 0 && o.Height == 0 {
		return errors.New("export: give a height with the width")
	}
	if o.Height > 0 && o.Height < 16 || o.Width > 0 && o.Width < 16 {
		return fmt.Errorf("export: size %dx%d is too small", o.Width, o.Height)
	}
	if o.Fit != "" && o.Fit != "fit" && o.Fit != "fill" {
		return fmt.Errorf("export: fit %q (fit or fill)", o.Fit)
	}
	if o.FPS != 0 && (o.FPS < 1 || o.FPS > 240) {
		return fmt.Errorf("export: frame rate %g (1..240)", o.FPS)
	}
	if o.Quality != "" && qualityStep(o.Quality) < 0 {
		return fmt.Errorf("export: quality %q (low, medium, high or best)", o.Quality)
	}
	if o.Kbps != 0 && (o.Kbps < 100 || o.Kbps > 200000) {
		return fmt.Errorf("export: video bitrate %d kbps (100..200000)", o.Kbps)
	}
	if o.Encoder != "" && !slices.Contains(formatEncoders[o.Format], o.Encoder) {
		return fmt.Errorf("export: encoder %q can't make %s (%s)", o.Encoder, o.Format, strings.Join(formatEncoders[o.Format], ", "))
	}
	if o.AudioKbps != 0 && (o.AudioKbps < 32 || o.AudioKbps > 512) {
		return fmt.Errorf("export: audio bitrate %d kbps (32..512)", o.AudioKbps)
	}
	if o.SampleRate != 0 && o.SampleRate != 44100 && o.SampleRate != 48000 {
		return fmt.Errorf("export: sample rate %d (44100 or 48000)", o.SampleRate)
	}
	if o.Format == FormatM4A && o.NoAudio {
		return errors.New("export: an audio-only export needs its audio")
	}
	in, out := o.rangeMs(totalMs)
	if o.RangeIn < 0 || o.RangeOut < 0 || out > totalMs || out-in < 100 {
		return fmt.Errorf("export: range %s..%s is outside the %s timeline (or under 0.1 s)", msSeconds(o.RangeIn), msSeconds(out), msSeconds(totalMs))
	}
	g := o.GIF
	if g.FPS != 0 && (g.FPS < 1 || g.FPS > 50) {
		return fmt.Errorf("export: GIF frame rate %g (1..50)", g.FPS)
	}
	if g.Width != 0 && (g.Width < 16 || g.Width > 3840) {
		return fmt.Errorf("export: GIF width %d (16..3840)", g.Width)
	}
	if g.Loop < -1 {
		return fmt.Errorf("export: GIF loop %d (0 forever, -1 once, or a count)", g.Loop)
	}
	if g.Dither != "" && !slices.Contains(gifDithers, g.Dither) {
		return fmt.Errorf("export: GIF dither %q (%s)", g.Dither, strings.Join(gifDithers, ", "))
	}
	if g.Colors != 0 && (g.Colors < 2 || g.Colors > 256) {
		return fmt.Errorf("export: GIF colours %d (2..256)", g.Colors)
	}
	return nil
}

// rangeMs is the exported part of a totalMs timeline.
func (o ExportOptions) rangeMs(totalMs int) (int, int) {
	out := o.RangeOut
	if out == 0 {
		out = totalMs
	}
	return o.RangeIn, out
}

// ExportLengthMs is how long the export runs: the range, or the whole
// project.
func (req ProjectExport) ExportLengthMs() int {
	total := ProjectDurationMs(req.Project)
	if req.Options == nil {
		return total
	}
	in, out := req.Options.rangeMs(total)
	return out - in
}

// qualityStep is low 0 .. best 3 ("" = high), -1 when unknown.
func qualityStep(q string) int {
	switch q {
	case "low":
		return 0
	case "medium":
		return 1
	case "", "high":
		return 2
	case "best":
		return 3
	}
	return -1
}

// pickEncoder is o.Encoder, or the format's best usable encoder.
func (o ExportOptions) pickEncoder(fallbackH264 string) string {
	if o.Encoder != "" {
		return o.Encoder
	}
	if o.Format == FormatMP4 && fallbackH264 != "" {
		return fallbackH264
	}
	for _, e := range formatEncoders[o.Format] {
		if encoderUsable(e) {
			return e
		}
	}
	return formatEncoders[o.Format][len(formatEncoders[o.Format])-1]
}

// videoCodecArgs is the encoder plus its rate control: the quality step on
// each encoder's own scale, or a bitrate.
func (o ExportOptions) videoCodecArgs(enc string) []string {
	args := []string{"-c:v", enc}
	step := qualityStep(o.Quality)
	pick := func(vals ...int) string { return strconv.Itoa(vals[step]) }
	switch {
	case o.Kbps > 0:
		args = append(args, "-b:v", fmt.Sprintf("%dk", o.Kbps), "-maxrate", fmt.Sprintf("%dk", o.Kbps*3/2), "-bufsize", fmt.Sprintf("%dk", o.Kbps*2))
	case enc == "libvpx-vp9":
		args = append(args, "-b:v", "0", "-crf", pick(40, 35, 31, 24))
	case enc == "libx264" || enc == "libx265":
		args = append(args, "-crf", pick(30, 26, 22, 18))
	case strings.HasSuffix(enc, "_nvenc"):
		args = append(args, "-preset", "p5", "-rc", "vbr", "-cq", pick(32, 28, 23, 19), "-b:v", "0")
	case strings.HasSuffix(enc, "_amf"):
		args = append(args, "-rc", "qvbr", "-qvbr_quality_level", pick(32, 28, 23, 19))
	case strings.HasSuffix(enc, "_qsv"):
		args = append(args, "-global_quality", pick(32, 28, 23, 19))
	case strings.HasSuffix(enc, "_mf"):
		args = append(args, "-rate_control", "quality", "-quality", pick(45, 60, 75, 90))
	}
	if enc == "libvpx-vp9" {
		args = append(args, "-row-mt", "1")
	}
	if o.Format == FormatHEVC {
		args = append(args, "-tag:v", "hvc1") // plays in more players than hev1
	}
	return append(args, "-pix_fmt", "yuv420p")
}

func (o ExportOptions) audioCodecArgs() []string {
	kbps := o.AudioKbps
	if kbps == 0 {
		kbps = 160
	}
	rate := o.SampleRate
	if rate == 0 || o.Format == FormatWebM {
		rate = 48000 // Opus runs at 48 kHz
	}
	codec := "aac"
	if o.Format == FormatWebM {
		codec = "libopus"
	}
	return []string{"-c:a", codec, "-b:a", fmt.Sprintf("%dk", kbps), "-ar", strconv.Itoa(rate)}
}

// sizeFilters scales the picture into Width x Height (fit: letterboxed,
// fill: cropped), or to Height keeping the aspect.
func (o ExportOptions) sizeFilters() []string {
	switch {
	case o.Height == 0:
		return nil
	case o.Width == 0:
		return []string{fmt.Sprintf("scale=-2:%d:flags=lanczos", o.Height)}
	case o.Fit == "fill":
		return []string{
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase:flags=lanczos", o.Width, o.Height),
			fmt.Sprintf("crop=%d:%d", o.Width, o.Height),
			"setsar=1",
		}
	default:
		return []string{
			fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:flags=lanczos", o.Width, o.Height),
			fmt.Sprintf("pad=%d:%d:(ow-iw)/2:(oh-ih)/2:color=black", o.Width, o.Height),
			"setsar=1",
		}
	}
}

// optionsTail finishes BuildProjectArgs for an export with options: the
// range, size and rate after the crop/text post chain, then the format's
// streams and codecs. graph already holds the timeline; videoOut and
// audioOut are its labels.
func optionsTail(args, graph []string, videoOut, audioOut string, post []string, totalMs int, req ProjectExport, encoder, work string) ([]string, error) {
	o := *req.Options
	in, out := o.rangeMs(totalMs)
	var trimV, trimA []string
	if in > 0 || out < totalMs {
		trimV = []string{fmt.Sprintf("trim=start=%s:end=%s", msSeconds(in), msSeconds(out)), "setpts=PTS-STARTPTS"}
		trimA = []string{fmt.Sprintf("atrim=start=%s:end=%s", msSeconds(in), msSeconds(out)), "asetpts=PTS-STARTPTS"}
	}

	var maps, codec []string
	switch o.Format {
	case FormatM4A:
		if len(trimA) > 0 {
			graph = append(graph, audioOut+strings.Join(trimA, ",")+"[aexp]")
			audioOut = "[aexp]"
		}
		// Every graph output must go somewhere: the picture to a null sink.
		graph = append(graph, videoOut+"nullsink")
		maps = []string{"-map", audioOut, "-vn"}
		codec = o.audioCodecArgs()
	case FormatGIF:
		g := o.GIF
		fps, width, colors, dither := g.FPS, g.Width, g.Colors, g.Dither
		if fps == 0 {
			fps = 15
		}
		if width == 0 {
			width = 480
		}
		if colors == 0 {
			colors = 256
		}
		if dither == "" {
			dither = "sierra2_4a"
		}
		chain := append(append(append([]string{}, post...), trimV...), "fps="+formatFPS(fps), fmt.Sprintf("scale=%d:-2:flags=lanczos", width))
		graph = append(graph,
			videoOut+strings.Join(chain, ",")+",split[gifa][gifb]",
			fmt.Sprintf("[gifa]palettegen=max_colors=%d:stats_mode=diff[gifp]", colors),
			fmt.Sprintf("[gifb][gifp]paletteuse=dither=%s:diff_mode=rectangle[gif]", dither),
			audioOut+"anullsink",
		)
		maps = []string{"-map", "[gif]", "-an"}
		codec = []string{"-loop", strconv.Itoa(g.Loop), "-f", "gif"}
	default:
		chain := append(append([]string{}, post...), trimV...)
		if o.FPS > 0 {
			chain = append(chain, "fps="+formatFPS(o.FPS))
		}
		chain = append(chain, o.sizeFilters()...)
		if len(chain) > 0 {
			graph = append(graph, videoOut+strings.Join(chain, ",")+"[vexp]")
			videoOut = "[vexp]"
		}
		maps = []string{"-map", videoOut}
		codec = o.videoCodecArgs(o.pickEncoder(encoder))
		if o.NoAudio {
			graph = append(graph, audioOut+"anullsink")
			maps = append(maps, "-an")
		} else {
			if len(trimA) > 0 {
				graph = append(graph, audioOut+strings.Join(trimA, ",")+"[aexp]")
				audioOut = "[aexp]"
			}
			maps = append(maps, "-map", audioOut)
			codec = append(codec, o.audioCodecArgs()...)
		}
	}

	fc, err := filterComplexArgs(strings.Join(graph, ";"), work)
	if err != nil {
		return nil, err
	}
	args = append(args, fc...)
	args = append(args, maps...)
	args = append(args, codec...)
	args = append(args, capture.ContainerFlags(req.OutPath)...)
	return append(args, req.OutPath), nil
}

// PickExportFolder shows a folder picker starting at start (the export
// screen's current folder). It returns "" when cancelled.
func (s *VideoService) PickExportFolder(start string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("choose folder: no running application")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle("Export to folder")
	if start != "" {
		if fi, err := os.Stat(start); err == nil && fi.IsDir() {
			dlg = dlg.SetDirectory(start)
		}
	}
	if win := app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		return "", nil // cancelled
	}
	return chosen, nil
}

// ExportPath joins dir, name and the format's extension into a path that
// isn't taken yet: "name.mp4", else "name (2).mp4", "name (3).mp4", ...
// Characters Windows refuses in a file name are replaced with "-". taken
// says whether that name was in use (so the screen can say it picked
// another).
func (s *VideoService) ExportPath(dir, name, format string) (path string, taken bool, err error) {
	return exportPath(dir, name, format)
}

func exportPath(dir, name, format string) (string, bool, error) {
	ext, ok := formatExt[format]
	if !ok {
		return "", false, fmt.Errorf("export: unknown format %q", format)
	}
	if strings.TrimSpace(dir) == "" {
		return "", false, errors.New("export: choose a folder")
	}
	name = strings.TrimSpace(name)
	if e := filepath.Ext(name); e != "" && slices.Contains([]string{".mp4", ".webm", ".gif", ".m4a"}, strings.ToLower(e)) {
		name = strings.TrimSuffix(name, e)
	}
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, name)
	name = strings.TrimRight(name, ". ")
	if name == "" {
		name = "video"
	}
	for i := 1; i < 1000; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s (%d)", name, i)
		}
		p := filepath.Join(dir, n+ext)
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			return p, i > 1, nil
		}
	}
	return "", false, fmt.Errorf("export: too many files named %s in %s", name, dir)
}
