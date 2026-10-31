package vid

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/fx/fxtest"
)

func withOptions(o ExportOptions, out string) ProjectExport {
	req := testProject()
	req.Preset = "original"
	req.OutPath = out
	req.Options = &o
	return req
}

func TestExportOptionsValidate(t *testing.T) {
	ok := []struct {
		o   ExportOptions
		out string
	}{
		{ExportOptions{Format: FormatMP4}, `C:\out\a.mp4`},
		{ExportOptions{Format: FormatHEVC, Width: 1920, Height: 1080, Fit: "fill", FPS: 30, Quality: "best", Encoder: "hevc_nvenc"}, `C:\out\a.MP4`},
		{ExportOptions{Format: FormatWebM, Kbps: 4000, NoAudio: true}, `C:\out\a.webm`},
		{ExportOptions{Format: FormatGIF, RangeIn: 1000, RangeOut: 3000, GIF: GIFOptions{FPS: 12, Width: 320, Loop: -1, Dither: "bayer", Colors: 64}}, `C:\out\a.gif`},
		{ExportOptions{Format: FormatM4A, AudioKbps: 256, SampleRate: 44100}, `C:\out\a.m4a`},
		{ExportOptions{Format: FormatMP4, Height: 720}, `C:\out\a.mp4`},
	}
	for _, c := range ok {
		if err := withOptions(c.o, c.out).Validate(); err != nil {
			t.Errorf("%+v: %v", c.o, err)
		}
	}
	bad := []struct {
		o    ExportOptions
		out  string
		want string
	}{
		{ExportOptions{Format: "avi"}, `C:\out\a.avi`, "unknown format"},
		{ExportOptions{Format: FormatGIF}, `C:\out\a.mp4`, "needs a .gif"},
		{ExportOptions{Format: FormatMP4, Width: 1921, Height: 1080}, `C:\out\a.mp4`, "even"},
		{ExportOptions{Format: FormatMP4, Width: 1920}, `C:\out\a.mp4`, "height with the width"},
		{ExportOptions{Format: FormatMP4, Fit: "stretch"}, `C:\out\a.mp4`, "fit"},
		{ExportOptions{Format: FormatMP4, FPS: 500}, `C:\out\a.mp4`, "frame rate"},
		{ExportOptions{Format: FormatMP4, Quality: "ultra"}, `C:\out\a.mp4`, "quality"},
		{ExportOptions{Format: FormatMP4, Encoder: "libvpx-vp9"}, `C:\out\a.mp4`, "can't make mp4"},
		{ExportOptions{Format: FormatMP4, SampleRate: 22050}, `C:\out\a.mp4`, "sample rate"},
		{ExportOptions{Format: FormatM4A, NoAudio: true}, `C:\out\a.m4a`, "needs its audio"},
		{ExportOptions{Format: FormatMP4, RangeIn: 3000, RangeOut: 2000}, `C:\out\a.mp4`, "range"},
		{ExportOptions{Format: FormatMP4, RangeOut: 999999}, `C:\out\a.mp4`, "range"},
		{ExportOptions{Format: FormatGIF, GIF: GIFOptions{Colors: 300}}, `C:\out\a.gif`, "colours"},
		{ExportOptions{Format: FormatGIF, GIF: GIFOptions{Dither: "atkinson"}}, `C:\out\a.gif`, "dither"},
	}
	for _, c := range bad {
		err := withOptions(c.o, c.out).Validate()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want %q", c.o, err, c.want)
		}
	}
	d := withOptions(ExportOptions{Format: FormatGIF}, `C:\out\a.gif`)
	d.Preset = "discord"
	if err := d.Validate(); err == nil {
		t.Error("discord + gif should be refused")
	}
}

