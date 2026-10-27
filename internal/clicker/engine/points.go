package engine

import "math/rand/v2"

// pointSeq walks Config.Points round robin: Clicks completed cycles at a
// point, then the next one. Only completed cycles move it on; a cycle cut
// short by a pause or a stop does not.
type pointSeq struct {
	list      []Point
	once      bool // StopAfterPass
	idx       int
	tick      int // completed cycles at idx
	rnd       *rand.Rand
	placed    bool // the cursor has been put on a point this run
	placedIdx int
}

func newPointSeq(list []Point, once bool, rnd *rand.Rand) pointSeq {
	return pointSeq{list: list, once: once, rnd: rnd}
}

// active reports whether the run uses points.
func (p *pointSeq) active() bool { return len(p.list) > 0 }

// progress is the active point and its tick for Progress (-1, 0 without points).
func (p *pointSeq) progress() (idx, tick int) {
	if !p.active() {
		return -1, 0
	}
	return p.idx, p.tick
}

// next decides where the cursor goes before the coming cycle. move is false
// when it already sits on the current point and the point has no scatter, so
// a user nudging the cursor in place is left alone. newPoint is true when the
// target is a different point than the one the cursor was put on (the only
// case worth a glide).
func (p *pointSeq) next() (x, y int32, move, newPoint bool) {
	pt := p.list[p.idx]
	newPoint = !p.placed || p.placedIdx != p.idx
	if !newPoint && pt.Radius <= 0 {
		return pt.X, pt.Y, false, false
	}
	dx, dy := scatter(p.rnd, pt.Radius)
	return pt.X + dx, pt.Y + dy, true, newPoint && p.placed
}

// placedAt records that the cursor reached the current point.
func (p *pointSeq) placedAt() {
	p.placed, p.placedIdx = true, p.idx
}

// advance counts a completed cycle. done is true when StopAfterPass is set and
// the last point just finished; the index then stays on the last point.
func (p *pointSeq) advance() (done bool) {
	if !p.active() {
		return false
	}
	p.tick++
	if p.tick < max(p.list[p.idx].Clicks, 1) {
		return false
	}
	if p.once && p.idx == len(p.list)-1 {
		return true
	}
	p.idx = (p.idx + 1) % len(p.list)
	p.tick = 0
	return false
}

// scatter picks an offset uniformly among the whole pixels inside a disk of
// the radius (rejection sampling on the square, about 1.3 tries per draw), so
// no sample ever lands outside it.
func scatter(rnd *rand.Rand, radius int) (dx, dy int32) {
	if radius <= 0 {
		return 0, 0
	}
	r := int64(radius)
	for {
		x := rnd.Int64N(2*r+1) - r
		y := rnd.Int64N(2*r+1) - r
		if x*x+y*y <= r*r {
			return int32(x), int32(y)
		}
	}
}
