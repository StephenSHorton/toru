package vid

import (
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

func testProject() ProjectExport {
	return ProjectExport{
		Project: ProjectJSON{
			Version: 2,
			Canvas:  CanvasJSON{Width: 1280, Height: 720, FPS: 60},
			Media: []MediaJSON{
				{ID: "game", Path: `C:\clips\game.mp4`, Kind: "video", Duration: 30000, Width: 2560, Height: 1440, FPS: 144, HasAudio: true,
					Tracks: []AudioTrack{{Index: 0, IsMix: true}, {Index: 1, Title: "Game"}, {Index: 2, Title: "Mic"}}},
				{ID: "cam", Path: `C:\clips\cam.mp4`, Kind: "video", Duration: 10000, Width: 640, Height: 480, FPS: 30},
				{ID: "logo", Path: `C:\img\logo.png`, Kind: "image", Width: 512, Height: 512},
				{ID: "song", Path: `C:\music\song.mp3`, Kind: "audio", Duration: 180000, HasAudio: true, Tracks: []AudioTrack{{Index: 0}}},
			},
			Tracks: []TrackJSON{
				{ID: "v1", Kind: "video", Clips: []ClipJSON{
					{ID: "a", Media: "game", Start: 0, In: 2000, Out: 6000, Speed: 1, Volume: 1,
						Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}},
				}},
				{ID: "v2", Kind: "video", Clips: []ClipJSON{
					{ID: "b", Media: "cam", Start: 1000, In: 0, Out: 2000, Speed: 1, Volume: 1,
						Transform: TransformJSON{X: 0.85, Y: 0.2, Scale: 0.3, Opacity: 0.5},
						Chroma:    &ChromaJSON{Color: "#00ff00", Similarity: 0.2, Blend: 0.1}},
				}},
				{ID: "v3", Kind: "video", Clips: []ClipJSON{
					{ID: "c", Media: "logo", Start: 500, In: 0, Out: 3000, Speed: 1, Volume: 1,
						Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 0.4, Opacity: 1}},
				}},
				{ID: "a1", Kind: "audio", Clips: []ClipJSON{
					{ID: "d", Media: "song", Start: 0, In: 10000, Out: 14000, Speed: 1, Volume: 0.8, FadeIn: 500, FadeOut: 1000},
				}},
			},
		},
		Preset:  "original",
		OutPath: `C:\out\edit.mp4`,
	}
}

