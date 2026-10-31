package vid

import (
	"math"
	"os/exec"
	"strings"
	"testing"
)

func rampClip(blend string, pts ...SpeedPointJSON) ClipJSON {
	end := pts[len(pts)-1].S
	return ClipJSON{In: 0, Out: end, Speed: 1, BlendFrames: blend, SpeedCurve: &SpeedCurveJSON{Points: pts, Audio: "follow"}}
}

func TestClipTimingShapes(t *testing.T) {
	hold := clipTiming(ClipJSON{In: 4000, Out: 6500, Speed: 1, Hold: true})
	if strings.Join(hold, ",") != "trim=end_frame=1,setpts=PTS-STARTPTS,tpad=stop_mode=clone:stop_duration=2.500" {
		t.Fatalf("hold %v", hold)
	}
	if got := clipTiming(ClipJSON{In: 0, Out: 1000, Speed: 2}); len(got) != 1 || got[0] != "setpts=(PTS-STARTPTS)/2" {
		t.Fatalf("constant %v", got)
	}
	mix := clipTiming(rampClip("mix", SpeedPointJSON{0, 1}, SpeedPointJSON{1000, 0.2}, SpeedPointJSON{2000, 1}))
	if len(mix) != 2 || mix[1] != "tmix=frames=3" {
		t.Fatalf("mix %v", mix)
	}
	if fast := clipTiming(ClipJSON{In: 0, Out: 1000, Speed: 2, BlendFrames: "mix"}); len(fast) != 1 {
		t.Fatalf("mix on a fast clip blends: %v", fast)
	}
}

func TestSlowRangesCoverTheClip(t *testing.T) {
	c := rampClip("smooth", SpeedPointJSON{0, 1}, SpeedPointJSON{1000, 3}, SpeedPointJSON{2000, 0.3}, SpeedPointJSON{3000, 0.3}, SpeedPointJSON{4000, 3}, SpeedPointJSON{5000, 1})
	rs := slowRanges(c)
	length := float64(ClipLengthMs(c)) / 1000
	if rs[0].T0 != 0 || math.Abs(rs[len(rs)-1].T1-length) > 1e-9 {
		t.Fatalf("ranges %v don't cover 0..%v", rs, length)
	}
	slow := 0
	for i, r := range rs {
		if i > 0 && math.Abs(r.T0-rs[i-1].T1) > 1e-9 {
			t.Fatalf("gap in %v", rs)
		}
		if r.Slow {
			slow++
		}
	}
	if slow != 1 || len(rs) != 3 {
		t.Fatalf("montage-like curve: %v", rs)
	}
	if c := (ClipJSON{In: 0, Out: 2000, Speed: 0.5, BlendFrames: "smooth"}); len(slowRanges(c)) != 1 || !slowRanges(c)[0].Slow {
		t.Fatalf("constant 0.5x: %v", slowRanges(c))
	}
}

func TestTempoSteps(t *testing.T) {
	for _, tc := range []struct {
		t    float64
		want string
	}{{1, ""}, {2, "atempo=2"}, {0.1, "atempo=0.5,atempo=0.5,atempo=0.5,atempo=0.8"}, {10, "atempo=2,atempo=2,atempo=2,atempo=1.25"}} {
		if got := strings.Join(tempoSteps(tc.t), ","); got != tc.want {
			t.Errorf("tempo %v: %q, want %q", tc.t, got, tc.want)
		}
	}
}

// TestTimeEffectsRunInFFmpeg runs ramps, a freeze, blending and the ramp
// audio through a real ffmpeg and checks each comes out the length the
// preview gives it. Skipped without ffmpeg.
func TestTimeEffectsRunInFFmpeg(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	dir := t.TempDir()
	run := func(name string, args ...string) {
		t.Helper()
		if out, err := exec.Command(ff, append([]string{"-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s\nargs: %q", name, err, out, args)
		}
	}
	src := dir + "/src.mp4"
	run("source", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=30:d=4", "-f", "lavfi", "-i", "sine=f=440:d=4", "-c:v", "mpeg4", "-q:v", "5", "-c:a", "aac", "-shortest", src)

	bullet := rampClip("", SpeedPointJSON{0, 1}, SpeedPointJSON{1500, 0.2}, SpeedPointJSON{2500, 0.2}, SpeedPointJSON{4000, 1})
	cases := []struct {
		name string
		c    ClipJSON
	}{
		{"bullet", bullet},
		{"flash in", rampClip("", SpeedPointJSON{0, 4}, SpeedPointJSON{1200, 1}, SpeedPointJSON{4000, 1})},
		{"blend", func() ClipJSON { c := bullet; c.BlendFrames = "mix"; return c }()},
		{"smooth", func() ClipJSON { c := bullet; c.BlendFrames = "smooth"; return c }()},
		{"hold", ClipJSON{In: 1000, Out: 3000, Speed: 1, Hold: true}},
	}
	for _, tc := range cases {
		want := float64(ClipLengthMs(tc.c)) / 1000
		out := dir + "/" + strings.ReplaceAll(tc.name, " ", "_") + ".mp4"
		var graph []string
		if wantsSmooth(tc.c) {
			graph = clipSmoothGraph("[0:v]", "[tv]", tc.c, 30, "x")
			graph = append(graph, "[tv]fps=30[v]")
		} else {
			graph = []string{"[0:v]" + strings.Join(clipTiming(tc.c), ",") + ",fps=30[v]"}
		}
		in := []string{"-i", src}
		if tc.c.Hold {
			in = holdInputArgs(tc.c, src, 30)
		}
		args := append(in, "-filter_complex", strings.Join(graph, ";"), "-map", "[v]", "-c:v", "mpeg4", "-q:v", "5", out)
		run(tc.name, args...)
		if got := probeDur(t, out); math.Abs(got-want) > 0.07 {
			t.Errorf("%s: video %.3f s, want %.3f", tc.name, got, want)
		}
	}

	// Ramp audio comes out exactly the clip's length.
	cv := clipCurve(bullet)
	aout := dir + "/ramp.wav"
	graph := strings.Join(rampAudioGraph("[0:a]", "[a]", cv, "r"), ";")
	run("ramp audio", "-i", src, "-filter_complex", graph, "-map", "[a]", aout)
	if got, want := probeDur(t, aout), cv.outLength(); math.Abs(got-want) > 0.03 {
		t.Errorf("ramp audio %.3f s, want %.3f", got, want)
	}
}
