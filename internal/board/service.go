package board

import (
	"errors"
	"time"
)

// Service is the board window's way to its board (docs/whiteboard.md,
// section 2): it loads the board, sends the person's changes as ops, and
// undoes and redoes through the one history the board's agents share.
// Every change, from here or an agent, reaches open windows as a board:op
// event from the Store's OnOp, in rev order; the window keeps a copy of
// the board and applies each op once, by its rev.
type Service struct {
	store *Store
}

// NewService is the window's service over store, the one store agents use
// too.
func NewService(store *Store) *Service { return &Service{store: store} }

// Load is the board as it stands, with the rev the window counts from.
func (s *Service) Load(id string) (Board, error) { return s.store.Load(id) }

// Apply makes a change from the board window and returns it as applied.
// Its source is "person", or "agent:<name>" for a change an agent made
// through the window's live controls (board.act), whose name Go handed the
// window with the call. Anything else is refused.
func (s *Service) Apply(id string, op Op) (Op, error) {
	if op.Source == "" {
		op.Source = Person
	}
	if err := op.Source.Check(); err != nil {
		return Op{}, err
	}
	op.Rev, op.At, op.Kind = 0, time.Time{}, ""
	return s.store.Apply(id, op)
}

// Undo takes back the board's latest change, the person's or an agent's,
// for the person. It returns nil when there is nothing to undo.
func (s *Service) Undo(id string) (*Op, error) {
	return step(s.store.Undo(id, Person))
}

// Redo makes the latest undone change again, for the person. It returns
// nil when there is nothing to redo.
func (s *Service) Redo(id string) (*Op, error) {
	return step(s.store.Redo(id, Person))
}

func step(op Op, err error) (*Op, error) {
	if errors.Is(err, ErrNothingToUndo) || errors.Is(err, ErrNothingToRedo) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}
