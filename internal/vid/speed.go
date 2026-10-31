package vid

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// speed.go is the export's half of speed ramps (docs/video-editor-v3.md,
// Speed ramps): the same maths as frontend/src/trim/speed.ts, checked
// against the shared testdata/speed_golden.json.
//
// A curve's speed is piecewise LINEAR in source time s (seconds from the
// clip's in point). Between points (s0, v0) and (s1, v1), with
// k = (v1 - v0) / (s1 - s0), the output time spent is
//   T(s) = T0 + ln((v0 + k(s - s0)) / v0) / k      (or (s - s0) / v0 when k = 0)
// and the inverse is s(T) = s0 + v0 (e^{k(T - T0)} - 1) / k. The export
// writes T(s) into setpts.

const (
	minCurveSpeed  = 0.1
	maxCurveSpeed  = 10
	audioMinSpeed  = 0.25 // a ramp's audio follows the speed within 0.25..4x
	audioMaxSpeed  = 4    // and is muted outside it
	flatSlope      = 1e-9 // |k| below this counts as constant speed
	speedPointSnap = 1e-6 // points closer than this (s) are one point
)

type speedPt struct{ S, V float64 }

// speedCurve is a curve in seconds, fitted to its clip's source range.
type speedCurve struct {
	Pts   []speedPt
	Audio string // "follow" | "mute"
}

// normSpeedPts fits points to a source range of srcLen seconds: sorted,
// speeds clamped to 0.1..10, inside 0..srcLen, with points at both ends
// (speed.ts normCurve).
func normSpeedPts(in []speedPt, srcLen float64) []speedPt {
	srcLen = math.Max(0, srcLen)
	pts := make([]speedPt, 0, len(in)+2)
	for _, p := range in {
		if math.IsNaN(p.S) || math.IsInf(p.S, 0) {
			continue
		}
		v := p.V
		if math.IsNaN(v) || math.IsInf(v, 0) {
			v = 1
		}
		pts = append(pts, speedPt{clampF(p.S, 0, srcLen), clampF(v, minCurveSpeed, maxCurveSpeed)})
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i].S < pts[j].S })
	out := make([]speedPt, 0, len(pts)+2)
	for _, p := range pts {
		if len(out) == 0 || p.S-out[len(out)-1].S > speedPointSnap {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = append(out, speedPt{0, 1})
	}
	if out[0].S > 0 {
		out = append([]speedPt{{0, out[0].V}}, out...)
	}
	if last := out[len(out)-1]; last.S < srcLen {
		out = append(out, speedPt{srcLen, last.V})
	}
	if len(out) == 1 {
		out = append(out, speedPt{srcLen, out[0].V})
	}
	return out
}

// clipCurve is a clip's speed curve in seconds, or nil when it plays at a
// constant speed (or is a freeze frame).
func clipCurve(c ClipJSON) *speedCurve {
	if c.SpeedCurve == nil || c.Hold {
		return nil
	}
	pts := make([]speedPt, len(c.SpeedCurve.Points))
	for i, p := range c.SpeedCurve.Points {
		pts[i] = speedPt{float64(p.S) / 1000, p.V}
	}
	audio := "follow"
	if c.SpeedCurve.Audio == "mute" {
		audio = "mute"
	}
	return &speedCurve{Pts: normSpeedPts(pts, float64(c.Out-c.In)/1000), Audio: audio}
}

func segSlope(a, b speedPt) float64 { return (b.V - a.V) / (b.S - a.S) }

// segT is the output time spent going from a.S to s (a.S <= s <= b.S).
func segT(a, b speedPt, s float64) float64 {
	ds := s - a.S
	if ds <= 0 {
		return 0
	}
	k := segSlope(a, b)
	if math.Abs(k) < flatSlope {
		return ds / a.V
	}
	return math.Log((a.V+k*ds)/a.V) / k
}

// outAt is the output time at source time s.
func (c *speedCurve) outAt(s float64) float64 {
	p := c.Pts
	T := 0.0
	for i := 0; i < len(p)-1; i++ {
		if s <= p[i+1].S {
			return T + segT(p[i], p[i+1], math.Max(p[i].S, s))
		}
		T += segT(p[i], p[i+1], p[i+1].S)
	}
	return T
}

// outLength is how long the whole curve plays on the timeline (s).
func (c *speedCurve) outLength() float64 {
	if len(c.Pts) == 0 {
		return 0
	}
	return c.outAt(c.Pts[len(c.Pts)-1].S)
}

