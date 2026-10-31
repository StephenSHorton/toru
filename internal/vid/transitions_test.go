package vid

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/fx/fxtest"
)

func tclip(id string, start, length int, tr *TransitionJSON) ClipJSON {
	return ClipJSON{ID: id, Media: "m", Start: start, In: 0, Out: length, Speed: 1, Volume: 1,
		Transform: TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}, Transition: tr}
}

func TestTrackTransitions(t *testing.T) {
	fade := func(d int) *TransitionJSON { return &TransitionJSON{Type: "fade", Duration: d} }
	all := func(ClipJSON) bool { return true }
	cases := []struct {
		name  string
		track TrackJSON
		want  map[int]int // index → duration
	}{
		{"adjacent", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 2000, fade(1000))}}, map[int]int{1: 1000}},
		{"first clip has none", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, fade(500))}}, map[int]int{}},
		{"gap", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2500, 2000, fade(500))}}, map[int]int{}},
		{"1 ms off is still a cut", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2001, 2000, fade(500))}}, map[int]int{1: 500}},
		{"unknown type", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 2000, &TransitionJSON{Type: "spin", Duration: 500})}}, map[int]int{}},
		{"clamped duration", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 9000, nil), tclip("b", 9000, 9000, fade(60000))}}, map[int]int{1: MaxTransitionMs}},
		{"too long for the clip", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 400, nil), tclip("b", 400, 2000, fade(1000))}}, map[int]int{}},
		{"both sides fit", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 1000, fade(1000)), tclip("c", 3000, 2000, fade(1000))}}, map[int]int{1: 1000, 2: 1000}},
		{"both sides don't", TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 800, fade(1000)), tclip("c", 2800, 2000, fade(1000))}}, map[int]int{}},
		{"audio track", TrackJSON{Kind: "audio", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 2000, fade(500))}}, map[int]int{}},
		{"hidden track", TrackJSON{Kind: "video", Hidden: true, Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 2000, fade(500))}}, map[int]int{}},
	}
	for _, c := range cases {
		got := trackTransitions(c.track, all)
		g := map[int]int{}
		for i, tr := range got {
			g[i] = tr.Duration
		}
		if fmt.Sprint(g) != fmt.Sprint(c.want) {
			t.Errorf("%s: %v, want %v", c.name, g, c.want)
		}
	}
	// A clip that doesn't show (shows false) breaks the pair.
	tr := TrackJSON{Kind: "video", Clips: []ClipJSON{tclip("a", 0, 2000, nil), tclip("b", 2000, 2000, fade(500))}}
	if got := trackTransitions(tr, func(c ClipJSON) bool { return c.ID != "a" }); len(got) != 0 {
		t.Errorf("hidden outgoing clip: %v", got)
	}
	x := Transition{Duration: 501, Cut: 2000}
	if x.Half() != 250 || x.Start() != 1750 || x.End() != 2251 {
		t.Errorf("odd duration: half %d start %d end %d", x.Half(), x.Start(), x.End())
	}
}

