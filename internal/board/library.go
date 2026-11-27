package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// BlocksDir is %AppData%\toru\blocks, the person's own block library: one
// <type>.json file per custom block type (BlockDef). A var so tests can
// point it elsewhere.
var BlocksDir = func() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru", "blocks"), nil
}

// MaxLibraryBlocks is how many custom block types one library keeps.
const MaxLibraryBlocks = 500

// ErrBlockExists is Define's error for a type the library has.
var ErrBlockExists = errors.New("block type exists")

// BrokenBlock is a library file that can't be read, listed so it isn't
// silently lost.
type BrokenBlock struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

// LibraryList is the library as board.block.list and the board window see
// it: every block, by label, and the files that couldn't be read.
type LibraryList struct {
	Blocks []BlockDef    `json:"blocks"`
	Broken []BrokenBlock `json:"broken,omitempty"`
}

// Library is the person's block library on disk. Safe for use by several
// goroutines.
type Library struct {
	mu       sync.Mutex
	onChange func()
}

// NewLibrary is the library over BlocksDir.
func NewLibrary() *Library { return &Library{} }

// OnChange sets what hears every change to the library (the
// blocks:changed event).
func (l *Library) OnChange(f func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.onChange = f
}

func (l *Library) changed() {
	if l.onChange != nil {
		l.onChange()
	}
}

func blockPath(t string) (string, error) {
	if !typePattern.MatchString(t) {
		return "", fmt.Errorf("bad block type name %q", t)
	}
	dir, err := BlocksDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, t+".json"), nil
}

func readBlock(path string) (BlockDef, error) {
	f, err := os.Open(path)
	if err != nil {
		return BlockDef{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxDefBytes*2+1))
	if err != nil {
		return BlockDef{}, err
	}
	if len(data) > MaxDefBytes*2 {
		return BlockDef{}, errors.New("the file is too big to be a block type")
	}
	var d BlockDef
	if err := json.Unmarshal(data, &d); err != nil {
		return BlockDef{}, fmt.Errorf("not a block type: %w", err)
	}
	if err := d.Check(); err != nil {
		return BlockDef{}, err
	}
	if want := strings.TrimSuffix(filepath.Base(path), ".json"); d.Type != want {
		return BlockDef{}, fmt.Errorf("the file is named %s but holds block type %q", filepath.Base(path), d.Type)
	}
	return d, nil
}

func writeBlockDef(d BlockDef) error {
	path, err := blockPath(d.Type)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(d, "", "  ")
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

// List is every block in the library, sorted by label.
func (l *Library) List() (LibraryList, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.list()
}

func (l *Library) list() (LibraryList, error) {
	out := LibraryList{Blocks: []BlockDef{}}
	dir, err := BlocksDir()
	if err != nil {
		return out, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		d, err := readBlock(filepath.Join(dir, e.Name()))
		if err != nil {
			out.Broken = append(out.Broken, BrokenBlock{File: e.Name(), Error: err.Error()})
			continue
		}
		out.Blocks = append(out.Blocks, d)
	}
	sort.Slice(out.Blocks, func(i, j int) bool {
		a, b := strings.ToLower(out.Blocks[i].Label), strings.ToLower(out.Blocks[j].Label)
		if a != b {
			return a < b
		}
		return out.Blocks[i].Type < out.Blocks[j].Type
	})
	return out, nil
}

// Get is one block of the library.
func (l *Library) Get(t string) (BlockDef, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.get(t)
}

func (l *Library) get(t string) (BlockDef, error) {
	path, err := blockPath(t)
	if err != nil {
		return BlockDef{}, err
	}
	d, err := readBlock(path)
	if errors.Is(err, os.ErrNotExist) {
		return BlockDef{}, fmt.Errorf("your block library has no block type %q (board.block.list lists it)", t)
	}
	return d, err
}

func (l *Library) has(t string) bool {
	path, err := blockPath(t)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

func (l *Library) room() error {
	dir, err := BlocksDir()
	if err != nil {
		return err
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) >= MaxLibraryBlocks {
		return fmt.Errorf("your block library is full (%d block types): remove one first", MaxLibraryBlocks)
	}
	return nil
}

// Define adds a new block type: version 1, with a new origin. Its Version
// and Origin as sent are ignored.
func (l *Library) Define(d BlockDef) (BlockDef, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := d.checkContent(); err != nil {
		return BlockDef{}, err
	}
	if l.has(d.Type) {
		return BlockDef{}, fmt.Errorf("%w: your library already has %q: change it with board.block.update, or pick another name", ErrBlockExists, d.Type)
	}
	if err := l.room(); err != nil {
		return BlockDef{}, err
	}
	d.Version, d.Origin = 1, RandomID("blt")
	if err := d.Check(); err != nil {
		return BlockDef{}, err
	}
	if err := writeBlockDef(d); err != nil {
		return BlockDef{}, err
	}
	l.changed()
	return d, nil
}

// Update changes a block type: f gets a copy to change, and the result is
// saved as the next version, with the same type and origin.
func (l *Library) Update(t string, f func(d *BlockDef) error) (BlockDef, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	old, err := l.get(t)
	if err != nil {
		return BlockDef{}, err
	}
	d := old
	if err := f(&d); err != nil {
		return BlockDef{}, err
	}
	d.Type, d.Origin, d.Version = old.Type, old.Origin, old.Version+1
	if err := d.Check(); err != nil {
		return BlockDef{}, err
	}
	if err := writeBlockDef(d); err != nil {
		return BlockDef{}, err
	}
	l.changed()
	return d, nil
}

// Remove takes a block type out of the library. Boards that use it keep
// their own copy, so they still show it.
func (l *Library) Remove(t string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	path, err := blockPath(t)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("your block library has no block type %q", t)
		}
		return err
	}
	l.changed()
	return nil
}

// Save keeps a board's copy of a block type in the library, so the person
// can use it on their own boards. as, when set, saves it under another
// name, as a new block of its own (version 1, new origin).
//
// Without as: a type the library lacks is saved as it is, origin and
// version kept, so the two stay the same block. A newer version of the
// same block replaces the library's; the same or an older one changes
// nothing. A different block with the same name is refused: give as.
func (l *Library) Save(d BlockDef, as string) (saved BlockDef, changed bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := d.Check(); err != nil {
		return BlockDef{}, false, err
	}
	if as != "" && as != d.Type {
		d.Type = as
		if err := d.checkContent(); err != nil {
			return BlockDef{}, false, err
		}
		if l.has(as) {
			return BlockDef{}, false, fmt.Errorf("%w: your library already has %q: pick another name", ErrBlockExists, as)
		}
		if err := l.room(); err != nil {
			return BlockDef{}, false, err
		}
		d.Version, d.Origin = 1, RandomID("blt")
	} else if l.has(d.Type) {
		mine, err := l.get(d.Type)
		if err != nil {
			return BlockDef{}, false, fmt.Errorf("your library's %q can't be read (%v): remove it first", d.Type, err)
		}
		if mine.Origin != d.Origin {
			return BlockDef{}, false, fmt.Errorf("%w: your library has a different block type called %q: save this one under another name (as)", ErrBlockExists, d.Type)
		}
		if d.Version <= mine.Version {
			return mine, false, nil
		}
	} else if err := l.room(); err != nil {
		return BlockDef{}, false, err
	}
	if err := writeBlockDef(d); err != nil {
		return BlockDef{}, false, err
	}
	l.changed()
	return d, true, nil
}
