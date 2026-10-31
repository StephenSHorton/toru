package vid

import (
	"fmt"
	"slices"
	"strings"
)

// transitions.go: transitions between adjacent clips on a video track.
//
// A transition belongs to the incoming clip (ClipJSON.Transition) and plays
// at its cut with the clip before it, centred on the cut: for a duration d it
// runs from cut-d/2 to cut+d/2, so the timeline's length doesn't change. The
// outgoing clip's last frame is held for the second half and the incoming
// clip's first frame for the first half (no source handles), so it works for
// every kind of clip (speed curves, reversed, freeze frames, images) and the
// preview matches it exactly. The export blends the two with ffmpeg's xfade
// (types are xfade's names); the preview (frontend/src/trim/compositor/
// transition.ts) ports xfade's formulas for those types. Audio: the outgoing
// clip fades out over the half before the cut, the incoming one fades in
// over the half after it, on top of the clips' own fades.
//
// A transition that doesn't fit is ignored, never an error, so edits that
// move clips apart can leave the field in place: it needs a clip right
// before it on the same track (ending where it starts), both visible video
// or image clips, and room: each half fits in its clip next to that clip's
// other transition. TransitionAt is the one rule; frontend/src/trim/
// transition.ts transitionAt keeps the same.

// TransitionJSON is a clip's incoming transition.
type TransitionJSON struct {
	Type     string `json:"type"`     // one of TransitionTypes
	Duration int    `json:"duration"` // ms, whole: half before the cut, half after
}

// TransitionTypes are the transitions offered, as xfade names.
var TransitionTypes = []string{
	"fade", "fadeblack", "fadewhite",
	"wipeleft", "wiperight", "wipeup", "wipedown",
	"slideleft", "slideright", "slideup", "slidedown",
	"zoomin", "hblur",
}

// Transition duration limits (ms).
const (
	MinTransitionMs     = 100
	MaxTransitionMs     = 5000
	DefaultTransitionMs = 500
)

// Transition is a valid transition on a track: clip Index (the incoming
// one) with the clip before it.
type Transition struct {
	Index    int
	Type     string
	Duration int // ms
	Cut      int // ms, timeline time of the cut
}

// Half is the time on each side of the cut (ms; the first half rounds down).
func (t Transition) Half() int { return t.Duration / 2 }

// Start is when the transition begins on the timeline (ms).
func (t Transition) Start() int { return t.Cut - t.Half() }

// End is when it ends (ms).
func (t Transition) End() int { return t.Cut + t.Duration - t.Half() }

// trackTransitions lists the valid transitions on a track, by incoming clip
// index (absent = none). shows says whether a clip draws video (a hidden
// track, an audio file or a fully transparent clip doesn't).
func trackTransitions(t TrackJSON, shows func(ClipJSON) bool) map[int]Transition {
	out := map[int]Transition{}
	if t.Kind != "video" || t.Hidden {
		return out
	}
	// First pass: each transition on its own (type, adjacency, visible).
	// Second: room, counted against the neighbouring candidates, so a clip
	// with transitions on both sides has room for both halves.
	cand := map[int]Transition{}
	for i := 1; i < len(t.Clips); i++ {
		c, prev := t.Clips[i], t.Clips[i-1]
		tr := c.Transition
		if tr == nil || !slices.Contains(TransitionTypes, tr.Type) {
			continue
		}
		d := min(max(tr.Duration, MinTransitionMs), MaxTransitionMs)
		cut := prev.Start + ClipLengthMs(prev)
		if absInt(cut-c.Start) > 1 || !shows(c) || !shows(prev) {
			continue
		}
		cand[i] = Transition{Index: i, Type: tr.Type, Duration: d, Cut: c.Start}
	}
	for i, tr := range cand {
		prev, c := t.Clips[i-1], t.Clips[i]
		// The outgoing clip holds its last frame for the second half; the
		// first half must fit in it after its own incoming transition's.
		prevIn := 0
		if p, ok := cand[i-1]; ok {
			prevIn = p.Duration - p.Half()
		}
		next := 0
		if n, ok := cand[i+1]; ok {
			next = n.Half()
		}
		if tr.Half()+prevIn > ClipLengthMs(prev) || tr.Duration-tr.Half()+next > ClipLengthMs(c) {
			continue
		}
		out[i] = tr
	}
	return out
}

// TransitionAt is clip i's valid incoming transition on track t, if any,
// for a project where every clip shows (tests, the agent command check).
func TransitionAt(t TrackJSON, i int) (Transition, bool) {
	tr, ok := trackTransitions(t, func(ClipJSON) bool { return true })[i]
	return tr, ok
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// transitionLayer turns a clip's chain (labelled in, frames at timeline
// time from start-padStart) into a canvas-size layer with alpha, so two
// layers can go through xfade: a transparent canvas the layer's length with
// the clip overlaid at x/y (overlay arguments, timeline t).
func transitionLayer(in, out string, w, h int, fps float64, start, length int, x, y string) string {
	return fmt.Sprintf("color=c=black@0:s=%dx%d:r=%s:d=%s,format=yuva420p,setpts=PTS+%s/TB[%s_cv];[%s_cv]%soverlay=x=%s:y=%s:format=auto:eof_action=pass,format=yuva420p%s",
		w, h, formatFPS(fps), msSeconds(length), msSeconds(start), strings.Trim(out, "[]"), strings.Trim(out, "[]"), in, x, y, out)
}

// xfadeFilter blends run (from runStart) into next with tr.
func xfadeFilter(run, next, out string, tr Transition, runStart int) string {
	return fmt.Sprintf("%s%sxfade=transition=%s:duration=%s:offset=%s%s",
		run, next, tr.Type, msSeconds(tr.Duration), msSeconds(tr.Start()-runStart), out)
}
