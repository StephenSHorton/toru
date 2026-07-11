// Package history keeps a short list of recent screenshots and recordings so
// the tray menu can re-open them. Files live under %AppData%/toru/captures/ with
// an index.json; temps under %TEMP%/toru are NOT referenced (they get deleted
// when an overlay session ends).
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

// MaxItems is the cap for the tray "Recent" list (newest first).
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
	onChange func()
}

// New loads (or creates) the captures directory and index. onChange is called
// after every successful Add (and after load if desired by the caller for an
// initial tray build — the caller usually rebuilds once on start anyway).
func New(onChange func()) *Store {
	s := &Store{onChange: onChange}
	dir, err := capturesDir()
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

// Dir returns the captures directory (may be empty if UserConfigDir failed).
func (s *Store) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
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
	if len(s.items) > MaxItems {
		// Drop oldest files that fell off the list (best-effort).
		dropped := s.items[MaxItems:]
		s.items = s.items[:MaxItems]
		for _, d := range dropped {
			_ = os.Remove(d.Path)
		}
	}
	_ = saveIndex(s.index, s.items) // keep in-memory even if index write fails

	cb := s.onChange
	s.mu.Unlock()
	// Notify AFTER unlock so a tray rebuild that List()s us can't deadlock.
	if cb != nil {
		cb()
	}
	return item, nil
}

// pruneMissingLocked drops items whose files no longer exist. Caller holds mu.
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
