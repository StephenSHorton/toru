package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/history"
)

// agent_commands_board_images.go registers board.image.import: it copies a
// picture into the board images so an agent can place it as an image block
// (block catalog, internal/board/catalog.json).

// boardLibrary lists the Library's screenshots for image blocks: images
// only, newest first.
func boardLibrary(hist *history.Store) func() []board.LibraryItem {
	return func() []board.LibraryItem {
		out := []board.LibraryItem{}
		for _, it := range hist.List() {
			if it.Kind == history.KindImage {
				out = append(out, board.LibraryItem{ID: it.ID, Label: it.Label, Path: it.Path, TakenAt: it.TakenAt})
			}
		}
		return out
	}
}

func registerBoardImageAgentCommands(reg *agentctl.Registry, images *board.ImageService) {
	reg.Register(agentctl.Command{
		Name: "board.image.import",
		Description: "Copy a picture into the board images, from a file path or a Library screenshot (an id from library.list). " +
			"Returns its asset name and size: place it with board.add as an image block, props {\"asset\": <asset>, \"alt\": \"what it shows\"}. " +
			"PNG, JPEG, GIF or WebP, up to 32 MB.",
		Input: schema(map[string]any{
			"path":      map[string]any{"type": "string", "description": "A picture file's full path."},
			"libraryId": map[string]any{"type": "string", "description": "A screenshot's id from library.list."},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path      string `json:"path"`
				LibraryID string `json:"libraryId"`
			}](in)
			if err != nil {
				return nil, err
			}
			switch {
			case v.Path != "" && v.LibraryID != "":
				return nil, errors.New("give path or libraryId, not both")
			case v.Path != "":
				return board.ImportImageFile(v.Path)
			case v.LibraryID != "":
				return images.ImportLibrary(v.LibraryID)
			}
			return nil, errors.New("give path or libraryId")
		},
	})
}
