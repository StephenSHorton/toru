package engine

import (
	"math"
	"math/rand/v2"
)

// Glide: a smooth cursor move to a new click point. The path is a cubic Bezier
// whose two inner control points sit at a third and two thirds of the way and
// are pushed sideways by a random amount, so the line bows a little like a
// hand-moved mouse; position along it follows a smoothstep ease (slow, fast,
// slow). The runner walks the waypoints on the clock, waiting between them,
// so a glide can be interrupted like any other wait, and it ends with an exact
// Teleport because absolute-move normalization can land a pixel off.

const (
	// glideMinBaseNs: below this base interval there is no time to glide;
	// points are reached by teleport.
	glideMinBaseNs = 20 * msNs
	// glideMaxNs caps one glide.
	glideMaxNs = 200 * msNs
	// glideShare is the part of the drawn interval a glide may use.
	glideShare = 0.4
	// glideStepNs is roughly the time between waypoints.
	glideStepNs = 8 * msNs
	// glideMaxSteps bounds the waypoint count (and the path array).
	glideMaxSteps = 25
	// glideMinDist: shorter moves just teleport.
	glideMinDist = 3.0
	// glideBowMax caps the sideways push of a control point in px.
	glideBowMax = 60.0
)

// glidePath is one planned glide: waypoints and when each is due relative to
// the start.
type glidePath struct {
	n  int
	x  [glideMaxSteps]int32
	y  [glideMaxSteps]int32
	at [glideMaxSteps]int64
}

// glideDuration is how long a glide gets out of a drawn interval.
func glideDuration(drawn int64) int64 {
	return min(int64(glideShare*float64(drawn)), glideMaxNs)
}

// planGlide fills g with a path from (x0, y0) to (x1, y1) over dur ns. It
// returns false when the move is too short in space or time to bother, and
// the caller teleports.
func planGlide(g *glidePath, rnd *rand.Rand, x0, y0, x1, y1 int32, dur int64) bool {
	dx, dy := float64(x1-x0), float64(y1-y0)
	dist := math.Hypot(dx, dy)
	if dist < glideMinDist || dur < 2*msNs {
		return false
	}
	steps := int(min(max(dur/glideStepNs, 2), glideMaxSteps))
	// Unit normal to the line, for the sideways bow.
	nx, ny := -dy/dist, dx/dist
	bow := math.Min(0.15*dist, glideBowMax)
	b1 := (rnd.Float64()*2 - 1) * bow
	b2 := (rnd.Float64()*2 - 1) * bow
	p0x, p0y := float64(x0), float64(y0)
	c1x, c1y := p0x+dx/3+nx*b1, p0y+dy/3+ny*b1
	c2x, c2y := p0x+2*dx/3+nx*b2, p0y+2*dy/3+ny*b2
	p3x, p3y := float64(x1), float64(y1)
	for i := range steps {
		t := float64(i+1) / float64(steps)
		e := t * t * (3 - 2*t)
		u := 1 - e
		w0, w1, w2, w3 := u*u*u, 3*u*u*e, 3*u*e*e, e*e*e
		g.x[i] = int32(math.Round(w0*p0x + w1*c1x + w2*c2x + w3*p3x))
		g.y[i] = int32(math.Round(w0*p0y + w1*c1y + w2*c2y + w3*p3y))
		g.at[i] = dur * int64(i+1) / int64(steps)
	}
	g.n = steps
	return true
}
