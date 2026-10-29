package vid

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

// clip1440 is a 1440p/144fps clip with separate tracks: 0 = mix, 1 = game,
// 2 = mic. rec1080 is a 1080p/60fps recording with one track. silent has no
// audio at all.
var (
	clip1440 = MediaInfo{DurationMs: 120_000, Width: 3440, Height: 1440, FPS: 144, HasAudio: true,
		Tracks: []AudioTrack{{Index: 0, Title: "Mix", IsMix: true}, {Index: 1, Title: "Game"}, {Index: 2, Title: "Mic"}}}
	rec1080 = MediaInfo{DurationMs: 60_000, Width: 1920, Height: 1080, FPS: 60, HasAudio: true,
		Tracks: []AudioTrack{{Index: 0}}}
	silent = MediaInfo{DurationMs: 10_000, Width: 1280, Height: 720, FPS: 29.97002997}
)

func TestBuildEditArgs(t *testing.T) {
	const scale1440 = "scale=3440:1440:force_original_aspect_ratio=decrease,pad=3440:1440:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=144,format=yuv420p"
	cases := []struct {
		name string
		req  EditRequest
		info []MediaInfo
		text string
		want string // the whole arg list, space-joined
	}{
		{
			name: "one segment, original",
			req: EditRequest{Sources: []string{`C:\v\a.mp4`}, Segments: []Segment{{0, 1500, 4250}},
				Preset: "original", OutPath: `C:\v\a-edited.mp4`},
			info: []MediaInfo{clip1440},
			want: `-y -ss 1.500 -t 2.750 -i C:\v\a.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,` + scale1440 + `[v0];` +
				`[0:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a0];` +
				`[v0][a0]concat=n=1:v=1:a=1[cv][ca] -map [cv] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart C:\v\a-edited.mp4`,
		},
		{
			name: "three cuts out of one clip, kept parts in order",
			req: EditRequest{Sources: []string{"a.mp4"},
				Segments: []Segment{{0, 0, 10_000}, {0, 20_000, 30_000}, {0, 40_000, 50_000}, {0, 60_000, 120_000}},
				Preset:   "original", OutPath: "out.mp4"},
			info: []MediaInfo{clip1440},
			want: `-y -ss 0.000 -t 10.000 -i a.mp4 -ss 20.000 -t 10.000 -i a.mp4 -ss 40.000 -t 10.000 -i a.mp4 -ss 60.000 -t 60.000 -i a.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,` + scale1440 + `[v0];[0:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a0];` +
				`[1:v]setpts=PTS-STARTPTS,` + scale1440 + `[v1];[1:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a1];` +
				`[2:v]setpts=PTS-STARTPTS,` + scale1440 + `[v2];[2:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a2];` +
				`[3:v]setpts=PTS-STARTPTS,` + scale1440 + `[v3];[3:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a3];` +
				`[v0][a0][v1][a1][v2][a2][v3][a3]concat=n=4:v=1:a=1[cv][ca] -map [cv] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart out.mp4`,
		},
		{
			name: "two sources of different size and fps fit the first; silent source gets silence",
			req: EditRequest{Sources: []string{"a.mp4", "b.mp4", "c.mp4"},
				Segments: []Segment{{1, 0, 5000}, {0, 1000, 2000}, {2, 9000, 12_000}},
				Preset:   "original", OutPath: "out.mp4"},
			info: []MediaInfo{silent, rec1080, silent},
			want: `-y -ss 0.000 -t 5.000 -i b.mp4 -ss 1.000 -t 1.000 -i a.mp4 -ss 9.000 -t 1.000 -i c.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=29.97,format=yuv420p[v0];` +
				`[0:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a0];` +
				`[1:v]setpts=PTS-STARTPTS,scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=29.97,format=yuv420p[v1];` +
				`anullsrc=r=48000:cl=stereo:d=1.000,` + silentFormat + `[a1];` +
				`[2:v]setpts=PTS-STARTPTS,scale=1280:720:force_original_aspect_ratio=decrease,pad=1280:720:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=29.97,format=yuv420p[v2];` +
				`anullsrc=r=48000:cl=stereo:d=1.000,` + silentFormat + `[a2];` +
				`[v0][a0][v1][a1][v2][a2]concat=n=3:v=1:a=1[cv][ca] -map [cv] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart out.mp4`,
		},
		{
			name: "muted mic, game at 150%, mixed",
			req: EditRequest{Sources: []string{"a.mp4"}, Segments: []Segment{{0, 0, 2000}},
				Tracks: []TrackLevel{{Source: 0, Index: 1, Volume: 150}, {Source: 0, Index: 2, Volume: 100, Muted: true}},
				Preset: "original", OutPath: "out.mp4"},
			info: []MediaInfo{clip1440},
			want: `-y -ss 0.000 -t 2.000 -i a.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,` + scale1440 + `[v0];` +
				`[0:a:1]asetpts=PTS-STARTPTS,` + silentFormat + `,volume=1.50[a0];` +
				`[v0][a0]concat=n=1:v=1:a=1[cv][ca] -map [cv] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart out.mp4`,
		},
		{
			name: "two source tracks summed without normalizing",
			req: EditRequest{Sources: []string{"a.mp4"}, Segments: []Segment{{0, 0, 2000}},
				Tracks: []TrackLevel{{Source: 0, Index: 1, Volume: 100}, {Source: 0, Index: 2, Volume: 50}},
				Preset: "original", OutPath: "out.mp4"},
			info: []MediaInfo{clip1440},
			want: `-y -ss 0.000 -t 2.000 -i a.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,` + scale1440 + `[v0];` +
				`[0:a:1]asetpts=PTS-STARTPTS,` + silentFormat + `[a0_0];` +
				`[0:a:2]asetpts=PTS-STARTPTS,` + silentFormat + `,volume=0.50[a0_1];` +
				`[a0_0][a0_1]amix=inputs=2:duration=longest:normalize=0[a0];` +
				`[v0][a0]concat=n=1:v=1:a=1[cv][ca] -map [cv] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart out.mp4`,
		},
		{
			name: "every listed track muted: silence",
			req: EditRequest{Sources: []string{"a.mp4"}, Segments: []Segment{{0, 0, 2000}},
				Tracks: []TrackLevel{{Source: 0, Index: 0, Volume: 100, Muted: true}},
				Preset: "original", OutPath: "out.mp4"},
			info: []MediaInfo{rec1080},
			want: `-y -ss 0.000 -t 2.000 -i a.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2,setsar=1,fps=60,format=yuv420p[v0];` +
				`anullsrc=r=48000:cl=stereo:d=2.000,` + silentFormat + `[a0];` +
				`[v0][a0]concat=n=1:v=1:a=1[cv][ca] -map [cv] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart out.mp4`,
		},
		{
			name: "crop, text and 1080p: crop is under 1080 tall so no scale",
			req: EditRequest{Sources: []string{"a.mp4"}, Segments: []Segment{{0, 0, 2000}},
				Crop: &capture.Rect{X: 101, Y: 51, W: 1001, H: 901}, Preset: "1080p", OutPath: "out.mp4"},
			info: []MediaInfo{clip1440},
			text: "drawtext=textfile=t0.txt",
			want: `-y -ss 0.000 -t 2.000 -i a.mp4 -filter_complex ` +
				`[0:v]setpts=PTS-STARTPTS,` + scale1440 + `[v0];[0:a:0]asetpts=PTS-STARTPTS,` + silentFormat + `[a0];` +
				`[v0][a0]concat=n=1:v=1:a=1[cv][ca];[cv]crop=1000:900:100:50,drawtext=textfile=t0.txt[vout] -map [vout] -map [ca] ` +
				`-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart out.mp4`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.req.Validate(c.info); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := strings.Join(BuildEditArgs(c.req, c.info, "libx264", c.text), " ")
			if got != c.want {
				t.Errorf("args\n got: %s\nwant: %s", got, c.want)
			}
		})
	}
}

