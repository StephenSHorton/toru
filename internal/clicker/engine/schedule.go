package engine

// Deadline scheduling. Each fire advances an absolute deadline by the drawn
// interval, so the time spent sending input, holding the button, gliding or
// oversleeping never adds up as drift: a late fire just makes the next wait
// shorter. Two rules keep lateness from turning into a burst:
//   - more than 2 base intervals behind (a stall, a pause) re-bases the
//     deadline on now instead of catching up;
//   - catching up fires at most 2 times back to back, each at least minGap
//     after the previous fire, before the schedule re-bases.

// minGapNs separates back-to-back catch-up fires.
const minGapNs = msNs

// maxCatchUp is how many fires in a row may run behind the schedule.
const maxCatchUp = 2

type schedule struct {
	base     int64 // the configured interval, for the stall rule
	deadline int64 // when the next fire is due
	lastFire int64
	fired    bool // lastFire is valid
	behind   int  // fires in a row that left the deadline in the past
}

// start makes the first fire due at now.
func (s *schedule) start(now, base int64) {
	*s = schedule{base: base, deadline: now}
}

// due is when the next fire may happen.
func (s *schedule) due() int64 {
	if s.fired {
		return max(s.deadline, s.lastFire+minGapNs)
	}
	return s.deadline
}

// fire records a fire at now and advances the deadline by drawn.
func (s *schedule) fire(now, drawn int64) {
	if now-s.deadline > 2*s.base {
		s.deadline = now + drawn
		s.behind = 0
	} else {
		s.deadline += drawn
		if s.deadline < now {
			s.behind++
			if s.behind > maxCatchUp {
				s.deadline = now + drawn
				s.behind = 0
			}
		} else {
			s.behind = 0
		}
	}
	s.lastFire, s.fired = now, true
}

// resume re-bases after a pause: the next fire is due now, but never sooner
// than one base interval after the last one, so flicking in and out of a pause
// zone cannot click faster than the set rate.
func (s *schedule) resume(now int64) {
	s.deadline = now
	if s.fired {
		s.deadline = max(now, s.lastFire+s.base)
	}
	s.behind = 0
}
