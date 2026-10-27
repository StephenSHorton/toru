package engine

import "math/rand/v2"

// guardSliceNs is the longest the runner waits without asking the guard.
const guardSliceNs = 10 * msNs

// Run clicks until a limit is reached, the guard says Stop, or the caller
// interrupts the clock while paused() reports false. When the clock is
// interrupted and paused() is true the run releases anything held and waits
// (re-checking the guard) until paused() turns false, then resumes without a
// burst of catch-up clicks. It returns the final verdict (Kind == Stop).
// onProgress may be nil.
//
// Controller protocol (the Clock interface is the only wake-up channel):
//   - Pause: make paused() report true, then clk.Interrupt(). The run releases
//     held input and itself calls ClearInterrupt before reading paused(), so
//     it then idles in 10 ms slices polling paused() and the guard.
//   - Resume: make paused() report false. Do NOT interrupt; the run notices
//     within one slice.
//   - Stop: make paused() report false, then clk.Interrupt(). This works while
//     running and while paused. Because the run clears the interrupt before it
//     reads paused(), a stop can never be swallowed; the only misread is a
//     pause undone within microseconds being taken as a stop, which is safe.
//
// A guard Pause verdict releases held input the same way and resumes when the
// guard returns Continue (and the user has not paused). Guard Stop verdicts
// still end a paused run. The time limit is wall clock: paused time counts.
//
// Run is meant for a goroutine locked to its OS thread; it allocates nothing
// per click.
func Run(cfg Config, inj Injector, clk Clock, g Guard, paused func() bool, onProgress func(Progress)) Verdict {
	if paused == nil {
		paused = func() bool { return false }
	}
	r := &runner{cfg: cfg, inj: inj, clk: clk, g: g, paused: paused, onProgress: onProgress}
	return r.run()
}

// step is how a piece of a cycle ended.
type step uint8

const (
	stepOK    step = iota
	stepPause      // user pause or a guard Pause verdict
	stepStop       // r.final holds the verdict
)

type runner struct {
	cfg        Config
	inj        Injector
	clk        Clock
	g          Guard
	paused     func() bool
	onProgress func(Progress)

	rnd   *rand.Rand
	iv    interval
	sched schedule
	pts   pointSeq
	path  glidePath
	ops   [8]Op // every Send goes out of this buffer

	clicks  int64
	timeEnd int64 // 0 = no time limit
	held    bool  // a press is down (r.shift says whether Shift went with it)
	shift   bool
	final   Verdict
}

func (r *runner) run() Verdict {
	if v, ok := r.setup(); !ok {
		return v
	}
	v := r.loop()
	r.releaseAll()
	return v
}

// setup checks the config and readies the per-run state; false means the run
// cannot start and the verdict says why.
func (r *runner) setup() (Verdict, bool) {
	if r.cfg.Mode == 0 {
		r.cfg.Mode = ModeClick
	}
	if r.cfg.Keyboard && r.cfg.Scan == 0 {
		return Verdict{Kind: Stop, Code: CodeError, Reason: "No key to press"}, false
	}
	if r.cfg.Mode == ModeKeyHold && !r.cfg.Keyboard {
		return Verdict{Kind: Stop, Code: CodeError, Reason: "Key hold needs a key"}, false
	}
	if !r.cfg.Keyboard && r.cfg.Button == 0 {
		r.cfg.Button = ButtonLeft
	}
	r.rnd = newRand(r.cfg.Seed, r.clk)
	r.iv = newInterval(r.cfg.BaseIntervalNs, r.cfg.JitterSigma, r.rnd)
	r.pts = newPointSeq(r.cfg.Points, r.cfg.StopAfterPass, r.rnd)
	now := r.clk.Now()
	if r.cfg.TimeLimitNs > 0 {
		r.timeEnd = now + r.cfg.TimeLimitNs
	}
	r.sched.start(now, r.iv.base)
	return Verdict{}, true
}

func (r *runner) loop() Verdict {
	for {
		var s step
		switch r.cfg.Mode {
		case ModeMouseHoldInterval:
			s = r.holdCycle()
		case ModeKeyHold:
			s = r.keyHold()
		default:
			s = r.clickCycle()
		}
		switch s {
		case stepStop:
			return r.final
		case stepPause:
			if r.pause() == stepStop {
				return r.final
			}
			r.sched.resume(r.clk.Now())
		}
	}
}