// Each preset's scale and codec args: only the tail after the concat differs.
func TestBuildEditArgsPresets(t *testing.T) {
	base := EditRequest{Sources: []string{"a.mp4"}, Segments: []Segment{{0, 0, 1000}}}
	cases := []struct {
		preset, out string
		height      int
		kbps        int
		crop        *capture.Rect
		encoder     string
		wantPost    string // "" = video maps [cv] straight
		wantCodec   string
	}{
		{preset: "original", out: "o.mp4", encoder: "libx264", wantCodec: "-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "1080p", out: "o.mp4", encoder: "libx264", wantPost: "scale=-2:1080", wantCodec: "-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "720p", out: "o.mp4", encoder: "h264_nvenc", wantPost: "scale=-2:720",
			wantCodec: "-c:v h264_nvenc -preset p5 -rc vbr -cq 23 -b:v 0 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "discord", out: "o.mp4", encoder: "h264_mf", wantCodec: "-c:v h264_mf -rate_control quality -quality 75 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "custom", out: "o.mp4", height: 540, kbps: 4000, encoder: "h264_amf", wantPost: "scale=-2:540",
			wantCodec: "-c:v h264_amf -b:v 4000k -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "custom", out: "o.mp4", height: 1440, encoder: "libx264", // taller than the frame: no upscale, no bitrate: quality mode
			wantCodec: "-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "720p", out: "o.mp4", encoder: "libx264", crop: &capture.Rect{W: 1280, H: 700}, // crop already under 720
			wantPost: "crop=1280:700:0:0", wantCodec: "-c:v libx264 -pix_fmt yuv420p -c:a aac -b:a 192k -movflags +faststart"},
		{preset: "original", out: "o.webm", encoder: "libx264",
			wantCodec: "-c:v libvpx-vp9 -b:v 0 -crf 30 -row-mt 1 -pix_fmt yuv420p -c:a libopus -b:a 128k"},
		{preset: "custom", out: "o.WEBM", height: 720, kbps: 2500, encoder: "libx264", wantPost: "scale=-2:720",
			wantCodec: "-c:v libvpx-vp9 -b:v 2500k -row-mt 1 -pix_fmt yuv420p -c:a libopus -b:a 128k"},
	}
	for _, c := range cases {
		t.Run(c.preset+"/"+c.out, func(t *testing.T) {
			req := base
			req.Preset, req.OutPath, req.Height, req.Kbps, req.Crop = c.preset, c.out, c.height, c.kbps, c.crop
			info := []MediaInfo{rec1080}
			info[0].Height = 1440 // a 1440-tall frame so the presets scale down
			if err := req.Validate(info); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			got := strings.Join(BuildEditArgs(req, info, c.encoder, ""), " ")
			_, tail, ok := strings.Cut(got, "concat=n=1:v=1:a=1[cv][ca]")
			if !ok {
				t.Fatalf("no concat in %s", got)
			}
			want := " -map [cv] -map [ca] " + c.wantCodec + " " + c.out
			if c.wantPost != "" {
				want = ";[cv]" + c.wantPost + "[vout] -map [vout] -map [ca] " + c.wantCodec + " " + c.out
			}
			if tail != want {
				t.Errorf("tail\n got: %s\nwant: %s", tail, want)
			}
		})
	}
}

