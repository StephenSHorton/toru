// Package history keeps a short list of recent screenshots and recordings so
// the tray menu and dashboard Library can re-open them. Files live under a
// configurable library directory (default %AppData%/toru/captures/) with an
// index.json; temps under %TEMP%/toru are NOT referenced (they get deleted when
// an overlay session ends). Screenshots are added when the user hits Done after
// annotating; recordings are added when a recording finishes.
package history

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Kind of a captured artifact.
const (
	KindImage = "image"
	KindVideo = "video"
)

// MaxItems is how many captures the tray "Recent" menu shows (newest first).
// The library itself keeps everything unless the user turns on auto-delete
// (see Retention).
const MaxItems = 12

// Item is one recent capture shown in the tray menu.
type Item struct {
	ID      string    `json:"id"`
	Kind    string    `json:"kind"` // KindImage | KindVideo
	Path    string    `json:"path"` // absolute path under captures/
	Label   string    `json:"label"`
	TakenAt time.Time `json:"takenAt"`
}

// Store is the process-wide recent-captures list.
type Store struct {
	mu       sync.Mutex
	items    []Item
	dir      string // %AppData%/toru/captures
	index    string // %AppData%/toru/captures/index.json
	keep     Retention
	onChange func()
}

// New loads (or creates) the library directory and index. onChange is called
// after every successful Add (and after load if desired by the caller for an
// initial tray build — the caller usually rebuilds once on start anyway).
// The directory is the user-configured path when set, otherwise DefaultDir.
func New(onChange func()) *Store {
	s := &Store{onChange: onChange, keep: loadRetention()}
	dir, err := loadPreferredDir()
	if err != nil {
		return s
	}
	s.dir = dir
	s.index = filepath.Join(dir, "index.json")
	s.items = loadIndex(s.index)
	s.pruneMissingLocked()
	return s
}

// List returns a copy of the recent items (newest first).
func (s *Store) List() []Item {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Item, len(s.items))
	copy(out, s.items)
	return out
}

// Dir returns the library directory (may be empty if config setup failed).
func (s *Store) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// IsDefaultDir reports whether the active library path is the built-in default.
func (s *Store) IsDefaultDir() bool {
	s.mu.Lock()
	dir := s.dir
	s.mu.Unlock()
	def, err := DefaultDir()
	if err != nil {
		return false
	}
	return samePath(dir, def)
}

// SetDir switches the library folder to dir (must be absolute or cleanable),
// creates it if needed, reloads index.json from the new location, and persists
// the preference. Existing files in the previous folder are left in place (not
// migrated) so a mistaken path change is non-destructive.
func (s *Store) SetDir(dir string) error {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return fmt.Errorf("history: empty library path")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("history: resolve path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return fmt.Errorf("history: create library dir: %w", err)
	}
	// Reject paths that aren't writable directories.
	fi, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("history: stat library dir: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("history: library path is not a directory")
	}

	if err := savePreferredDir(abs); err != nil {
		return fmt.Errorf("history: persist library path: %w", err)
	}

	s.mu.Lock()
	s.dir = abs
	s.index = filepath.Join(abs, "index.json")
	s.items = loadIndex(s.index)
	s.pruneMissingLocked()
	_ = saveIndex(s.index, s.items)
	cb := s.onChange
	s.mu.Unlock()

	if cb != nil {
		cb()
	}
	emitHistoryChanged()
	return nil
}

// ResetDir restores the built-in default library folder.
func (s *Store) ResetDir() error {
	def, err := DefaultDir()
	if err != nil {
		return err
	}
	if err := savePreferredDir(""); err != nil {
		return err
	}
	return s.SetDir(def)
}

// Get returns the item with the given id, if present.
func (s *Store) Get(id string) (Item, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return it, true
		}
	}
	return Item{}, false
}

// Delete removes the item with id from the list and deletes its file.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	var removed *Item
	kept := s.items[:0]
	for i := range s.items {
		if s.items[i].ID == id {
			cp := s.items[i]
			removed = &cp
			continue
		}
		kept = append(kept, s.items[i])
	}
	s.items = kept
	if removed != nil {
		_ = saveIndex(s.index, s.items)
	}
	cb := s.onChange
	s.mu.Unlock()

	if removed == nil {
		return fmt.Errorf("history: unknown id %q", id)
	}
	_ = os.Remove(removed.Path)
	if cb != nil {
		cb()
	}
	emitHistoryChanged()
	return nil
}

// emitHistoryChanged notifies open dashboard windows that the recent list
// changed. Best-effort; no-op when the app isn't running (tests).
func emitHistoryChanged() {
	// Lazy import avoidance: application.Get is safe when nil.
	// Defined in emit_wails.go so unit tests without wails don't fail compile
	// on non-linked paths — here we call the shared helper.
	emitHistoryChangedImpl()
}

