// HistoryService is the Wails-bound recent-captures API used by the dashboard
// (and any future UI that wants the same list the tray menu shows).
//
// JS binding name: HistoryService.*
package history

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// HistoryService is the Wails-bound history API.
type HistoryService struct {
	store     *Store
	openImage func(path string)
	openVideo func(path string)
}

// NewService wraps a Store for Wails binding.
func NewService(store *Store) *HistoryService {
	return &HistoryService{store: store}
}

// SetOpeners injects the window openers for re-opening a capture (image →
// annotation editor, video → trim editor). Wired from main.
//
//wails:ignore
func (s *HistoryService) SetOpeners(openImage, openVideo func(path string)) {
	s.openImage = openImage
	s.openVideo = openVideo
}

// List returns recent captures newest-first (may be empty, never nil).
func (s *HistoryService) List() []Item {
	if s == nil || s.store == nil {
		return []Item{}
	}
	items := s.store.List()
	if items == nil {
		return []Item{}
	}
	return items
}

// Open re-opens a capture by id: screenshots in the annotation editor,
// recordings in the trim editor.
func (s *HistoryService) Open(id string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("history: not configured")
	}
	it, ok := s.store.Get(id)
	if !ok {
		return fmt.Errorf("history: unknown id %q", id)
	}
	if _, err := os.Stat(it.Path); err != nil {
		return fmt.Errorf("history: file missing: %w", err)
	}
	switch it.Kind {
	case KindVideo:
		if s.openVideo != nil {
			s.openVideo(it.Path)
		}
	default:
		if s.openImage != nil {
			s.openImage(it.Path)
		}
	}
	return nil
}

// OpenFolder reveals the captures directory in the OS file manager.
func (s *HistoryService) OpenFolder() error {
	if s == nil || s.store == nil {
		return fmt.Errorf("history: not configured")
	}
	dir := s.store.Dir()
	if dir == "" {
		return fmt.Errorf("history: no captures dir")
	}
	if runtime.GOOS == "windows" {
		return exec.Command("explorer.exe", dir).Start()
	}
	return exec.Command("xdg-open", dir).Start()
}

// Delete removes one capture from the list and deletes its file.
func (s *HistoryService) Delete(id string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("history: not configured")
	}
	return s.store.Delete(id)
}