// A segment running past the source's end is cut at the end, so a silent
// source's anullsrc matches its frames and progress sums the real length.
func TestEditSegmentClampedToSource(t *testing.T) {
	req := EditRequest{Sources: []string{"a.mp4", "b.mp4"}, Segments: []Segment{{0, 0, 2000}, {1, 8000, 15_000}},
		Preset: "original", OutPath: "o.mp4"}
	info := []MediaInfo{rec1080, silent}
	if err := req.Validate(info); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(BuildEditArgs(req, info, "libx264", ""), " ")
	for _, want := range []string{"-ss 8.000 -t 2.000 -i b.mp4", "anullsrc=r=48000:cl=stereo:d=2.000,"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if d := EditDurationMs(req, info); d != 4000 {
		t.Errorf("EditDurationMs = %d, want 4000", d)
	}
}

func TestEditFrameSize(t *testing.T) {
	info := []MediaInfo{{Width: 1921, Height: 1081}}
	if w, h := EditFrameSize(EditRequest{}, info); w != 1920 || h != 1080 {
		t.Errorf("no crop: %dx%d", w, h)
	}
	if w, h := EditFrameSize(EditRequest{Crop: &capture.Rect{W: 641, H: 361}}, info); w != 640 || h != 360 {
		t.Errorf("crop: %dx%d", w, h)
	}
}

func TestEditValidate(t *testing.T) {
	info := []MediaInfo{clip1440, silent}
	ok := EditRequest{Sources: []string{`C:\v\a.mp4`, `C:\v\b.mp4`}, Segments: []Segment{{0, 0, 1000}, {1, 0, 1000}},
		Preset: "original", OutPath: `C:\v\out.mp4`}
	if err := ok.Validate(info); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	cases := []struct {
		name string
		mod  func(r *EditRequest)
		info []MediaInfo
	}{
		{"no sources", func(r *EditRequest) { r.Sources = nil }, nil},
		{"probe count", func(r *EditRequest) {}, info[:1]},
		{"no segments", func(r *EditRequest) { r.Segments = nil }, nil},
		{"segment source", func(r *EditRequest) { r.Segments[0].Source = 2 }, nil},
		{"empty range", func(r *EditRequest) { r.Segments[0].EndMs = 0 }, nil},
		{"negative start", func(r *EditRequest) { r.Segments[0].StartMs = -1 }, nil},
		{"starts past end", func(r *EditRequest) { r.Segments[1] = Segment{1, 10_000, 11_000} }, nil},
		{"track source", func(r *EditRequest) { r.Tracks = []TrackLevel{{Source: 5, Volume: 100}} }, nil},
		{"track index", func(r *EditRequest) { r.Tracks = []TrackLevel{{Source: 0, Index: 3, Volume: 100}} }, nil},
		{"track on silent source", func(r *EditRequest) { r.Tracks = []TrackLevel{{Source: 1, Volume: 100}} }, nil},
		{"volume", func(r *EditRequest) { r.Tracks = []TrackLevel{{Source: 0, Index: 1, Volume: 201}} }, nil},
		{"crop outside", func(r *EditRequest) { r.Crop = &capture.Rect{X: 3000, W: 500, H: 100} }, nil},
		{"crop tiny", func(r *EditRequest) { r.Crop = &capture.Rect{W: 1, H: 100} }, nil},
		{"preset", func(r *EditRequest) { r.Preset = "4k" }, nil},
		{"custom odd height", func(r *EditRequest) { r.Preset, r.Height = "custom", 721 }, nil},
		{"custom no height", func(r *EditRequest) { r.Preset = "custom" }, nil},
		{"no out path", func(r *EditRequest) { r.OutPath = "" }, nil},
		{"overwrites a source", func(r *EditRequest) { r.OutPath = `c:\V\B.mp4` }, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ok
			r.Segments = append([]Segment{}, ok.Segments...)
			c.mod(&r)
			in := info
			if c.info != nil {
				in = c.info
			}
			if err := r.Validate(in); err == nil {
				t.Errorf("accepted")
			}
		})
	}
}
