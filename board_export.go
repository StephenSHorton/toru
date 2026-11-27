package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/boardexport"
	"github.com/StephenSHorton/toru/internal/export"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// board_export.go: the whiteboard's exports and import (docs/whiteboard.md
// section 3) for the board window's Export menu (BoardExportService) and for
// agents (board.export and board.import, agent_commands_boards.go). Both go
// through internal/boardexport, so the person and agents get the same files.

// newBoardExporter is the exporter over the board viewer Toru embeds
// (frontend/dist/board-export.html) and its own WebView2 folder.
func newBoardExporter() *boardexport.Exporter {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return &boardexport.Exporter{
		Viewer:     func() ([]byte, error) { return fs.ReadFile(assets, "frontend/dist/board-export.html") },
		ReadImage:  board.ReadImage,
		WebViewDir: filepath.Join(dir, "toru", "board-export-webview"),
	}
}

// exportFileName is a file name for a board's export: its name, without
// characters Windows won't take in one.
func exportFileName(name string, f boardexport.Format) string {
	clean := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '-'
		}
		return r
	}, strings.TrimSpace(name))
	clean = strings.TrimRight(clean, ". ")
	if clean == "" {
		clean = "Whiteboard"
	}
	return clean + f.Ext()
}

var formatFilters = map[boardexport.Format][2]string{
	boardexport.Toruboard: {"Toru board", "*.toruboard"},
	boardexport.PNG:       {"PNG picture", "*.png"},
	boardexport.PDF:       {"PDF document", "*.pdf"},
	boardexport.HTML:      {"Web page", "*.html"},
	boardexport.Markdown:  {"Markdown", "*.md"},
}

// BoardExportService is the board window's Export menu: save a board in a
// format, copy it as a picture or an outline, and import a .toruboard.
type BoardExportService struct {
	store    *board.Store
	exporter *boardexport.Exporter
	copier   *export.ExportService
	open     func(id string)
	emit     func(name string, data any)
	app      *application.App
}

// SetApp gives the service the running app, for its file dialogs.
//
//wails:ignore
func (s *BoardExportService) SetApp(app *application.App) { s.app = app }

// Save exports board id in format (toruboard, png, pdf, html or markdown)
// to a file the person picks. It returns the path, or "" if they cancelled.
func (s *BoardExportService) Save(id, format string) (string, error) {
	f, err := boardexport.ParseFormat(format)
	if err != nil {
		return "", err
	}
	b, err := s.store.Load(id)
	if err != nil {
		return "", err
	}
	if s.app == nil {
		return "", errors.New("no app for the save dialog")
	}
	filter := formatFilters[f]
	dlg := s.app.Dialog.SaveFile().SetFilename(exportFileName(b.Name, f)).AddFilter(filter[0], filter[1])
	if win := s.app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil // cancelled
	}
	if !strings.EqualFold(filepath.Ext(path), f.Ext()) {
		path += f.Ext()
	}
	data, err := s.exporter.Export(context.Background(), b, f)
	if err != nil {
		return "", err
	}
	if err := writeExport(path, data); err != nil {
		return "", err
	}
	return path, nil
}

// Copy puts board id on the clipboard: format png as a picture, markdown as
// its outline.
func (s *BoardExportService) Copy(id, format string) error {
	f, err := boardexport.ParseFormat(format)
	if err != nil {
		return err
	}
	b, err := s.store.Load(id)
	if err != nil {
		return err
	}
	switch f {
	case boardexport.Markdown:
		return s.copier.CopyTextAs(board.Markdown(b), "Board outline")
	case boardexport.PNG:
		data, err := s.exporter.PNG(context.Background(), b)
		if err != nil {
			return err
		}
		tmp, err := os.CreateTemp("", "toru-board-*.png")
		if err != nil {
			return err
		}
		defer func() { _ = os.Remove(tmp.Name()) }()
		_, werr := tmp.Write(data)
		if cerr := tmp.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
		}
		return s.copier.CopyToClipboardAs(tmp.Name(), export.MediaImage, "Board picture")
	}
	return fmt.Errorf("a board copies as png or markdown, not %s", f)
}

// Import asks for a .toruboard, saves it as a board (under a new id if its
// own is taken) and opens it. It returns the board's id, or "" if the
// person cancelled.
func (s *BoardExportService) Import() (string, error) {
	if s.app == nil {
		return "", errors.New("no app for the open dialog")
	}
	dlg := s.app.Dialog.OpenFile().CanChooseFiles(true).CanChooseDirectories(false).
		SetTitle("Import a whiteboard").AddFilter("Toru board", "*.toruboard")
	if win := s.app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil
	}
	b, err := s.store.ImportFile(path)
	if err != nil {
		return "", err
	}
	if s.emit != nil {
		s.emit(evBoardsChanged, map[string]any{"board": b.ID})
	}
	if s.open != nil {
		s.open(b.ID)
	}
	return b.ID, nil
}

// writeExport writes an export through a temporary file beside it, so a
// failed write never leaves half a file where an old one was.
func writeExport(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".toru-export-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
	}
	return werr
}
