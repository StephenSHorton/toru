package fx_test

import (
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/fx"
	"github.com/StephenSHorton/toru/internal/fx/fxtest"
)

// These run against the FFmpeg Toru ships (fxtest.PinnedFFmpeg): set
// TORU_FFMPEG_PINNED=1 to download it once, or TORU_FFMPEG to a binary.
// Skipped otherwise.

// TestPinnedFFmpegPath writes the pinned ffmpeg's path to
// $TORU_FFMPEG_PATH_OUT, for the frontend scripts (effects-parity.ts,
// migration-golden.ts), which render the export side with it.
func TestPinnedFFmpegPath(t *testing.T) {
	out := os.Getenv("TORU_FFMPEG_PATH_OUT")
	if out == "" {
		t.Skip("TORU_FFMPEG_PATH_OUT not set")
	}
	if err := os.WriteFile(out, []byte(fxtest.PinnedFFmpeg(t)), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestAllowlistIsThePinnedBuild checks lgpl_filters.txt is exactly the pinned
// build's `ffmpeg -filters`, so bumping the build without regenerating the
// list (or hand-adding a filter to it) fails.
func TestAllowlistIsThePinnedBuild(t *testing.T) {
	ff := fxtest.PinnedFFmpeg(t)
	out, err := exec.Command(ff, "-hide_banner", "-filters").Output()
	if err != nil {
		t.Fatal(err)
	}
	var have []string
	past := false
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if !past {
			past = len(f) == 1 && f[0] == "------"
			continue
		}
		if len(f) >= 2 {
			have = append(have, f[1])
		}
	}
	b, err := os.ReadFile("lgpl_filters.txt")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			listed = append(listed, l)
		}
	}
	slices.Sort(have)
	slices.Sort(listed)
	for _, n := range listed {
		if _, ok := slices.BinarySearch(have, n); !ok {
			t.Errorf("lgpl_filters.txt lists %q, which %s doesn't have", n, ff)
		}
	}
	for _, n := range have {
		if _, ok := slices.BinarySearch(listed, n); !ok {
			t.Errorf("%s has %q, which lgpl_filters.txt doesn't list (regenerate it?)", ff, n)
		}
	}
}

// TestBuildersRunInPinnedFFmpeg runs every registered effect's graph through
// the pinned build: at default, every number at min, every number at max (the
// last enum option), each at full and half intensity, then all of them
// stacked. That checks option names and syntax, which the allowlist test
// (names only) can't.
func TestBuildersRunInPinnedFFmpeg(t *testing.T) {
	ff := fxtest.PinnedFFmpeg(t)
	work := t.TempDir()
	run := func(name string, stack []fx.Effect) {
		t.Helper()
		stmts, err := fx.StackGraph(stack, fx.Ctx{W: 320, H: 180}, work, "t", "[0:v]", "[fx]")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if stmts == nil {
			return // changes nothing
		}
		graph := strings.Join(stmts, ";") + ";[fx]format=rgba[o]"
		cmd := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=10:d=0.3",
			"-filter_complex", graph, "-map", "[o]", "-f", "null", "-")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s: %v\n%s\ngraph: %s", name, err, out, graph)
		}
	}
	var all []fx.Effect
	for _, d := range fx.Defs() {
		for i, p := range extremes(d) {
			for _, amount := range []float64{1, 0.5} {
				run(fmt.Sprintf("%s set %d at %.1f", d.Type, i, amount), []fx.Effect{{Type: d.Type, Enabled: true, Params: p, Amount: amount}})
			}
		}
		all = append(all, fx.Effect{Type: d.Type, Enabled: true, Params: extremes(d)[2], Amount: 1})
	}
	run("every effect at max", all)
}

// extremes is a def's params at default, every number at min, and every
// number at max with enums at their last option.
func extremes(d fx.Def) []map[string]any {
	def := d.Resolve(nil)
	lo, hi := map[string]any{}, map[string]any{}
	for _, p := range d.Params {
		lo[p.ID], hi[p.ID] = def[p.ID], def[p.ID]
		switch p.Type {
		case "number":
			lo[p.ID], hi[p.ID] = *p.Min, *p.Max
		case "enum":
			hi[p.ID] = p.Options[len(p.Options)-1].ID
		}
	}
	return []map[string]any{def, lo, hi}
}
