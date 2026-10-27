package engine

// Cycle shapes. Every shape follows one counting rule: a press counts when its
// whole hold has run and the release went out. So a double click counts 1
// after its first tap and 1 more after the second, a hold cut short by a pause
// or a stop counts nothing, and a click point moves on only after a complete
// cycle.

// clickCycle is one ModeClick or ModeDouble cycle on the deadline schedule:
// wait for the deadline, move to the click point, then press-hold-release (or
// tap, gap, press-hold-release). Everything in the cycle, glide included,
// finishes at least 1 ms before the next deadline.
func (r *runner) clickCycle() step {
	if s := r.wait(r.sched.due()); s != stepOK {
		return s
	}
	now := r.clk.Now()
	if s := r.preClick(now); s != stepOK {
		return s
	}
	drawn := r.iv.draw()
	r.sched.fire(now, drawn)
	end := now + drawn - msNs // the last moment this cycle may still hold input
	if s := r.moveToPoint(drawn); s != stepOK {
		return s
	}
	double := r.cfg.Mode == ModeDouble &&
		(r.cfg.ClickLimit <= 0 || r.cfg.ClickLimit-r.clicks >= 2)
	if double {
		if s := r.tap(); s != stepOK {
			return s
		}
		if s := r.finish(1, false); s != stepOK {
			return s
		}
		gapEnd := min(r.clk.Now()+max(r.cfg.DoubleGapNs, 0), end)
		if s := r.wait(gapEnd); s != stepOK {
			return s
		}
		if s := r.preClick(r.clk.Now()); s != stepOK {
			return s
		}
	}
	return r.timedClick(drawn, end)
}

// timedClick presses, holds DutyFraction of the drawn interval (ending no
// later than end) and releases. A hold that rounds to nothing is a tap, with
// down and up in one Send.
func (r *runner) timedClick(drawn, end int64) step {
	t := r.clk.Now()
	hold := min(int64(r.cfg.DutyFraction*float64(drawn)), end-t)
	if hold <= 0 {
		if s := r.tap(); s != stepOK {
			return s
		}
		return r.finish(1, true)
	}
	if s := r.press(); s != stepOK {
		return s
	}
	if s := r.wait(t + hold); s != stepOK {
		return s // the pause/stop path releases the button
	}
	if s := r.release(); s != stepOK {
		return s
	}
	return r.finish(1, true)
}

// holdCycle is one ModeMouseHoldInterval cycle: press, hold for the drawn
// interval, release, then wait HoldGapNs with the button up. It runs off its
// own clock rather than the deadline schedule; a glide to a new point comes
// before the hold.
func (r *runner) holdCycle() step {
	if s := r.preClick(r.clk.Now()); s != stepOK {
		return s
	}
	drawn := r.iv.draw()
	if s := r.moveToPoint(drawn); s != stepOK {
		return s
	}
	if s := r.press(); s != stepOK {
		return s
	}
	if s := r.wait(r.clk.Now() + drawn); s != stepOK {
		return s
	}
	if s := r.release(); s != stepOK {
		return s
	}
	if s := r.finish(1, true); s != stepOK {
		return s
	}
	return r.wait(r.clk.Now() + max(r.cfg.HoldGapNs, 0))
}

// Typematic defaults for a KeyRepeat left zero: Windows' own defaults
// (keyboard delay 1 = 500 ms, speed 31 = about 30 per second).
const (
	defaultRepeatDelayNs  = 500 * msNs
	defaultRepeatPeriodNs = 33 * msNs
)

// keyHold holds the key like a finger would: one down, then after
// KeyRepeat.DelayNs a repeated down every PeriodNs, never an up until a pause
// or the end of the run. Each down counts. It returns only on pause or stop;
// after a pause the next call presses afresh, delay included.
func (r *runner) keyHold() step {
	delay, period := r.cfg.KeyRepeat.DelayNs, r.cfg.KeyRepeat.PeriodNs
	if delay <= 0 {
		delay = defaultRepeatDelayNs
	}
	if period <= 0 {
		period = defaultRepeatPeriodNs
	}
	if s := r.preClick(r.clk.Now()); s != stepOK {
		return s
	}
	if s := r.moveToPoint(period); s != stepOK {
		return s
	}
	if s := r.press(); s != stepOK {
		return s
	}
	next := r.clk.Now() + delay
	if s := r.finish(1, true); s != stepOK {
		return s
	}
	for {
		if s := r.wait(next); s != stepOK {
			return s
		}
		now := r.clk.Now()
		if s := r.preClick(now); s != stepOK {
			return s
		}
		if s := r.moveToPoint(period); s != stepOK {
			return s
		}
		if s := r.repeatKey(); s != stepOK {
			return s
		}
		if s := r.finish(1, true); s != stepOK {
			return s
		}
		// A real held key does not catch up after a stall.
		next += period
		if next <= now {
			next = now + period
		}
	}
}

// wantShift decides, from Caps Lock as it is right now, whether this press
// needs Shift around it to come out in the wanted case. The answer is stored
// with the press so the release lifts exactly what went down.
func (r *runner) wantShift() bool {
	if !r.cfg.Keyboard || (!r.cfg.WantUpper && !r.cfg.WantLower) {
		return false
	}
	caps := r.cfg.CapsLock != nil && r.cfg.CapsLock()
	return (r.cfg.WantUpper && !caps) || (r.cfg.WantLower && caps)
}

// putDown appends the press ops for the configured input to r.ops[n:].
func (r *runner) putDown(n int, shift bool) int {
	if !r.cfg.Keyboard {
		r.ops[n] = Op{Kind: OpMouseDown, Button: r.cfg.Button}
		return n + 1
	}
	if shift {
		r.ops[n] = Op{Kind: OpShiftDown}
		n++
	}
	r.ops[n] = Op{Kind: OpKeyDown, Scan: r.cfg.Scan, Extended: r.cfg.Extended}
	return n + 1
}

// putUp appends the release ops matching a press made with shift.
func (r *runner) putUp(n int, shift bool) int {
	if !r.cfg.Keyboard {
		r.ops[n] = Op{Kind: OpMouseUp, Button: r.cfg.Button}
		return n + 1
	}
	r.ops[n] = Op{Kind: OpKeyUp, Scan: r.cfg.Scan, Extended: r.cfg.Extended}
	n++
	if shift {
		r.ops[n] = Op{Kind: OpShiftUp}
		n++
	}
	return n
}

// press sends the down half of a press and marks it held.
func (r *runner) press() step {
	r.shift = r.wantShift()
	r.held = true
	return r.send(r.putDown(0, r.shift))
}

// release sends the up half matching the held press.
func (r *runner) release() step {
	r.held = false
	return r.send(r.putUp(0, r.shift))
}

// tap sends a whole press with no hold: down and up in one Send.
func (r *runner) tap() step {
	shift := r.wantShift()
	return r.send(r.putUp(r.putDown(0, shift), shift))
}

// repeatKey is a typematic repeat: another key-down while the key (and Shift,
// if the press used it) is still held.
func (r *runner) repeatKey() step {
	r.ops[0] = Op{Kind: OpKeyDown, Scan: r.cfg.Scan, Extended: r.cfg.Extended}
	return r.send(1)
}

// releaseAll lifts whatever a press left down. Errors are ignored: this runs
// on the way out of a pause or the run, and there is nothing better to do.
func (r *runner) releaseAll() {
	if !r.held {
		return
	}
	r.held = false
	_, _ = r.inj.Send(r.ops[:r.putUp(0, r.shift)])
}