// SetOnChange replaces the change callback (used when the tray controller is
// ready after Store construction).
func (s *Store) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Add copies srcPath into the captures folder and prepends it to the recent
// list. kind is KindImage or KindVideo. Best-effort: errors are returned but
// callers typically log-and-ignore so a history failure never blocks capture.
func (s *Store) Add(srcPath, kind string) (Item, error) {
	s.mu.Lock()

	if s.dir == "" {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("history: no captures dir")
	}
	if srcPath == "" {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("history: empty source path")
	}
	if kind != KindImage && kind != KindVideo {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("history: unknown kind %q", kind)
	}
	if _, err := os.Stat(srcPath); err != nil {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("history: source: %w", err)
	}

	now := time.Now()
	ext := filepath.Ext(srcPath)
	if ext == "" {
		if kind == KindVideo {
			ext = ".webm"
		} else {
			ext = ".png"
		}
	}
	// Keep the id simple (date + time + ms) so filenames stay portable.
	id := now.Format("20060102-150405") + fmt.Sprintf("-%03d", now.Nanosecond()/1e6)
	base := id + ext
	dst := filepath.Join(s.dir, base)

	if err := copyFile(srcPath, dst); err != nil {
		s.mu.Unlock()
		return Item{}, err
	}

	label := formatLabel(kind, now)
	item := Item{
		ID:      id,
		Kind:    kind,
		Path:    dst,
		Label:   label,
		TakenAt: now,
	}
	// Newest first; drop any prior entry that pointed at a deleted path, then
	// prepend and cap.
	s.pruneMissingLocked()
	s.items = append([]Item{item}, s.items...)
	dropped := s.expireLocked(now)
	_ = saveIndex(s.index, s.items) // keep in-memory even if index write fails
	for _, d := range dropped {
		_ = os.Remove(d.Path) // best-effort
	}

	cb := s.onChange
	s.mu.Unlock()
	// Notify AFTER unlock so a tray rebuild that List()s us can't deadlock.
	if cb != nil {
		cb()
	}
	// Also broadcast so an open dashboard library refreshes live.
	emitHistoryChanged()
	return item, nil
}

// Retention returns the library's auto-delete choice.
func (s *Store) Retention() Retention {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.keep
}

// CountExpired reports how many current captures r would delete right now,
// so the UI can ask before a tighter limit removes existing files.
func (s *Store) CountExpired(r Retention) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, drop := splitExpired(s.items, r, time.Now())
	return len(drop)
}

// SetRetention saves r and applies it immediately.
func (s *Store) SetRetention(r Retention) error {
	r = r.normalized()
	if err := saveRetention(r); err != nil {
		return fmt.Errorf("history: persist retention: %w", err)
	}
	s.mu.Lock()
	s.keep = r
	s.mu.Unlock()
	s.Expire()
	return nil
}

// Expire deletes the captures the retention choice no longer keeps. Called
// on Add, on SetRetention, and periodically so "older than N days" applies
// to a tray app that runs for weeks without new captures.
func (s *Store) Expire() {
	s.mu.Lock()
	dropped := s.expireLocked(time.Now())
	if len(dropped) > 0 {
		_ = saveIndex(s.index, s.items)
	}
	cb := s.onChange
	s.mu.Unlock()
	if len(dropped) == 0 {
		return
	}
	for _, d := range dropped {
		_ = os.Remove(d.Path)
	}
	if cb != nil {
		cb()
	}
	emitHistoryChanged()
}

// expireLocked trims s.items to the retention choice and returns what fell
// off; the caller saves the index and removes the files. Caller holds mu.
func (s *Store) expireLocked(now time.Time) []Item {
	keep, drop := splitExpired(s.items, s.keep, now)
	if len(drop) > 0 {
		s.items = append([]Item(nil), keep...)
	}
	return drop
}

// pruneMissingLocked drops items whose files no longer exist. Caller holds mu.
// Replace swaps the file behind an existing item for srcPath (an edited
// re-open from the library) while keeping its id, label, time and position,
// so finishing the editor on a library capture updates it instead of adding
// a duplicate. The file gets a fresh name so cached thumbnails refresh.
func (s *Store) Replace(id, srcPath string) (Item, error) {
	s.mu.Lock()
	idx := -1
	for i := range s.items {
		if s.items[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("history: unknown id %q", id)
	}
	if _, err := os.Stat(srcPath); err != nil {
		s.mu.Unlock()
		return Item{}, fmt.Errorf("history: source: %w", err)
	}

	old := s.items[idx]
	ext := filepath.Ext(old.Path)
	dst := filepath.Join(s.dir, fmt.Sprintf("%s-e%d%s", id, time.Now().UnixMilli(), ext))
	if err := copyFile(srcPath, dst); err != nil {
		s.mu.Unlock()
		return Item{}, err
	}
	s.items[idx].Path = dst
	item := s.items[idx]
	_ = saveIndex(s.index, s.items)
	cb := s.onChange
	s.mu.Unlock()

	_ = os.Remove(old.Path)
	if cb != nil {
		cb()
	}
	emitHistoryChanged()
	return item, nil
}

func (s *Store) pruneMissingLocked() {
	kept := s.items[:0]
	for _, it := range s.items {
		if _, err := os.Stat(it.Path); err == nil {
			kept = append(kept, it)
		}
	}
	s.items = kept
}

func formatLabel(kind string, t time.Time) string {
	// e.g. "Screenshot  ·  3:04 PM" / "Recording  ·  3:04 PM"
	prefix := "Screenshot"
	if kind == KindVideo {
		prefix = "Recording"
	}
	return prefix + "  ·  " + t.Format("3:04 PM")
}

func capturesDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru", "captures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

type indexFile struct {
	Items []Item `json:"items"`
}

func loadIndex(path string) []Item {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var f indexFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil
	}
	return f.Items
}

func saveIndex(path string, items []Item) error {
	raw, err := json.MarshalIndent(indexFile{Items: items}, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
