package board

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ImportFile reads a .toruboard file and adds it to the boards. It never
// replaces a board: when the file's id is already in use (a board exported
// from this computer and read back, say), the import gets a new id and
// both are kept. Pictures the board shows are not brought in with it.
func (s *Store) ImportFile(path string) (Board, error) {
	b, err := readFile(path)
	if err != nil {
		return Board{}, err
	}
	if strings.TrimSpace(b.Name) == "" {
		b.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	b.fill()
	if !idPattern.MatchString(b.ID) {
		b.ID = "brd-" + randHex(8) // checked again below, with a free id
	}
	if err := b.Validate(); err != nil {
		return Board{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.idTaken(b.ID) {
		b.ID = "brd-" + randHex(8)
	}
	if err := writeFile(b); err != nil {
		return Board{}, err
	}
	s.docs[b.ID] = NewDoc(b)
	return clone(b), nil
}

// idTaken reports whether a board with id is open or saved. Call it with
// s.mu held.
func (s *Store) idTaken(id string) bool {
	if _, ok := s.docs[id]; ok {
		return true
	}
	p, err := boardPath(id)
	if err != nil {
		return true
	}
	_, err = os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}
