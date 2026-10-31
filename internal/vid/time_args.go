package vid

import (
	"fmt"
	"math"
	"strings"
)

// time_args.go is the export's time effects (docs/video-editor-v3.md, Time
// effects): how a clip's frames and sound are timed by its speed, speed
// curve or freeze, and how slow motion is blended. project_args.go calls
// these where it times a clip. Pure: no ffmpeg calls, no file access.

// holdInputArgs is a freeze frame's input: just past its frame, since only
// the first decoded frame is used.
func holdInputArgs(c ClipJSON, path string) []string {
	return []string{"-ss", msSeconds(c.In), "-t", "0.5", "-i", path}
}

// clipTiming is the filters that time a video clip's frames, from its
// input to just before the canvas-rate fps: a freeze frame's held still,
// a curve's setpts, or a constant speed; then frame blending (mix) where
// it's slow. Smooth slow motion is a graph of its own (clipSmoothGraph).
func clipTiming(c ClipJSON) []string {
	if c.Hold {
		return []string{
			"trim=end_frame=1",
			"setpts=PTS-STARTPTS",
			fmt.Sprintf("tpad=stop_mode=clone:stop_duration=%s", msSeconds(c.Out-c.In)),
		}
	}
	var parts []string
	cv := clipCurve(c)
	if cv != nil {
		parts = append(parts, curveSetpts(cv))
	} else {
		parts = append(parts, fmt.Sprintf("setpts=(PTS-STARTPTS)/%s", num(clipSpeed(c))))
	}
	if c.BlendFrames == "mix" {
		if slow := clipMinSpeed(c, cv); slow < 1 {
			// Two frames per output frame down to 0.5x, three below that.
			frames := 2
			if slow < 0.5 {
				frames = 3
			}
			parts = append(parts, fmt.Sprintf("tmix=frames=%d", frames))
		}
	}
	return parts
}

// clipMinSpeed is the slowest a clip plays.
func clipMinSpeed(c ClipJSON, cv *speedCurve) float64 {
	if cv == nil {
		return clipSpeed(c)
	}
	m := math.Inf(1)
	for _, p := range cv.Pts {
		m = math.Min(m, p.V)
	}
	return m
}

// wantsSmooth says whether a clip's slow motion is motion-interpolated.
func wantsSmooth(c ClipJSON) bool {
	return !c.Hold && c.BlendFrames == "smooth" && clipMinSpeed(c, clipCurve(c)) < 1
}

// timeRange is a stretch of a clip's timeline (s from its start).
type timeRange struct {
	T0, T1 float64
	Slow   bool
}

// minSmoothRange is the shortest slow stretch worth interpolating (s); a
// shorter one joins its neighbour.
const minSmoothRange = 0.1

// slowRanges cuts a clip's timeline into stretches slower than 1x and the
// rest, in order, covering 0..its length.
func slowRanges(c ClipJSON) []timeRange {
	length := float64(ClipLengthMs(c)) / 1000
	cv := clipCurve(c)
	if cv == nil {
		return []timeRange{{0, length, clipSpeed(c) < 1}}
	}
	// Source points where the speed crosses 1x, plus the curve's points.
	var cuts []float64
	p := cv.Pts
	for i := 0; i < len(p)-1; i++ {
		a, b := p[i], p[i+1]
		if (a.V-1)*(b.V-1) < 0 {
			cuts = append(cuts, a.S+(1-a.V)*(b.S-a.S)/(b.V-a.V))
		}
		cuts = append(cuts, b.S)
	}
	var out []timeRange
	prevS := 0.0
	for _, s := range cuts {
		if s-prevS < 1e-9 {
			continue
		}
		slow := cv.speedAt((prevS+s)/2) < 1-1e-9
		r := timeRange{cv.outAt(prevS), cv.outAt(s), slow}
		if n := len(out); n > 0 && out[n-1].Slow == slow {
			out[n-1].T1 = r.T1
		} else {
			out = append(out, r)
		}
		prevS = s
	}
	// Fold stretches too short to matter into the one before (or after).
	merged := out[:0]
	for _, r := range out {
		if n := len(merged); n > 0 && (r.T1-r.T0 < minSmoothRange || merged[n-1].Slow == r.Slow) {
			merged[n-1].T1 = r.T1
			continue
		}
		merged = append(merged, r)
	}
	if len(merged) > 1 && merged[0].T1-merged[0].T0 < minSmoothRange {
		merged[1].T0 = merged[0].T0
		merged = merged[1:]
	}
	if len(merged) > 0 {
		merged[len(merged)-1].T1 = length
	}
	return merged
}