func TestBuildProjectArgs(t *testing.T) {
	req := testProject()
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if d := ProjectDurationMs(req.Project); d != 4000 {
		t.Fatalf("duration %d", d)
	}
	args, _ := BuildProjectArgs(req, "h264_nvenc", "", t.TempDir())
	joined := strings.Join(args, " ")
	graph := args[indexOf(args, "-filter_complex")+1]

	// One input per clip, in track order; the image loops for its length.
	wantInputs := []string{
		`-ss 2.000 -t 4.000 -i C:\clips\game.mp4`,
		`-ss 0.000 -t 2.000 -i C:\clips\cam.mp4`,
		`-loop 1 -framerate 60 -t 3.000 -i C:\img\logo.png`,
		`-ss 10.000 -t 4.000 -i C:\music\song.mp3`,
	}
	last := -1
	for _, w := range wantInputs {
		i := strings.Index(joined, w)
		if i < 0 || i < last {
			t.Fatalf("input %q missing or out of order in\n%s", w, joined)
		}
		last = i
	}
	for _, w := range []string{
		"color=c=black:s=1280x720:r=60:d=4.000,format=yuv420p[base]",
		"anullsrc=r=48000:cl=stereo:d=4.000," + silentFormat + "[abase]",
		// Bottom clip fits the canvas: 2560x1440 -> 1280x720 at 0,0.
		"[0:v]setpts=(PTS-STARTPTS)/1,fps=60,scale=1280:720,format=yuva420p,setpts=PTS+0.000/TB[v0]",
		"[base][v0]overlay=x=0:y=0:eof_action=pass:enable='between(t,0.000,4.000)'[o0]",
		// PiP: green screen, 30% of fit (640x480 fits 960x720 -> 288x216), half opacity, centre (1088,144).
		"[1:v]setpts=(PTS-STARTPTS)/1,fps=60,chromakey=color=0x00FF00:similarity=0.2:blend=0.1,scale=288:216,format=rgba,colorchannelmixer=aa=0.5,setpts=PTS+1.000/TB[v1]",
		"[o0][v1]overlay=x=944:y=36:eof_action=pass:enable='between(t,1.000,3.000)'[o1]",
		// Logo on top: 512x512 fits 720x720, 40% -> 288x288, centred.
		"[2:v]setpts=PTS-STARTPTS,fps=60,scale=288:288,format=yuva420p,setpts=PTS+0.500/TB[v2]",
		"[o1][v2]overlay=x=496:y=216:eof_action=pass:enable='between(t,0.500,3.500)'[o2]",
		// The game clip has no levels: its mix track at 100%.
		"[0:a:0]asetpts=PTS-STARTPTS," + silentFormat + "[ca0]",
		"[ca0]anull[c0]",
		// Music: volume and fades, no delay at 0.
		"[3:a:0]asetpts=PTS-STARTPTS," + silentFormat + "[ca3]",
		"[ca3]volume=0.8,afade=t=in:st=0:d=0.5,afade=t=out:st=3:d=1[c1]",
		"[abase][c0][c1]amix=inputs=3:duration=first:normalize=0[aout]",
	} {
		if !strings.Contains(graph, w) {
			t.Errorf("graph lacks %q\n%s", w, strings.ReplaceAll(graph, ";", ";\n"))
		}
	}
	// The cam has no audio: no audio chain from input 1.
	if strings.Contains(graph, "[1:a") {
		t.Errorf("silent cam got an audio chain")
	}
	if !strings.Contains(joined, "-map [o2] -map [aout]") || !strings.HasSuffix(joined, `C:\out\edit.mp4`) {
		t.Errorf("maps/output: %s", joined)
	}
}

func TestBuildProjectArgsTracksSpeedAndPost(t *testing.T) {
	req := testProject()
	p := &req.Project
	p.Tracks[1].Hidden = true // PiP track hidden: no video from it
	p.Tracks[3].Muted = true  // music muted
	// Game clip at 2x with only the mic, quieter; starts at 1s.
	p.Tracks[0].Clips[0].Speed = 2
	p.Tracks[0].Clips[0].Start = 1000
	p.Tracks[0].Clips[0].Levels = []TrackLevel{{Source: 0, Index: 1, Volume: 100, Muted: true}, {Source: 0, Index: 2, Volume: 50}}
	p.Crop = &capture.Rect{X: 0, Y: 0, W: 640, H: 360}
	req.Preset = "custom"
	req.Height = 240
	req.Kbps = 4000
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
	if d := ProjectDurationMs(*p); d != 4000 { // the muted music still sets the length, as project.ts does
		t.Fatalf("duration %d", d)
	}
	args, _ := BuildProjectArgs(req, "h264_nvenc", "drawtext=x", t.TempDir())
	graph := args[indexOf(args, "-filter_complex")+1]
	for _, w := range []string{
		"[0:v]setpts=(PTS-STARTPTS)/2,",
		"setpts=PTS+1.000/TB[v0]",
		"enable='between(t,1.000,3.000)'[o0]",
		"[0:a:2]asetpts=PTS-STARTPTS," + silentFormat + ",volume=0.50[ca0]",
		"[ca0]atempo=2,adelay=delays=1000:all=1[c0]",
		// The hidden PiP adds no input, so the logo is input 1, layer 1.
		"[1:v]setpts=PTS-STARTPTS,fps=60,scale=288:288,format=yuva420p,setpts=PTS+0.500/TB[v1]",
		"[o1]crop=640:360:0:0,drawtext=x,scale=-2:240[vout]",
		"[abase][c0]amix=inputs=2:duration=first:normalize=0[aout]",
	} {
		if !strings.Contains(graph, w) {
			t.Errorf("graph lacks %q\n%s", w, strings.ReplaceAll(graph, ";", ";\n"))
		}
	}
	for _, bad := range []string{"[2:", "[1:a", "[0:a:1]", "chromakey"} {
		if strings.Contains(graph, bad) {
			t.Errorf("graph has %q (hidden/muted/muted level)", bad)
		}
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, `cam.mp4`) || strings.Contains(joined, `song.mp3`) {
		t.Errorf("hidden/muted clips still added as inputs: %s", joined)
	}
	if !strings.Contains(joined, "-b:v 4000k") {
		t.Errorf("custom bitrate missing: %s", joined)
	}
}

