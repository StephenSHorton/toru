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

	"github.com/wailsapp/wails/v3/pkg/application"
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

// Add copies srcPath into the library folder and prepends it to the recent list.
// Used by the editor Done path (annotated PNG) and any other frontend import.
// kind is "image" or "video".
func (s *HistoryService) Add(srcPath, kind string) (Item, error) {
	if s == nil || s.store == nil {
		return Item{}, fmt.Errorf("history: not configured")
	}
	return s.store.Add(srcPath, kind)
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

// OpenFolder reveals the library directory in the OS file manager.
func (s *HistoryService) OpenFolder() error {
	if s == nil || s.store == nil {
		return fmt.Errorf("history: not configured")
	}
	dir := s.store.Dir()
	if dir == "" {
		return fmt.Errorf("history: no library dir")
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

// GetDir returns the absolute path of the library folder (empty if unset).
func (s *HistoryService) GetDir() string {
	if s == nil || s.store == nil {
		return ""
	}
	return s.store.Dir()
}

// IsDefaultDir reports whether the library uses the built-in default path.
func (s *HistoryService) IsDefaultDir() bool {
	if s == nil || s.store == nil {
		return true
	}
	return s.store.IsDefaultDir()
}

// SetDir switches the library folder (creates it if needed) and reloads the list.
func (s *HistoryService) SetDir(path string) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("history: not configured")
	}
	return s.store.SetDir(path)
}

// ResetDir restores the built-in default library folder.
func (s *HistoryService) ResetDir() error {
	if s == nil || s.store == nil {
		return fmt.Errorf("history: not configured")
	}
	return s.store.ResetDir()
}

// PickDir opens a native folder picker and, if the user chooses a path, switches
// the library there. Returns the new absolute path, or "" if the user cancelled.
func (s *HistoryService) PickDir() (string, error) {
	if s == nil || s.store == nil {
		return "", fmt.Errorf("history: not configured")
	}
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("history: no running application")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle("Choose Toru library folder")
	if cur := s.store.Dir(); cur != "" {
		dlg = dlg.SetDirectory(cur)
	}
	if win := app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		// Cancel is not an error for the frontend.
		return "", nil
	}
	if err := s.store.SetDir(chosen); err != nil {
		return "", err
	}
	return s.store.Dir(), nil
}