// srcAt is the source time shown at output time T, held at the ends.
func (c *speedCurve) srcAt(T float64) float64 {
	p := c.Pts
	if len(p) == 0 {
		return 0
	}
	if T <= 0 {
		return p[0].S
	}
	T0 := 0.0
	for i := 0; i < len(p)-1; i++ {
		a, b := p[i], p[i+1]
		l := segT(a, b, b.S)
		if T <= T0+l || i == len(p)-2 {
			dT := math.Min(T-T0, l)
			k := segSlope(a, b)
			var s float64
			if math.Abs(k) < flatSlope {
				s = a.S + a.V*dT
			} else {
				s = a.S + a.V*(math.Exp(k*dT)-1)/k
			}
			return math.Min(b.S, s)
		}
		T0 += l
	}
	return p[len(p)-1].S
}

// speedAt is the speed at source time s (held past the ends).
func (c *speedCurve) speedAt(s float64) float64 {
	p := c.Pts
	if len(p) == 0 {
		return 1
	}
	if s <= p[0].S {
		return p[0].V
	}
	for i := 0; i < len(p)-1; i++ {
		if s <= p[i+1].S {
			return p[i].V + (p[i+1].V-p[i].V)*(s-p[i].S)/(p[i+1].S-p[i].S)
		}
	}
	return p[len(p)-1].V
}

// audibleAt is speed.ts audibleAtSpeed: "mute" is silent wherever the
// speed isn't 1x; "follow" is silent outside 0.25..4x.
func audibleAtSpeed(audio string, v float64) bool {
	if audio == "mute" {
		return math.Abs(v-1) < 1e-6
	}
	return v >= audioMinSpeed-1e-9 && v <= audioMaxSpeed+1e-9
}

// curveSetpts is the setpts expression that times a clip's frames by its
// curve: T(S) over the segments as nested if()s, S being the frame's source
// time from the clip's start. Single-quoted for a filtergraph.
func curveSetpts(c *speedCurve) string {
	const S = "(PTS-STARTPTS)*TB"
	p := c.Pts
	segs := make([]string, 0, len(p)-1)
	T0 := 0.0
	for i := 0; i < len(p)-1; i++ {
		a, b := p[i], p[i+1]
		k := segSlope(a, b)
		var e string
		if math.Abs(k) < flatSlope {
			e = fmt.Sprintf("%s+(%s-%s)/%s", fnum(T0), S, fnum(a.S), fnum(a.V))
		} else {
			e = fmt.Sprintf("%s+log((%s+%s*(%s-%s))/%s)/%s", fnum(T0), fnum(a.V), fnum(k), S, fnum(a.S), fnum(a.V), fnum(k))
		}
		segs = append(segs, e)
		T0 += segT(a, b, b.S)
	}
	// Past the last point (a frame at exactly the end): carry on at the end
	// speed so timestamps keep rising.
	last := p[len(p)-1]
	expr := fmt.Sprintf("%s+(%s-%s)/%s", fnum(T0), S, fnum(last.S), fnum(last.V))
	for i := len(segs) - 1; i >= 0; i-- {
		expr = fmt.Sprintf("if(lt(%s,%s),%s,%s)", S, fnum(p[i+1].S), segs[i], expr)
	}
	return "setpts='(" + expr + ")/TB'"
}

// fnum formats a number for a filter expression: enough digits that the
// export lands on the same frames as the preview, no exponent.
func fnum(f float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.9f", f), "0"), ".")
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

// audioChunk is one piece of a ramped clip's audio: source s0..s1 (s from
// the clip's in), stretched by tempo to fill T(s1)-T(s0) exactly, or
// silent.
type audioChunk struct {
	S0, S1  float64
	Tempo   float64
	Audible bool
}

// rampChunk is how long (source s) each piece of a ramped clip's audio is.
// atempo works on windows tens of ms long, so shorter pieces click at the
// joins (spec, known traps).
const rampChunk = 0.25

// rampAudioChunks cuts a curve's source range into pieces for the audio.
// Each piece's tempo is its source length over its output length, so the
// pieces add up to the clip's timeline length (a mean speed wouldn't).
func rampAudioChunks(c *speedCurve) []audioChunk {
	end := c.Pts[len(c.Pts)-1].S
	n := max(1, int(math.Ceil(end/rampChunk-1e-9)))
	out := make([]audioChunk, 0, n)
	for i := 0; i < n; i++ {
		s0 := float64(i) * end / float64(n)
		s1 := float64(i+1) * end / float64(n)
		dT := c.outAt(s1) - c.outAt(s0)
		if dT <= 0 {
			continue
		}
		tempo := (s1 - s0) / dT
		mid := c.speedAt((s0 + s1) / 2)
		out = append(out, audioChunk{s0, s1, tempo, audibleAtSpeed(c.Audio, mid) && audibleAtSpeed(c.Audio, tempo)})
	}
	return out
}
