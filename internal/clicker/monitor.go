package clicker

import (
	"log"
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/safety"
)

// zonePollInterval is how often the idle start-zone monitor reads the cursor.
const zonePollInterval = 100 * time.Millisecond

// wantMonitor reports whether the start-zone monitor should run: armed, zones
// on, and at least one start zone.
func wantMonitor(s Settings) bool {
	if !s.Armed || !s.Profile.ZonesEnabled {
		return false
	}
	for _, z := range s.StopZones {
		if z.Action == ZoneStart {
			return true
		}
	}
	return false
}

// syncMonitor starts or stops the start-zone monitor to match the settings.
func (s *ClickerService) syncMonitor() {
	s.mu.Lock()
	want := wantMonitor(s.settings)
	switch {
	case want && s.monStop == nil:
		stop := make(chan struct{})
		s.monStop = stop
		s.mu.Unlock()
		go s.watchZones(stop)
		return
	case !want && s.monStop != nil:
		close(s.monStop)
		s.monStop = nil
	}
	s.mu.Unlock()
}

func (s *ClickerService) stopMonitor() {
	s.mu.Lock()
	if s.monStop != nil {
		close(s.monStop)
		s.monStop = nil
	}
	s.mu.Unlock()
}

// watchZones polls the cursor while the clicker is armed and idle-capable. On
// entering a start zone with nothing running it starts a run (no countdown)
// that remembers the zone; the guard stops that run (zoneLeft) when the cursor
// leaves, and the monitor stops it too as a backstop. The first sample only
// seeds the previous state, so arming with the cursor already inside a start
// zone does not start clicking.
func (s *ClickerService) watchZones(stop chan struct{}) {
	t := time.NewTicker(zonePollInterval)
	defer t.Stop()
	seeded, prevIn := false, false
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		x, y, ok := s.d.cursorPos()
		if !ok {
			continue
		}
		s.mu.Lock()
		zones := safetyZones(s.settings)
		r := s.cur
		s.mu.Unlock()
		action, idx := s.d.zoneAt(zones, x, y)
		in := action == safety.ZoneStart
		if !seeded {
			seeded, prevIn = true, in
			continue
		}
		switch {
		case in && !prevIn && r == nil:
			if _, err := s.begin(srcZone, nil, idx); err != nil {
				log.Printf("clicker: start zone: %v", err)
			}
		case !in && prevIn && r != nil && r.source == srcZone:
			s.stopRun(r.gen, StopZoneLeft)
		}
		prevIn = in
	}
}
