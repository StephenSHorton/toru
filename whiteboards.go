package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// whiteboards.go is how people reach boards from Toru (docs/whiteboard.md,
// section 5): the main window's Whiteboards page lists, makes, opens and
// imports boards, the tray's "New whiteboard" makes one, and the Library's
// "Send to whiteboard" puts a screenshot on a board as an image block.
// Changes go through the one board store, as the person, so open board
// windows follow them and they undo like any other change.

// newBoardName is the name a board gets when the person gives none.
const newBoardName = "Untitled whiteboard"

// sentImageWidth is the widest a sent screenshot lands on a board, in world
// units; a smaller one keeps its own size.
const sentImageWidth = 480.0

// WhiteboardsService is the main window's and tray's way to the boards.
type WhiteboardsService struct {
	boards *board.Store
	images *board.ImageService
	open   func(id string)
	// close shuts a board's window if it is open (after a delete).
	close func(id string)
	emit  func(name string, data any)
}

// List is every board, most recently changed first. A board whose file
// can't be read is listed with its error.
func (s *WhiteboardsService) List() ([]board.Summary, error) { return s.boards.List() }

// Create makes an empty board and opens its window. An empty name gives
// "Untitled whiteboard" (numbered when that is taken).
func (s *WhiteboardsService) Create(name string) (board.Summary, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = s.freeName()
	}
	b, err := s.boards.Create(name)
	if err != nil {
		return board.Summary{}, err
	}
	s.changed()
	s.Open(b.ID)
	return summary(b), nil
}

// Open shows a board's window, or brings it forward if it is open.
func (s *WhiteboardsService) Open(id string) {
	if s.open != nil {
		s.open(id)
	}
}

// Rename gives a board a new name, as the person. It is an op like any
// other, so an open board window shows it and it can be undone there.
func (s *WhiteboardsService) Rename(id, name string) (board.Summary, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return board.Summary{}, errors.New("a board needs a name")
	}
	if _, err := s.boards.Rename(id, name, board.Person); err != nil {
		return board.Summary{}, err
	}
	b, err := s.boards.Load(id)
	if err != nil {
		return board.Summary{}, err
	}
	s.changed()
	return summary(b), nil
}

// Delete removes a board and its file for good, closing its window if it
// is open. The Whiteboards page asks the person first.
func (s *WhiteboardsService) Delete(id string) error {
	if err := s.boards.Delete(id); err != nil {
		return err
	}
	if s.close != nil {
		s.close(id)
	}
	s.changed()
	return nil
}

// PickImport asks for a .toruboard file, adds it as a board and opens it.
// It never replaces a board (see board.Store.ImportFile). ok is false when
// the dialog was cancelled.
func (s *WhiteboardsService) PickImport() (sum board.Summary, ok bool, err error) {
	app := application.Get()
	if app == nil {
		return board.Summary{}, false, errors.New("no running application")
	}
	dlg := app.Dialog.OpenFile().
		SetTitle("Import whiteboard").
		CanChooseFiles(true).
		AddFilter("Toru whiteboard (.toruboard)", "*"+board.Ext).
		AddFilter("All Files", "*.*")
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return board.Summary{}, false, nil // cancelled (the dialog reports cancel as an error)
	}
	sum, err = s.Import(path)
	return sum, err == nil, err
}

// Import adds the .toruboard file at path as a board and opens it.
//
//wails:ignore
func (s *WhiteboardsService) Import(path string) (board.Summary, error) {
	b, err := s.boards.ImportFile(path)
	if err != nil {
		return board.Summary{}, fmt.Errorf("couldn't import that board: %w", err)
	}
	s.changed()
	s.Open(b.ID)
	return summary(b), nil
}

// SendScreenshot puts the Library screenshot libraryID on board boardID as
// an image block, below what is there, and opens the board. An empty
// boardID makes a new board for it. It returns the board.
func (s *WhiteboardsService) SendScreenshot(boardID, libraryID string) (board.Summary, error) {
	if s.images == nil {
		return board.Summary{}, errors.New("board images aren't set up")
	}
	img, err := s.images.ImportLibrary(libraryID)
	if err != nil {
		return board.Summary{}, err
	}
	label := ""
	for _, it := range s.images.LibraryImages() {
		if it.ID == libraryID {
			label = it.Label
		}
	}
	made := false
	if boardID == "" {
		b, err := s.boards.Create(s.freeName())
		if err != nil {
			return board.Summary{}, err
		}
		boardID, made = b.ID, true
	}
	if _, err := s.boards.Edit(boardID, func(b board.Board) (board.Op, error) {
		return imageBlockOp(b, img, label)
	}); err != nil {
		if made {
			_ = s.boards.Delete(boardID)
		}
		return board.Summary{}, err
	}
	if made {
		s.changed()
	}
	b, err := s.boards.Load(boardID)
	if err != nil {
		return board.Summary{}, err
	}
	s.Open(boardID)
	return summary(b), nil
}

// imageBlockOp is the person's change that puts img on b as an image
// block, at most sentImageWidth wide, below everything on the board.
func imageBlockOp(b board.Board, img board.Image, alt string) (board.Op, error) {
	bt, ok := board.LookupType("image")
	if !ok {
		return board.Op{}, errors.New("no image block in the catalog")
	}
	w, h := bt.W, bt.H
	if img.Width > 0 && img.Height > 0 {
		w = min(sentImageWidth, float64(img.Width))
		h = w * float64(img.Height) / float64(img.Width)
	}
	p := map[string]string{"asset": img.Asset}
	if alt = strings.TrimSpace(alt); alt != "" {
		p["alt"] = alt
	}
	props, err := json.Marshal(p)
	if err != nil {
		return board.Op{}, err
	}
	if err := board.CheckProps("image", props); err != nil {
		return board.Op{}, err
	}
	taken := itemIDs(b)
	id := board.RandomID("blk")
	for taken[id] {
		id = board.RandomID("blk")
	}
	x, y := newPlacer(b).next(w, h)
	return board.Op{
		Source: board.Person,
		Blocks: []board.Block{{ID: id, Type: "image", X: x, Y: y, W: w, H: h, Props: props}},
	}, nil
}

// freeName is "Untitled whiteboard", or "Untitled whiteboard 2" and on when
// that name is taken.
func (s *WhiteboardsService) freeName() string {
	used := map[string]bool{}
	if list, err := s.boards.List(); err == nil {
		for _, b := range list {
			used[b.Name] = true
		}
	}
	name := newBoardName
	for n := 2; used[name]; n++ {
		name = fmt.Sprintf("%s %d", newBoardName, n)
	}
	return name
}

// changed tells windows a board was made, renamed or deleted
// (boards:changed), as the agents' board.create and board.delete do.
func (s *WhiteboardsService) changed() {
	if s.emit != nil {
		s.emit(evBoardsChanged, nil)
	}
}

func summary(b board.Board) board.Summary {
	return board.Summary{ID: b.ID, Name: b.Name, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt, Blocks: len(b.Blocks)}
}
