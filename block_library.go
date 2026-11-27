package main

import (
	"fmt"

	"github.com/StephenSHorton/toru/internal/board"
)

// BlockLibraryService is the board window's way to the person's own block
// library (whiteboard#27): the custom block types agents designed with
// board.block.define. The window lists them in its gallery, copies one onto
// the board with the op that places it, and saves a block type a board
// carries (from someone else's board) to the library. Every change to the
// library is sent as blocks:changed.
type BlockLibraryService struct {
	library *board.Library
	boards  *board.Store
}

// List is every block type in the library, by label, and any file that
// can't be read.
func (s *BlockLibraryService) List() (board.LibraryList, error) { return s.library.List() }

// SaveFromBoard keeps the custom block type a board carries in the
// person's library. as, when set, saves it under that name instead (for
// when the library already has a different block with the same name). It
// returns the block as the library has it.
func (s *BlockLibraryService) SaveFromBoard(boardID, blockType, as string) (board.BlockDef, error) {
	b, err := s.boards.Load(boardID)
	if err != nil {
		return board.BlockDef{}, err
	}
	def := b.BoardType(blockType)
	if def == nil {
		return board.BlockDef{}, fmt.Errorf("this board carries no custom block type %q", blockType)
	}
	saved, _, err := s.library.Save(*def, as)
	return saved, err
}