func TestExportOptionsArgs(t *testing.T) {
	old := encoderUsable
	encoderUsable = func(n string) bool { return n == "hevc_nvenc" || n == "libvpx-vp9" }
	t.Cleanup(func() { encoderUsable = old })

	args := func(o ExportOptions, out string) string {
		t.Helper()
		a, err := BuildProjectArgs(withOptions(o, out), "h264_mf", "", "")
		if err != nil {
			t.Fatal(err)
		}
		return strings.Join(a, " ")
	}
	has := func(s string, want ...string) {
		t.Helper()
		for _, w := range want {
			if !strings.Contains(s, w) {
				t.Errorf("missing %q in\n%s", w, s)
			}
		}
	}
	hasNot := func(s string, bad ...string) {
		t.Helper()
		for _, b := range bad {
			if strings.Contains(s, b) {
				t.Errorf("unexpected %q in\n%s", b, s)
			}
		}
	}

	s := args(ExportOptions{Format: FormatMP4, Width: 1920, Height: 1080, FPS: 30, RangeIn: 500, RangeOut: 2500}, `C:\o\a.mp4`)
	has(s, "trim=start=0.500:end=2.500,setpts=PTS-STARTPTS,fps=30,scale=1920:1080:force_original_aspect_ratio=decrease", "pad=1920:1080",
		"atrim=start=0.500:end=2.500", "-c:v h264_mf -rate_control quality -quality 75", "-c:a aac -b:a 160k -ar 48000", "+faststart")

	s = args(ExportOptions{Format: FormatHEVC, Width: 1080, Height: 1920, Fit: "fill", Quality: "best"}, `C:\o\a.mp4`)
	has(s, "force_original_aspect_ratio=increase", "crop=1080:1920", "-c:v hevc_nvenc", "-cq 19", "-tag:v hvc1")

	s = args(ExportOptions{Format: FormatWebM, Kbps: 3000, NoAudio: true}, `C:\o\a.webm`)
	has(s, "-c:v libvpx-vp9 -b:v 3000k", "-an")
	has(s, "[aout]anullsink")
	hasNot(s, "libopus", "-map [aout]")

	s = args(ExportOptions{Format: FormatGIF, GIF: GIFOptions{FPS: 12, Width: 320, Loop: -1, Colors: 64, Dither: "bayer"}}, `C:\o\a.gif`)
	has(s, "fps=12,scale=320:-2:flags=lanczos,split[gifa][gifb]", "palettegen=max_colors=64", "paletteuse=dither=bayer", "-map [gif] -an", "-loop -1 -f gif")
	hasNot(s, "-c:a")

	s = args(ExportOptions{Format: FormatM4A, AudioKbps: 256, SampleRate: 44100, RangeIn: 1000}, `C:\o\a.m4a`)
	has(s, "atrim=start=1.000:end=4.000", "[o2]nullsink", "-vn", "-c:a aac -b:a 256k -ar 44100")
	hasNot(s, "-c:v")

	// Without options the preset path is unchanged.
	req := testProject()
	req.Preset, req.Height, req.OutPath = "custom", 480, `C:\o\a.mp4`
	a, err := BuildProjectArgs(req, "h264_mf", "", "")
	if err != nil {
		t.Fatal(err)
	}
	has(strings.Join(a, " "), "scale=-2:480")
}