// wait sleeps until deadline in slices of at most guardSliceNs, asking the
// guard between slices and ending at the time limit.
func (r *runner) wait(deadline int64) step {
	for {
		now := r.clk.Now()
		if r.timeEnd > 0 && now >= r.timeEnd {
			return r.stop(CodeTimeLimit, "Time limit reached")
		}
		if now >= deadline {
			return stepOK
		}
		until := min(deadline, now+guardSliceNs)
		if r.timeEnd > 0 {
			until = min(until, r.timeEnd)
		}
		if !r.clk.WaitUntil(until) {
			return r.interrupted()
		}
		if until < deadline {
			if s := r.check(r.clk.Now()); s != stepOK {
				return s
			}
		}
	}
}

// interrupted classifies a clock interrupt: pause when paused() says so,
// otherwise stop. The interrupt is cleared first (see Run).
func (r *runner) interrupted() step {
	r.clk.ClearInterrupt()
	if r.paused() {
		return stepPause
	}
	return r.stop(CodeInterrupted, "Stopped")
}

// check asks the guard.
func (r *runner) check(now int64) step {
	if r.g == nil {
		return stepOK
	}
	v := r.g.Check(now, r.clicks)
	switch v.Kind {
	case Pause:
		return stepPause
	case Stop:
		r.final = v
		return stepStop
	}
	return stepOK
}

// preClick runs before every press: the time limit, then the guard.
func (r *runner) preClick(now int64) step {
	if r.timeEnd > 0 && now >= r.timeEnd {
		return r.stop(CodeTimeLimit, "Time limit reached")
	}
	return r.check(now)
}

// pause releases held input and idles until neither the user nor the guard
// wants the run paused, or something stops it.
func (r *runner) pause() step {
	r.releaseAll()
	for {
		now := r.clk.Now()
		if r.timeEnd > 0 && now >= r.timeEnd {
			return r.stop(CodeTimeLimit, "Time limit reached")
		}
		guard := r.check(now)
		if guard == stepStop {
			return stepStop
		}
		if guard == stepOK && !r.paused() {
			return stepOK
		}
		until := now + guardSliceNs
		if r.timeEnd > 0 {
			until = min(until, r.timeEnd)
		}
		if !r.clk.WaitUntil(until) {
			r.clk.ClearInterrupt()
			if !r.paused() {
				return r.stop(CodeInterrupted, "Stopped")
			}
		}
	}
}

// finish counts n clicks, moves the click point on when the cycle is complete
// and reports progress, then applies the click limit and StopAfterPass.
func (r *runner) finish(n int64, cycleDone bool) step {
	r.clicks += n
	passDone := cycleDone && r.pts.advance()
	if r.onProgress != nil {
		idx, tick := r.pts.progress()
		r.onProgress(Progress{Clicks: r.clicks, ActivePoint: idx, PointTick: tick})
	}
	if r.cfg.ClickLimit > 0 && r.clicks >= r.cfg.ClickLimit {
		return r.stop(CodeClickLimit, "Click limit reached")
	}
	if passDone {
		return r.stop(CodePointsDone, "All click points completed")
	}
	return stepOK
}

// send injects r.ops[:n]. A short count (Windows refused some events, e.g. an
// elevated window in front) is not an error here; the guard reports that.
func (r *runner) send(n int) step {
	if _, err := r.inj.Send(r.ops[:n]); err != nil {
		return r.stop(CodeError, "Input injection failed: "+err.Error())
	}
	return stepOK
}

func (r *runner) stop(code, reason string) step {
	r.final = Verdict{Kind: Stop, Code: code, Reason: reason}
	return stepStop
}

// moveToPoint puts the cursor on the current click point before a cycle:
// nothing without points or when it already sits on a scatter-free point, a
// glide when the point changed and the run is slow enough, else a teleport.
// The glide uses glideDuration(drawn) of the cycle and waits like any other
// wait, so it can be paused or stopped part way.
func (r *runner) moveToPoint(drawn int64) step {
	if !r.pts.active() {
		return stepOK
	}
	x, y, move, newPoint := r.pts.next()
	if !move {
		return stepOK
	}
	if newPoint && r.cfg.Glide && r.iv.base >= glideMinBaseNs {
		if cx, cy, ok := r.inj.Cursor(); ok &&
			planGlide(&r.path, r.rnd, cx, cy, x, y, glideDuration(drawn)) {
			t0 := r.clk.Now()
			for i := range r.path.n {
				if s := r.wait(t0 + r.path.at[i]); s != stepOK {
					return s
				}
				if i == r.path.n-1 {
					break // the last waypoint is the target: teleport there exactly
				}
				if r.inj.GlideStep(r.path.x[i], r.path.y[i]) != nil {
					break
				}
			}
		}
	}
	if err := r.inj.Teleport(x, y); err != nil {
		return r.stop(CodeError, "Could not move the cursor: "+err.Error())
	}
	r.pts.placedAt()
	return stepOK
}