// clipSmoothGraph is smooth slow motion: the timed clip (from in) split into
// its slow and normal stretches, the slow ones motion-interpolated to the
// canvas rate (minterpolate), joined back on out. prefix keeps the labels
// unique.
func clipSmoothGraph(in, out string, c ClipJSON, fps float64, prefix string) []string {
	timed := fmt.Sprintf("[%st]", prefix)
	stmts := []string{in + strings.Join(clipTiming(ClipJSON{
		Out: c.Out, In: c.In, Speed: c.Speed, SpeedCurve: c.SpeedCurve, Reversed: c.Reversed,
	}), ",") + timed}
	rs := slowRanges(c)
	interp := fmt.Sprintf("minterpolate=fps=%s:mi_mode=mci:mc_mode=aobmc:vsbmc=1", formatFPS(fps))
	if len(rs) == 1 {
		return append(stmts, timed+interp+out)
	}
	var split, joined strings.Builder
	for i := range rs {
		fmt.Fprintf(&split, "[%ss%d]", prefix, i)
		fmt.Fprintf(&joined, "[%sr%d]", prefix, i)
	}
	stmts = append(stmts, fmt.Sprintf("%ssplit=%d%s", timed, len(rs), split.String()))
	for i, r := range rs {
		chain := fmt.Sprintf("[%ss%d]trim=start=%s:end=%s,setpts=PTS-STARTPTS", prefix, i, fnum(r.T0), fnum(r.T1))
		if r.Slow {
			chain += "," + interp
		} else {
			chain += ",fps=" + formatFPS(fps)
		}
		stmts = append(stmts, chain+fmt.Sprintf("[%sr%d]", prefix, i))
	}
	return append(stmts, fmt.Sprintf("%sconcat=n=%d:v=1:a=0%s", joined.String(), len(rs), out))
}

// tempoSteps splits a tempo into atempo steps each within 0.5..2 (atempo's
// clean range).
func tempoSteps(t float64) []string {
	if t <= 0 || math.IsNaN(t) || math.Abs(t-1) < 1e-9 {
		return nil
	}
	var out []string
	for t > 2+1e-9 {
		out = append(out, "atempo=2")
		t /= 2
	}
	for t < 0.5-1e-9 {
		out = append(out, "atempo=0.5")
		t /= 0.5
	}
	if math.Abs(t-1) > 1e-9 {
		out = append(out, "atempo="+fnum(t))
	}
	return out
}

// rampAudioGraph times a ramped clip's sound (from in, starting at its
// source in) by its curve: cut into rampAudioChunks, each stretched to its
// exact output length (pitch kept) or silenced, joined on out. The preview
// mutes the same chunks (speed.ts clipAudible).
func rampAudioGraph(in, out string, cv *speedCurve, prefix string) []string {
	chunks := rampAudioChunks(cv)
	if len(chunks) == 0 {
		return []string{in + "anull" + out}
	}
	var split, joined strings.Builder
	for i := range chunks {
		fmt.Fprintf(&split, "[%sa%d]", prefix, i)
		fmt.Fprintf(&joined, "[%sb%d]", prefix, i)
	}
	stmts := []string{fmt.Sprintf("%sasplit=%d%s", in, len(chunks), split.String())}
	for i, ch := range chunks {
		parts := []string{fmt.Sprintf("atrim=start=%s:end=%s", fnum(ch.S0), fnum(ch.S1)), "asetpts=PTS-STARTPTS"}
		parts = append(parts, tempoSteps(ch.Tempo)...)
		if !ch.Audible {
			parts = append(parts, "volume=0")
		}
		// Each piece is exactly its output length, so rounding in atempo
		// can't add up along the clip.
		dT := (ch.S1 - ch.S0) / ch.Tempo
		parts = append(parts, fmt.Sprintf("apad=whole_dur=%s", fnum(dT)), fmt.Sprintf("atrim=end=%s", fnum(dT)))
		stmts = append(stmts, fmt.Sprintf("[%sa%d]%s[%sb%d]", prefix, i, strings.Join(parts, ","), prefix, i))
	}
	return append(stmts, fmt.Sprintf("%sconcat=n=%d:v=0:a=1%s", joined.String(), len(chunks), out))
}