func TestProjectExportValidate(t *testing.T) {
	ok := testProject()
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid project: %v", err)
	}
	cases := map[string]func(r *ProjectExport){
		"tiny canvas":       func(r *ProjectExport) { r.Project.Canvas.Width = 1 },
		"empty timeline":    func(r *ProjectExport) { r.Project.Tracks = nil },
		"missing media":     func(r *ProjectExport) { r.Project.Tracks[0].Clips[0].Media = "nope" },
		"bad range":         func(r *ProjectExport) { r.Project.Tracks[0].Clips[0].Out = 2000 },
		"audio on video":    func(r *ProjectExport) { r.Project.Tracks[0].Clips[0].Media = "song" },
		"silent on audio":   func(r *ProjectExport) { r.Project.Tracks[3].Clips[0].Media = "cam" },
		"crop outside":      func(r *ProjectExport) { r.Project.Crop = &capture.Rect{X: 1000, Y: 0, W: 400, H: 100} },
		"unknown preset":    func(r *ProjectExport) { r.Preset = "4k" },
		"odd custom height": func(r *ProjectExport) { r.Preset = "custom"; r.Height = 721 },
		"no output":         func(r *ProjectExport) { r.OutPath = "" },
		"overwrite source":  func(r *ProjectExport) { r.OutPath = `c:\CLIPS\game.mp4` },
		"image no size":     func(r *ProjectExport) { r.Project.Media[2].Width = 0 },
	}
	for name, mut := range cases {
		r := testProject()
		mut(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestClipSizeAndLength(t *testing.T) {
	if w, h := ClipSize(1920, 1080, 1280, 720, 1); w != 1280 || h != 720 {
		t.Errorf("fit 16:9: %dx%d", w, h)
	}
	if w, h := ClipSize(1080, 1920, 1280, 720, 1); w != 404 || h != 720 { // portrait: fit by height
		t.Errorf("fit portrait: %dx%d", w, h)
	}
	if w, h := ClipSize(1, 1, 1280, 720, 0.001); w != 2 || h != 2 {
		t.Errorf("min size: %dx%d", w, h)
	}
	if n := ClipLengthMs(ClipJSON{In: 1000, Out: 3000, Speed: 0.5}); n != 4000 {
		t.Errorf("0.5x length %d", n)
	}
	if n := ClipLengthMs(ClipJSON{In: 0, Out: 3000}); n != 3000 { // speed 0 = 1x
		t.Errorf("zero speed length %d", n)
	}
	if tr := clipTransform(ClipJSON{}); tr != (TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}) {
		t.Errorf("zero transform %+v", tr)
	}
}

func TestProjectTextOverlays(t *testing.T) {
	p := ProjectJSON{Texts: []TextJSON{{ID: "t", Text: "GG", X: 0.5, Y: 0.9, Size: "l", Start: 1000, End: 2500}}}
	got := p.textOverlays()
	if len(got) != 1 || got[0] != (TextOverlay{Text: "GG", X: 0.5, Y: 0.9, Size: "l", StartMs: 1000, EndMs: 2500}) {
		t.Fatalf("%+v", got)
	}
	if w, h := ProjectFrameSize(ProjectJSON{Canvas: CanvasJSON{Width: 1281, Height: 721}}); w != 1280 || h != 720 {
		t.Fatalf("frame %dx%d", w, h)
	}
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}