// TestTransitionGraph checks the export's shape: the clips in a run become
// canvas layers held at their edges (tpad), joined by xfade with the offset
// from the run's start, then overlaid once; audio fades at the cut.
func TestTransitionGraph(t *testing.T) {
	p := ProjectJSON{
		Version: 3, Canvas: CanvasJSON{Width: 320, Height: 180, FPS: 30},
		Media: []MediaJSON{{ID: "m", Path: "C:/a.mp4", Kind: "video", Duration: 10000, Width: 320, Height: 180, FPS: 30, HasAudio: true, Tracks: []AudioTrack{{Index: 0, IsMix: true}}}},
		Tracks: []TrackJSON{{ID: "v", Kind: "video", Clips: []ClipJSON{
			tclip("a", 0, 2000, nil),
			tclip("b", 2000, 2000, &TransitionJSON{Type: "wipeleft", Duration: 1000}),
			tclip("c", 4000, 2000, &TransitionJSON{Type: "fadeblack", Duration: 600}),
		}}},
	}
	args, err := BuildProjectArgs(ProjectExport{Project: p, Preset: "original", OutPath: "C:/o.mp4"}, "h264_nvenc", "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	g := strings.Join(args, " ")
	var missing []string
	for _, want := range []string{
		"tpad=start_mode=clone:start_duration=0.000:stop_mode=clone:stop_duration=0.500,setpts=PTS+0.000/TB[v0]",
		"tpad=start_mode=clone:start_duration=0.500:stop_mode=clone:stop_duration=0.300,setpts=PTS+1.500/TB[v1]",
		"tpad=start_mode=clone:start_duration=0.300:stop_mode=clone:stop_duration=0.000,setpts=PTS+3.700/TB[v2]",
		"color=c=black@0:s=320x180:r=30:d=2.800,format=yuva420p,setpts=PTS+1.500/TB[l1_cv]",
		"[l0][l1]xfade=transition=wipeleft:duration=1.000:offset=1.500[x1]",
		"[x1][l2]xfade=transition=fadeblack:duration=0.600:offset=3.700[x2]",
		"[base][x2]overlay=eof_action=pass:enable='between(t,0.000,6.000)'[o2]",
		"[ca0]afade=t=out:st=1.500:d=0.500[c0]",
		"[ca1]afade=t=in:st=0:d=0.500,afade=t=out:st=1.700:d=0.300,adelay",
		"[ca2]afade=t=in:st=0:d=0.300,adelay",
	} {
		if !strings.Contains(g, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		t.Errorf("graph lacks %q\n%s", missing, g)
	}
}

// TestTransitionsInFFmpeg exports red → blue → green through the pinned
// LGPL ffmpeg and reads pixels: before the window, mid-fade (xfade's
// progress = 1 - (t-start)/d, so 50/50 at the cut), a wipe's two halves, a
// dip's black middle, and after.
func TestTransitionsInFFmpeg(t *testing.T) {
	ff := fxtest.PinnedFFmpeg(t)
	t.Setenv("TORU_FFMPEG", ff)
	dir := t.TempDir()
	src := func(name, color string) string {
		p := filepath.Join(dir, name+".mkv")
		if b, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "color=c="+color+":s=320x180:r=30:d=4", "-c:v", "ffv1", p).CombinedOutput(); err != nil {
			t.Fatalf("source: %v\n%s", err, b)
		}
		return p
	}
	media := []MediaJSON{
		{ID: "r", Path: src("r", "red"), Kind: "video", Duration: 4000, Width: 320, Height: 180, FPS: 30},
		{ID: "b", Path: src("b", "blue"), Kind: "video", Duration: 4000, Width: 320, Height: 180, FPS: 30},
		{ID: "g", Path: src("g", "0x00FF00"), Kind: "video", Duration: 4000, Width: 320, Height: 180, FPS: 30},
	}
	c := func(id, m string, start int, tr *TransitionJSON) ClipJSON {
		x := tclip(id, start, 2000, tr)
		x.Media = m
		return x
	}
	p := ProjectJSON{Version: 3, Canvas: CanvasJSON{Width: 320, Height: 180, FPS: 30}, Media: media,
		Tracks: []TrackJSON{{ID: "v", Kind: "video", Clips: []ClipJSON{
			c("a", "r", 0, nil),
			c("b", "b", 2000, &TransitionJSON{Type: "fade", Duration: 1000}),
			c("c", "g", 4000, &TransitionJSON{Type: "wipeleft", Duration: 600}),
		}}}}
	out := filepath.Join(dir, "out.mkv")
	req := ProjectExport{Project: p, Preset: "original", OutPath: out}
	args, err := BuildProjectArgs(req, "ffv1", "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command(ff, append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("export: %v\n%s\n%q", err, b, args)
	}
	frame := func(at float64) []byte {
		b, err := exec.Command(ff, "-v", "error", "-ss", fmt.Sprintf("%.4f", at), "-i", out, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgb24", "-").Output()
		if err != nil || len(b) != 320*180*3 {
			t.Fatalf("frame at %.3f: %v (%d bytes)", at, err, len(b))
		}
		return b
	}
	px := func(b []byte, x, y int) [3]int {
		i := (y*320 + x) * 3
		return [3]int{int(b[i]), int(b[i+1]), int(b[i+2])}
	}
	near := func(what string, got, want [3]int) {
		t.Helper()
		for k := range 3 {
			if d := got[k] - want[k]; d < -14 || d > 14 {
				t.Errorf("%s: %v, want about %v", what, got, want)
				return
			}
		}
	}
	// Frame starts are k/30 s; seek to a frame's middle.
	at := func(frame int) float64 { return (float64(frame) + 0.5) / 30 }
	near("red before the fade", px(frame(at(30)), 160, 90), [3]int{255, 0, 0})
	// Fade window 1.5..2.5 s = frames 45..75; at frame 60 progress is 0.5.
	near("fade midway", px(frame(at(60)), 160, 90), [3]int{128, 0, 128})
	near("blue after the fade", px(frame(at(90)), 160, 90), [3]int{0, 0, 255})
	// Wipe window 3.7..4.3 s = frames 111..129; frame 120: progress 0.5, the
	// left half still blue (x <= w*p), the right half green.
	mid := frame(at(120))
	near("wipe left half", px(mid, 40, 90), [3]int{0, 0, 255})
	near("wipe right half", px(mid, 280, 90), [3]int{0, 255, 0})
	near("green after", px(frame(at(150)), 160, 90), [3]int{0, 255, 0})
}