// TestExportOptionsInFFmpeg exports every format through the pinned LGPL
// ffmpeg and checks what ffprobe sees: codecs, size, length (the range),
// GIF frame rate and loop, audio rate. Video formats whose encoder this PC
// lacks are skipped.
func TestExportOptionsInFFmpeg(t *testing.T) {
	ff := fxtest.PinnedFFmpeg(t)
	t.Setenv("TORU_FFMPEG", ff)
	probe := filepath.Join(filepath.Dir(ff), "ffprobe.exe")
	if _, err := os.Stat(probe); err != nil {
		t.Skip("no ffprobe next to", ff)
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=640x360:r=30:d=3", "-f", "lavfi", "-i", "sine=f=440:d=3",
		"-c:v", "ffv1", "-c:a", "pcm_s16le", src).CombinedOutput(); err != nil {
		t.Fatalf("make source: %v\n%s", err, out)
	}
	project := ProjectJSON{
		Version: 3,
		Canvas:  CanvasJSON{Width: 640, Height: 360, FPS: 30},
		Media:   []MediaJSON{{ID: "m", Path: src, Kind: "video", Duration: 3000, Width: 640, Height: 360, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0, IsMix: true}}}},
		Tracks: []TrackJSON{{ID: "v", Kind: "video", Clips: []ClipJSON{{ID: "c", Media: "m", In: 0, Out: 3000, Speed: 1, Volume: 1,
			Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}}}}},
	}
	caps := exportCapabilities()
	t.Logf("capabilities: %+v", caps)

	type stream struct {
		CodecType    string `json:"codec_type"`
		CodecName    string `json:"codec_name"`
		CodecTag     string `json:"codec_tag_string"`
		Width        int    `json:"width"`
		Height       int    `json:"height"`
		RFrameRate   string `json:"r_frame_rate"`
		SampleRate   string `json:"sample_rate"`
		NbReadFrames string `json:"nb_read_frames"`
	}
	type probed struct {
		Streams []stream `json:"streams"`
		Format  struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	run := func(name string, o ExportOptions) probed {
		t.Helper()
		out := filepath.Join(dir, name+formatExt[o.Format])
		req := ProjectExport{Project: project, Preset: "original", OutPath: out, Options: &o}
		if err := req.Validate(); err != nil {
			t.Fatal(err)
		}
		args, err := BuildProjectArgs(req, "", "", dir)
		if err != nil {
			t.Fatal(err)
		}
		if b, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s\nargs: %q", name, err, b, args)
		}
		b, err := exec.Command(probe, "-v", "error", "-count_frames", "-show_entries",
			"stream=codec_type,codec_name,codec_tag_string,width,height,r_frame_rate,sample_rate,nb_read_frames:format=duration", "-of", "json", out).Output()
		if err != nil {
			t.Fatalf("%s: ffprobe: %v", name, err)
		}
		var p probed
		if err := json.Unmarshal(b, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	dur := func(p probed) float64 { f, _ := strconv.ParseFloat(p.Format.Duration, 64); return f }
	kinds := func(p probed) string {
		var s []string
		for _, st := range p.Streams {
			s = append(s, st.CodecType+":"+st.CodecName)
		}
		return strings.Join(s, " ")
	}
	near := func(name string, got, want, tol float64) {
		t.Helper()
		if got < want-tol || got > want+tol {
			t.Errorf("%s: %.3f, want %.3f ± %.2f", name, got, want, tol)
		}
	}

	for _, f := range []string{FormatMP4, FormatHEVC, FormatWebM} {
		if len(caps.Encoders[f]) == 0 {
			t.Logf("%s: no usable encoder here, skipped", f)
			continue
		}
		p := run(f, ExportOptions{Format: f, Width: 480, Height: 480, Fit: "fit", FPS: 24, RangeIn: 500, RangeOut: 2500, Quality: "medium", SampleRate: 44100})
		t.Logf("%s: %s, %.3f s", f, kinds(p), dur(p))
		near(f+" length", dur(p), 2.0, 0.1)
		v := p.Streams[0]
		if v.Width != 480 || v.Height != 480 || v.RFrameRate != "24/1" {
			t.Errorf("%s: video %dx%d @ %s, want 480x480 @ 24/1", f, v.Width, v.Height, v.RFrameRate)
		}
		if f == FormatHEVC && v.CodecTag != "hvc1" {
			t.Errorf("hevc: tag %q, want hvc1", v.CodecTag)
		}
		if len(p.Streams) != 2 {
			t.Errorf("%s: want video + audio, got %s", f, kinds(p))
		} else if f != FormatWebM && p.Streams[1].SampleRate != "44100" {
			t.Errorf("%s: audio at %s Hz, want 44100", f, p.Streams[1].SampleRate)
		}
	}
	if len(caps.Encoders[FormatMP4]) > 0 {
		p := run("bitrate-noaudio", ExportOptions{Format: FormatMP4, Kbps: 800, NoAudio: true})
		if len(p.Streams) != 1 || p.Streams[0].CodecType != "video" {
			t.Errorf("no-audio mp4: %s", kinds(p))
		}
	}

	g := run("gif", ExportOptions{Format: FormatGIF, RangeIn: 1000, RangeOut: 2000, GIF: GIFOptions{FPS: 10, Width: 320, Colors: 64, Dither: "bayer"}})
	t.Logf("gif: %s %dx%d frames %s, %.3f s", kinds(g), g.Streams[0].Width, g.Streams[0].Height, g.Streams[0].NbReadFrames, dur(g))
	if kinds(g) != "video:gif" || g.Streams[0].Width != 320 || g.Streams[0].Height != 180 {
		t.Errorf("gif: %s %dx%d, want one 320x180 gif stream", kinds(g), g.Streams[0].Width, g.Streams[0].Height)
	}
	if n, _ := strconv.Atoi(g.Streams[0].NbReadFrames); n < 9 || n > 11 {
		t.Errorf("gif: %d frames, want ~10 (1 s at 10 fps)", n)
	}

	a := run("audio", ExportOptions{Format: FormatM4A, AudioKbps: 128, SampleRate: 44100, RangeOut: 1500})
	if kinds(a) != "audio:aac" || a.Streams[0].SampleRate != "44100" {
		t.Errorf("m4a: %s at %s Hz", kinds(a), a.Streams[0].SampleRate)
	}
	near("m4a length", dur(a), 1.5, 0.1)
}

func TestExportPath(t *testing.T) {
	dir := t.TempDir()
	p, taken, err := exportPath(dir, ` my: clip?.mp4 `, FormatGIF)
	if err != nil || taken || p != filepath.Join(dir, "my- clip-.gif") {
		t.Fatalf("got %q %v %v", p, taken, err)
	}
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	p2, taken, err := exportPath(dir, "my: clip?", FormatGIF)
	if err != nil || !taken || p2 != filepath.Join(dir, "my- clip- (2).gif") {
		t.Fatalf("got %q %v %v", p2, taken, err)
	}
	if _, _, err := exportPath("", "x", FormatMP4); err == nil {
		t.Error("no folder should fail")
	}
	if p, _, _ := exportPath(dir, "...", FormatM4A); filepath.Base(p) != "video.m4a" {
		t.Errorf("empty name: %q", p)
	}
}
