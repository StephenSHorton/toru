package fx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// keyedSet is a def's animatable params keyed from min to max (colours
// black to white) over 1 s, or nil when it has none.
func keyedSet(d Def) map[string][]Key {
	keys := map[string][]Key{}
	for _, p := range d.Params {
		if p.Animate == "" {
			continue
		}
		switch p.Type {
		case "number":
			keys[p.ID] = []Key{{T: 0, V: *p.Min}, {T: 1000, V: *p.Max}}
		case "color":
			keys[p.ID] = []Key{{T: 0, V: "#000000"}, {T: 1000, V: "#ffffff"}}
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return keys
}

// TestKeyedBuildersAreLGPL is lgpl_test's check for keyframed effects:
// every def with animatable params, keyed min to max, builds and only emits
// allowed filters (plus sendcmd).
func TestKeyedBuildersAreLGPL(t *testing.T) {
	allow := allowlist()
	work := t.TempDir()
	for _, d := range Defs() {
		keys := keyedSet(d)
		if keys == nil {
			continue
		}
		stmts, err := StackGraph([]Effect{{Type: d.Type, Enabled: true, Amount: 1, Keys: keys}}, Ctx{W: 640, H: 360, FPS: 30, Frames: 31}, work, "k", "[in]", "[out]")
		if err != nil {
			t.Errorf("%s keyed: %v", d.Type, err)
			continue
		}
		g := strings.Join(stmts, ";")
		for _, f := range filtersIn(g) {
			if !allow[f] {
				t.Errorf("%s keyed emits %q, which the pinned LGPL ffmpeg doesn't have", d.Type, f)
			}
			if !slices.Contains(d.Export.Filters, f) && !slices.Contains(scaffold, f) && f != "sendcmd" {
				t.Errorf("%s keyed emits %q, not in its export.filters", d.Type, f)
			}
		}
		if !strings.Contains(g, "sendcmd=") && !strings.Contains(g, "enable=") {
			t.Errorf("%s keyed: nothing animates in %s", d.Type, g)
		}
	}
}

// TestKeyedMatchesStatic renders keyframed stacks through ffmpeg and checks
// that frame n is exactly the static stack at frame n's values (which the
// parity check already holds to the preview). Skipped without ffmpeg.
func TestKeyedMatchesStatic(t *testing.T) {
	ff := os.Getenv("TORU_FFMPEG")
	if ff == "" {
		var err error
		if ff, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg")
		}
	}
	work := t.TempDir()
	src := work + "/src.png"
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x180:d=1", "-frames:v", "1", src).CombinedOutput(); err != nil {
		t.Fatalf("source: %v %s", err, out)
	}
	const fps, frames = 30, 31
	ramp := func(a, b any) []Key { return []Key{{T: 100, V: a, Ease: "inOut"}, {T: 800, V: b}} }
	cases := map[string][]Effect{
		"adjust exposure + sharpen": {{Type: "adjust", Params: map[string]any{"contrast": 1.2}, Keys: map[string][]Key{"exposure": ramp(-1.0, 1.5), "sharpen": ramp(0.0, 1.0)}}},
		"look amount":               {{Type: "look", Params: map[string]any{"look": "sepia"}, Keys: map[string][]Key{"amount": ramp(0.0, 1.0)}}},
		"gaussian blur sigma":       {{Type: "gaussianBlur", Keys: map[string][]Key{"sigma": ramp(0.0, 12.0)}}},
		"green screen similarity":   {{Type: "chromaKey", Keys: map[string][]Key{"similarity": ramp(0.05, 0.6), "color": ramp("#00ff00", "#3cc850")}}},
		"rgb split":                 {{Type: "rgbSplit", Keys: map[string][]Key{"amount": ramp(0.0, 40.0), "angle": ramp(0.0, 200.0)}}},
	}
	render := func(fx []Effect, ctx Ctx, n int) []byte {
		t.Helper()
		stmts, err := StackGraph(fx, ctx, work, "k", "[0:v]", "[fx]")
		if err != nil {
			t.Fatal(err)
		}
		g := "[0:v]null[fx]"
		if stmts != nil {
			g = strings.Join(stmts, ";")
		}
		out, err := exec.Command(ff, "-v", "error", "-loop", "1", "-framerate", fmt.Sprint(fps), "-t", "1.1", "-i", src,
			"-filter_complex", g+";[fx]format=rgba,select=eq(n\\,"+fmt.Sprint(n)+")[o]", "-map", "[o]", "-frames:v", "1", "-f", "rawvideo", "-").CombinedOutput()
		if err != nil || len(out) != 320*180*4 {
			t.Fatalf("ffmpeg: %v (%d bytes)\n%s\n%s", err, len(out), g, out)
		}
		return out
	}
	for name, fx := range cases {
		for k := range fx {
			fx[k].Enabled, fx[k].Amount = true, 1
		}
		keyed := Ctx{W: 320, H: 180, FPS: fps, Frames: frames}
		for _, n := range []int{0, 3, 9, 14, 20, 24, 30} {
			got := render(fx, keyed, n)
			static := make([]Effect, len(fx))
			for k, e := range fx {
				d, _ := Lookup(e.Type)
				at, _ := keyedParams(d, e)
				static[k] = Effect{Type: e.Type, Enabled: true, Amount: 1, Params: at(n, fps)}
			}
			if want := render(static, Ctx{W: 320, H: 180}, n); !bytes.Equal(got, want) {
				diff := 0
				for i := range got {
					if got[i] != want[i] {
						diff++
					}
				}
				t.Errorf("%s frame %d: keyed export differs from the static one at its values in %d bytes", name, n, diff)
			}
		}
	}
}
