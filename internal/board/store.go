package board

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Dir is %AppData%\toru\boards. A var so tests can point it elsewhere.
var Dir = func() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru", "boards"), nil
}

// Summary is one board in the list. A board whose file can't be read is
// still listed, with Error saying why, so it isn't silently lost.
type Summary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Blocks    int       `json:"blocks"`
	// MirrorOf is set on a copy of someone else's shared board.
	MirrorOf *MirrorOf `json:"mirrorOf,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// Store keeps the boards on disk and one Doc (with its undo history) for
// each board that has been opened, so the board window and agents share
// one history. Every change is written to the board's file before it
// returns. Safe for use by several goroutines.
type Store struct {
	mu   sync.Mutex
	docs map[string]*Doc
	// onOp hears every change, undo and redo once it is saved, in the
	// order the board's rev counts them (it is called under mu).
	onOp func(id string, op Op)
	// remotes are the boards that are live mirrors of another Toru's
	// board (mirror.go): their changes go to the host.
	remotes map[string]Remote
	// folder is where the board files are; "" is Dir.
	folder string
}

// OnOp sets what hears every change to every board once it is saved: the
// op as applied, from agents and the board window alike, so open windows
// follow. It is called in rev order, under the store's lock, so it must not
// call back into the store.
func (s *Store) OnOp(f func(id string, op Op)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onOp = f
}

// NewStore returns a store over Dir.
func NewStore() *Store { return &Store{docs: map[string]*Doc{}} }

// NewStoreIn returns a store over its own folder, not Dir: tests use it
// for two Torus in one process. Pictures stay in ImagesDir.
func NewStoreIn(dir string) *Store { return &Store{docs: map[string]*Doc{}, folder: dir} }

func (s *Store) dir() (string, error) {
	if s.folder != "" {
		return s.folder, nil
	}
	return Dir()
}

func (s *Store) boardPath(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("bad board id %q", id)
	}
	dir, err := s.dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+Ext), nil
}

// readFile reads and checks one board file.
func readFile(path string) (Board, error) {
	f, err := os.Open(path)
	if err != nil {
		return Board{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return Board{}, err
	}
	if len(data) > maxFileBytes {
		return Board{}, errors.New("that file is too big to be a board")
	}
	return Decode(data)
}

// writeFile saves b, through a temporary file so a crash never leaves half
// a board.
func (s *Store) writeFile(b Board) error {
	path, err := s.boardPath(b.ID)
	if err != nil {
		return err
	}
	data, err := Encode(b)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// doc is the open Doc for id, reading the file the first time. s.mu must be
// held.
func (s *Store) doc(id string) (*Doc, error) {
	if d, ok := s.docs[id]; ok {
		return d, nil
	}
	path, err := s.boardPath(id)
	if err != nil {
		return nil, err
	}
	b, err := readFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no board %q", id)
	}
	if err != nil {
		return nil, fmt.Errorf("board %q can't be opened: %w", id, err)
	}
	if b.ID != id {
		return nil, fmt.Errorf("board %q can't be opened: its file says it is %q", id, b.ID)
	}
	d := NewDoc(b)
	s.docs[id] = d
	return d, nil
}

// Create makes an empty board named name and saves it.
func (s *Store) Create(name string) (Board, error) {
	name = strings.TrimSpace(name)
	if err := checkName(name, "board"); err != nil {
		return Board{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		id := "brd-" + randHex(8)
		path, err := s.boardPath(id)
		if err != nil {
			return Board{}, err
		}
		if _, err := os.Stat(path); err == nil {
			continue
		}
		b := New(id, name, now())
		if err := s.writeFile(b); err != nil {
			return Board{}, err
		}
		s.docs[id] = NewDoc(b)
		return clone(b), nil
	}
}

// List is every board in Dir, most recently changed first.
func (s *Store) List() ([]Summary, error) {
	dir, err := s.dir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Summary{}
	for _, e := range entries {
		name := e.Name()
		id := strings.TrimSuffix(name, Ext)
		if e.IsDir() || !strings.HasSuffix(name, Ext) || !idPattern.MatchString(id) {
			continue
		}
		var b Board
		if d, ok := s.docs[id]; ok {
			b = d.b
		} else {
			b, err = readFile(filepath.Join(dir, name))
			if err == nil && b.ID != id {
				err = fmt.Errorf("its file says it is board %q", b.ID)
			}
			if err != nil {
				sum := Summary{ID: id, Name: id, Error: err.Error()}
				if info, ierr := e.Info(); ierr == nil {
					sum.UpdatedAt = info.ModTime().UTC()
				}
				out = append(out, sum)
				continue
			}
		}
		out = append(out, Summary{ID: b.ID, Name: b.Name, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Blocks: len(b.Blocks), MirrorOf: b.MirrorOf})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Load is a board as it stands.
func (s *Store) Load(id string) (Board, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.doc(id)
	if err != nil {
		return Board{}, err
	}
	return d.Board(), nil
}

// Save writes a whole board, replacing the one with its id if there is one
// (an import, or a board read back from a .toruboard). It is checked
// first, and its undo history starts again.
func (s *Store) Save(b Board) (Board, error) {
	b = clone(b) // Validate compacts props in place: not the caller's
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
	return clone(b), nil
}

// change runs f on the board's Doc and saves the board if f succeeded. If
// the save fails the change is taken back, so memory and disk agree.
func (s *Store) change(id string, f func(d *Doc) (Op, error)) (Op, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.doc(id)
	if err != nil {
		return Op{}, err
	}
	saved := *d
	op, err := f(d)
	if err != nil {
		return Op{}, err
	}
	if err := s.writeFile(d.b); err != nil {
		*d = saved
		return Op{}, fmt.Errorf("couldn't save board %q: %w", id, err)
	}
	if s.onOp != nil {
		s.onOp(id, op)
	}
	return op, nil
}

// Apply makes a change to a board and saves it. It returns the op as
// applied (see Op).
func (s *Store) Apply(id string, op Op) (Op, error) {
	if r := s.remote(id); r != nil {
		return r.Apply(op)
	}
	return s.change(id, func(d *Doc) (Op, error) { return d.Apply(op) })
}

// Edit makes a change worked out from the board as it stands: f gets a copy
// of the board and returns the op to apply. Reading and applying happen
// under one lock, so no other change lands in between.
func (s *Store) Edit(id string, f func(b Board) (Op, error)) (Op, error) {
	if r := s.remote(id); r != nil {
		// A mirror: worked out from this copy and sent to the host, which
		// checks it against its own board.
		b, err := s.Load(id)
		if err != nil {
			return Op{}, err
		}
		op, err := f(b)
		if err != nil {
			return Op{}, err
		}
		return r.Apply(op)
	}
	return s.change(id, func(d *Doc) (Op, error) {
		op, err := f(d.Board())
		if err != nil {
			return Op{}, err
		}
		return d.Apply(op)
	})
}

// Undo takes back a board's latest change; src is who asked.
func (s *Store) Undo(id string, src Source) (Op, error) {
	if r := s.remote(id); r != nil {
		return r.Undo(src)
	}
	return s.change(id, func(d *Doc) (Op, error) { return d.Undo(src) })
}

// Redo makes a board's latest undone change again; src is who asked.
func (s *Store) Redo(id string, src Source) (Op, error) {
	if r := s.remote(id); r != nil {
		return r.Redo(src)
	}
	return s.change(id, func(d *Doc) (Op, error) { return d.Redo(src) })
}

// Rename gives a board a new name. It is an op like any other, so it can be
// undone.
func (s *Store) Rename(id, name string, src Source) (Op, error) {
	return s.Apply(id, Op{Source: src, Name: &name})
}

// Delete removes a board's file and forgets its history.
func (s *Store) Delete(id string) error {
	path, err := s.boardPath(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no board %q", id)
		}
		return err
	}
	delete(s.docs, id)
	delete(s.remotes, id)
	return nil
}

// Close forgets an open board's undo history (it stays saved on disk).
func (s *Store) Close(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.docs, id)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// RandomID is a fresh item id with prefix ("blk", "frm", "lnk"), like the
// ones Apply makes. Check it isn't on the board before using it.
func RandomID(prefix string) string { return prefix + "-" + randHex(6) }

// ValidID reports whether id can be a board or item id.
func ValidID(id string) bool { return idPattern.MatchString(id) }
