package board

import (
	"errors"
	"fmt"
)

// A mirror is this Toru's copy of a board another Toru shares in the room
// (internal/boardshare, docs/whiteboard.md section 2). The other Toru, the
// host, owns the board and its one undo history. Here:
//
//   - every change, undo and redo sent to the Store for a mirror goes to the
//     host through its Remote, and returns the op as the host applied it;
//   - the host's ops come back through ApplyMirror, exactly as the host
//     applied them (same rev, time and who made what), in rev order;
//   - the whole board comes through Mirror when this Toru joins, or catches
//     up after a gap.
//
// Open board windows follow a mirror through OnOp like any other board. A
// mirror whose Remote is taken away (the host stopped sharing, or left the
// room) is an ordinary board again: a copy that stays here.

// Remote is a mirror's way to its host.
type Remote interface {
	// Apply sends a change and returns it as the host applied it. The
	// mirror has the op (ApplyMirror) by the time Apply returns.
	Apply(op Op) (Op, error)
	// Undo and Redo ask the host to step its history back or forward. They
	// return ErrNothingToUndo / ErrNothingToRedo as Doc's do.
	Undo(src Source) (Op, error)
	Redo(src Source) (Op, error)
}

// KindReset is an op OnOp hears when a board was replaced whole (a mirror
// joining or catching up): it carries only the new rev, and an open window
// reloads the board. It is never applied or saved.
const KindReset = "reset"

// ErrGap is ApplyMirror's answer to an op past the next rev: the ops before
// it haven't arrived yet.
var ErrGap = errors.New("ops are missing before this one")

// SetRemote makes board id a mirror whose changes go to r, or, with r nil,
// an ordinary board again.
func (s *Store) SetRemote(id string, r Remote) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.remotes == nil {
		s.remotes = map[string]Remote{}
	}
	if r == nil {
		delete(s.remotes, id)
		return
	}
	s.remotes[id] = r
}

// remote is board id's Remote, or nil when it isn't a live mirror.
func (s *Store) remote(id string) Remote {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remotes[id]
}

// IsMirror reports whether board id is a live mirror right now.
func (s *Store) IsMirror(id string) bool { return s.remote(id) != nil }

// Rev is a board's rev as it stands, or -1 when it can't be opened.
func (s *Store) Rev(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.doc(id)
	if err != nil {
		return -1
	}
	return d.b.Rev
}

// Mirror saves b, a host's whole board, as it is (its rev, items and who
// made them), replacing the board with its id, and starts its history
// again. Open windows hear a KindReset op and reload.
func (s *Store) Mirror(b Board) (Board, error) {
	b = clone(b)
	b.fill()
	if err := b.Validate(); err != nil {
		return Board{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeFile(b); err != nil {
		return Board{}, err
	}
	s.docs[b.ID] = NewDoc(b)
	if s.onOp != nil {
		s.onOp(b.ID, Op{Rev: b.Rev, At: b.UpdatedAt, Kind: KindReset})
	}
	return clone(b), nil
}

// ApplyMirror makes one of the host's ops on its mirror exactly as the host
// applied it. An op at or below the mirror's rev is one it has: it returns
// false and nothing changes. One past the next rev returns ErrGap. The op
// is checked like any other (its items, ids and links), keeps the host's
// rev, time and who made what, and isn't added to an undo history here.
func (s *Store) ApplyMirror(id string, op Op) (bool, error) {
	if op.Kind == KindReset {
		return false, errors.New("a reset isn't an op")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.doc(id)
	if err != nil {
		return false, err
	}
	switch {
	case op.Rev <= d.b.Rev:
		return false, nil
	case op.Rev > d.b.Rev+1:
		return false, ErrGap
	}
	saved := *d
	fwd, _, err := d.apply(op, op.Kind, true)
	if err != nil {
		return false, fmt.Errorf("the host's change %d doesn't fit this copy: %w", op.Rev, err)
	}
	if !op.At.IsZero() {
		d.b.UpdatedAt = op.At
		fwd.At = op.At
	}
	if err := s.writeFile(d.b); err != nil {
		*d = saved
		return false, fmt.Errorf("couldn't save board %q: %w", id, err)
	}
	if s.onOp != nil {
		s.onOp(id, fwd)
	}
	return true, nil
}

// MapSources changes every Source in the op (who sent it, and who made and
// changed each item) through f.
func (o *Op) MapSources(f func(Source) Source) {
	if o.Source != "" {
		o.Source = f(o.Source)
	}
	for i := range o.Frames {
		o.Frames[i].mapSources(f)
	}
	for i := range o.Blocks {
		o.Blocks[i].mapSources(f)
	}
	for i := range o.Ink {
		o.Ink[i].mapSources(f)
	}
	for i := range o.Links {
		o.Links[i].mapSources(f)
	}
}

// MapSources changes who made and changed each item through f.
func (b *Board) MapSources(f func(Source) Source) {
	for i := range b.Frames {
		b.Frames[i].mapSources(f)
	}
	for i := range b.Blocks {
		b.Blocks[i].mapSources(f)
	}
	for i := range b.Ink {
		b.Ink[i].mapSources(f)
	}
	for i := range b.Links {
		b.Links[i].mapSources(f)
	}
}

func (m *Meta) mapSources(f func(Source) Source) {
	if m.CreatedBy != "" {
		m.CreatedBy = f(m.CreatedBy)
	}
	if m.UpdatedBy != "" {
		m.UpdatedBy = f(m.UpdatedBy)
	}
}
