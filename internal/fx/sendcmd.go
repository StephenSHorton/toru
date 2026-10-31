package fx

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// sendcmd.go writes keyframed values as ffmpeg sendcmd files
// (docs/video-editor-v3.md, Keyframes in the export): one command per
// output frame where a value changes, sent to a named filter instance
// (name@instance) just before that frame reaches it.

// Command sets a filter instance's option from frame Frame on (frames of
// the clip's output, at the canvas rate).
type Command struct {
	Frame  int
	Target string // "name@instance"
	Option string
	Value  string
}

// FrameMs is the clip-local time of frame n in ms, the time keyframes are
// sampled at (the preview samples the playhead's).
func FrameMs(n int, fps float64) float64 { return float64(n) * 1000 / fps }

// Frames is how many output frames a clip of lengthMs has at fps.
func Frames(lengthMs, fps float64) int {
	if fps <= 0 || lengthMs <= 0 {
		return 1
	}
	return max(1, int(math.Ceil(lengthMs*fps/1000)))
}

// SendcmdFilter writes cmds to a file in work (named by its content) and
// returns the sendcmd filter that plays them, or "" for none. A command is
// stamped half a frame before its frame, so rounding never moves it to the
// next one.
func SendcmdFilter(cmds []Command, fps float64, work string) (string, error) {
	if len(cmds) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, c := range cmds {
		at := math.Max(0, (float64(c.Frame)-0.5)/fps)
		fmt.Fprintf(&b, "%.6f %s %s %s;\n", at, c.Target, c.Option, c.Value)
	}
	text := b.String()
	sum := sha1.Sum([]byte(text))
	path := filepath.Join(work, "cmds-"+hex.EncodeToString(sum[:8])+".txt")
	if _, err := os.Stat(path); err != nil {
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			return "", fmt.Errorf("effects: write keyframe commands: %w", err)
		}
	}
	return "sendcmd=f=" + filterValue(filepath.ToSlash(path)), nil
}

// NumberCommands is the commands that move target's option through
// value(n) for frames 0..frames-1: one at frame 0, then one per change.
func NumberCommands(target, option string, frames int, value func(n int) float64) []Command {
	var out []Command
	prev := ""
	for n := 0; n < frames; n++ {
		v := num(round4(value(n)))
		if v != prev {
			out = append(out, Command{Frame: n, Target: target, Option: option, Value: v})
			prev = v
		}
	}
	return out
}

// Num formats a filter argument (shortest form, no exponent).
func Num(f float64) string { return num(f) }
