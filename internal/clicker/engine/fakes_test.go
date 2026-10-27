package engine

import (
	"errors"
	"testing"
)

const ms = msNs

// fakeClock is virtual time: WaitUntil jumps straight to the deadline, running
// any scripted events whose time falls inside the wait first. An event may
// move the clock (a stall), flip a pause flag or interrupt.
type fakeClock struct {
	now         int64
	interrupted bool
	events      []clockEvent
	waits       int
}

type clockEvent struct {
	at int64
	fn func(c *fakeClock)
}

// at schedules fn for virtual time t (events must be added in time order).
func (c *fakeClock) at(t int64, fn func(c *fakeClock)) {
	c.events = append(c.events, clockEvent{t, fn})
}

func (c *fakeClock) Now() int64 { return c.now }

func (c *fakeClock) WaitUntil(deadline int64) bool {
	c.waits++
	if c.waits > 50_000_000 {
		panic("fakeClock: run did not end")
	}
	for !c.interrupted && len(c.events) > 0 && c.events[0].at <= deadline {
		ev := c.events[0]
		c.events = c.events[1:]
		c.now = max(c.now, ev.at)
		ev.fn(c)
	}
	if c.interrupted {
		return false
	}
	c.now = max(c.now, deadline)
	return true
}

func (c *fakeClock) Interrupt()      { c.interrupted = true }
func (c *fakeClock) ClearInterrupt() { c.interrupted = false }

// rec is one recorded injector call.
type rec struct {
	t    int64
	op   Op    // Kind 0 for moves
	move uint8 // moveTeleport / moveGlide
	x, y int32
}

const (
	moveTeleport = 1
	moveGlide    = 2
)

// recInjector records everything with the clock time it happened at.
type recInjector struct {
	clk    *fakeClock
	log    []rec
	cx, cy int32
	sendNs int64 // virtual time each Send takes
	fail   bool
}

func (in *recInjector) Send(ops []Op) (int, error) {
	if in.fail {
		return 0, errors.New("boom")
	}
	for _, op := range ops {
		in.log = append(in.log, rec{t: in.clk.now, op: op})
	}
	in.clk.now += in.sendNs
	return len(ops), nil
}

func (in *recInjector) Teleport(x, y int32) error {
	in.cx, in.cy = x, y
	in.log = append(in.log, rec{t: in.clk.now, move: moveTeleport, x: x, y: y})
	return nil
}

func (in *recInjector) GlideStep(x, y int32) error {
	in.cx, in.cy = x, y
	in.log = append(in.log, rec{t: in.clk.now, move: moveGlide, x: x, y: y})
	return nil
}

func (in *recInjector) Cursor() (int32, int32, bool) { return in.cx, in.cy, true }

// ops returns the recorded input ops (moves left out).
func (in *recInjector) ops() []rec {
	var out []rec
	for _, r := range in.log {
		if r.move == 0 {
			out = append(out, r)
		}
	}
	return out
}

// times returns when each op of the kind went out.
func (in *recInjector) times(k OpKind) []int64 {
	var out []int64
	for _, r := range in.log {
		if r.move == 0 && r.op.Kind == k {
			out = append(out, r.t)
		}
	}
	return out
}

// guardFunc is a scripted Guard.
type guardFunc func(now, clicks int64) Verdict

func (g guardFunc) Check(now, clicks int64) Verdict { return g(now, clicks) }

// nopInjector discards input without allocating (benchmarks).
type nopInjector struct{ sent int }

func (n *nopInjector) Send(ops []Op) (int, error)   { n.sent += len(ops); return len(ops), nil }
func (n *nopInjector) Teleport(int32, int32) error  { return nil }
func (n *nopInjector) GlideStep(int32, int32) error { return nil }
func (n *nopInjector) Cursor() (int32, int32, bool) { return 0, 0, true }

// run is Run over fresh fakes.
func run(t *testing.T, cfg Config, setup func(c *fakeClock)) (Verdict, *recInjector, *fakeClock, []Progress) {
	t.Helper()
	c := &fakeClock{}
	in := &recInjector{clk: c}
	if setup != nil {
		setup(c)
	}
	var prog []Progress
	v := Run(cfg, in, c, nil, nil, func(p Progress) { prog = append(prog, p) })
	return v, in, c, prog
}

func kinds(rs []rec) []OpKind {
	out := make([]OpKind, len(rs))
	for i, r := range rs {
		out[i] = r.op.Kind
	}
	return out
}

func eqKinds(a, b []OpKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
