package clicker

import (
	"log"

	"github.com/StephenSHorton/toru/internal/clicker/overlay"
)

// deleteRadius is how close (px) a Ctrl+right click must be to delete a point.
const deleteRadius = 10

// StartPointPick starts picking click points on screen: right click adds a
// point (Shift keeps picking), Ctrl+right click deletes the nearest one, Esc
// ends. Results arrive as clicker:settings and clicker:pick events.
func (s *ClickerService) StartPointPick() error {
	return s.startPick(overlay.PickPoint, "")
}

// StartZonePick starts drawing a zone with a right-drag; zoneID "" (or an id
// that no longer exists) adds a new one.
func (s *ClickerService) StartZonePick(zoneID string) error {
	return s.startPick(overlay.PickZone, zoneID)
}

// CancelPick ends an active pick without changes.
func (s *ClickerService) CancelPick() {
	s.mu.Lock()
	cancel := s.pickCancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// startPick runs one pick. The overlay calls back on its hook thread, so the
// callbacks only queue work; one goroutine per pick applies it in order.
func (s *ClickerService) startPick(kind overlay.PickKind, zoneID string) error {
	s.mu.Lock()
	if s.picking {
		s.mu.Unlock()
		return clickerErr(ErrPickActive, "A pick is already in progress")
	}
	s.picking = true
	s.mu.Unlock()

	s.stopAndWait(StopPick, shutdownWait)

	s.mu.Lock()
	set := s.settings
	s.mu.Unlock()
	scene := safetyScene(set, s.monitors())
	pts := scenePoints(set.Profile.ClickPoints.Points)

	kindName := "point"
	if kind == overlay.PickZone {
		kindName = "zone"
	}
	queue := make(chan func(), 64)
	ended := false
	// push never blocks the overlay's hook thread; a full queue (the worker
	// is stuck or already gone) drops the event.
	push := func(fn func()) {
		select {
		case queue <- fn:
		default:
			log.Printf("clicker: pick event dropped")
		}
	}
	cb := overlay.PickCallbacks{
		OnPoint: func(x, y int32, _ bool) {
			push(func() { s.pickAddPoint(x, y) })
		},
		OnDelete: func(x, y int32) {
			push(func() { s.pickDeletePoint(x, y) })
		},
		OnZone: func(l, t, r, b int32) {
			push(func() { zoneID = s.pickZone(zoneID, l, t, r, b) })
		},
		OnEnd: func(cancelled bool) {
			select {
			case queue <- func() { ended = true; s.endPick(kindName, cancelled) }:
			default: // never lose the end: the page would think a pick is still on
				go s.endPick(kindName, cancelled)
			}
		},
	}
	go func() {
		for fn := range queue {
			fn()
			if ended {
				return
			}
		}
	}()

	cancel, err := s.d.startPick(kind, scene, pts, cb)
	if err != nil {
		close(queue)
		s.mu.Lock()
		s.picking = false
		s.mu.Unlock()
		return clickerErrf(ErrHookFailed, "Couldn't start picking on screen: %v", err)
	}
	s.mu.Lock()
	if s.picking {
		s.pickCancel = cancel
	}
	s.mu.Unlock()
	s.emitPick(PickEvent{Kind: kindName, Phase: "active", Index: -1})
	return nil
}

func (s *ClickerService) emitPick(ev PickEvent) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit(EventPick, ev)
	}
}

// pickMutate applies fn to a copy of the settings and commits it.
func (s *ClickerService) pickMutate(fn func(*Settings) int) int {
	s.mu.Lock()
	next := s.settings
	s.mu.Unlock()
	next.Profile.ClickPoints.Points = append([]ClickPoint{}, next.Profile.ClickPoints.Points...)
	next.StopZones = append([]StopZone{}, next.StopZones...)
	idx := fn(&next)
	if idx < 0 {
		return idx
	}
	if err := s.commitFromGo(Normalize(next)); err != nil {
		log.Printf("clicker: pick: %v", err)
	}
	return idx
}

func (s *ClickerService) pickAddPoint(x, y int32) {
	idx := s.pickMutate(func(st *Settings) int {
		st.Profile.ClickPoints.Points = append(st.Profile.ClickPoints.Points, ClickPoint{
			ID: newID(), X: x, Y: y, Clicks: st.NewPointClicks, Radius: st.NewPointRadius,
		})
		return len(st.Profile.ClickPoints.Points) - 1
	})
	s.emitPick(PickEvent{Kind: "point", Phase: "added", Index: idx})
	s.redrawPickPoints()
}

func (s *ClickerService) pickDeletePoint(x, y int32) {
	idx := s.pickMutate(func(st *Settings) int {
		pts := st.Profile.ClickPoints.Points
		best, bestD := -1, int64(deleteRadius*deleteRadius)
		for i, p := range pts {
			dx, dy := int64(p.X-x), int64(p.Y-y)
			if d := dx*dx + dy*dy; d <= bestD {
				best, bestD = i, d
			}
		}
		if best >= 0 {
			st.Profile.ClickPoints.Points = append(pts[:best], pts[best+1:]...)
		}
		return best
	})
	if idx < 0 {
		return
	}
	s.emitPick(PickEvent{Kind: "point", Phase: "deleted", Index: idx})
	s.redrawPickPoints()
}

func (s *ClickerService) redrawPickPoints() {
	if s.d.updatePickPoints == nil {
		return
	}
	s.mu.Lock()
	pts := scenePoints(s.settings.Profile.ClickPoints.Points)
	s.mu.Unlock()
	s.d.updatePickPoints(pts)
}

// pickZone writes a drawn rectangle into zone id (or a new stop zone) and
// returns the zone's id so a further drag in the same pick edits it again.
func (s *ClickerService) pickZone(id string, l, t, r, b int32) string {
	if r < l {
		l, r = r, l
	}
	if b < t {
		t, b = b, t
	}
	w, h := max(r-l, 1), max(b-t, 1)
	idx := s.pickMutate(func(st *Settings) int {
		for i := range st.StopZones {
			if id != "" && st.StopZones[i].ID == id {
				st.StopZones[i].X, st.StopZones[i].Y = l, t
				st.StopZones[i].W, st.StopZones[i].H = w, h
				return i
			}
		}
		id = newID()
		st.StopZones = append(st.StopZones, StopZone{ID: id, X: l, Y: t, W: w, H: h, Action: ZoneStop})
		return len(st.StopZones) - 1
	})
	s.syncMonitor()
	s.emitPick(PickEvent{Kind: "zone", Phase: "added", Index: idx})
	return id
}

func (s *ClickerService) endPick(kind string, cancelled bool) {
	s.mu.Lock()
	s.picking = false
	s.pickCancel = nil
	s.mu.Unlock()
	phase := "done"
	if cancelled {
		phase = "cancelled"
	}
	s.emitPick(PickEvent{Kind: kind, Phase: phase, Index: -1})
}
