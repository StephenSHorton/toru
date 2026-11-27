package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/boardshare"
)

// agent_commands_board_share.go lets agents reach boards shared between
// Toru installs (docs/whiteboard.md section 2, "Shared boards"). Each
// person's agent talks only to its own Toru, over its own pipe: it lists
// the boards people in the room share and joins one, which gives this
// Toru a live copy. From then on every board command (board.add,
// board.update, board.draw...) works on the copy's id, and each change
// goes to the board's owner, tagged with the agent's name and its
// person's.
//
// Sharing a board, which decides who can reach it, is the person's: only
// the board window's Share button does it. Agents can't share or unshare.

// boardSharer is what the commands use of *boardshare.ShareService.
type boardSharer interface {
	GetState() boardshare.State
	Join(host, board string) (string, error)
	Leave(id string) boardshare.BoardStatus
}

func registerBoardShareAgentCommands(reg *agentctl.Registry, share boardSharer, open func(id string)) {
	reg.Register(agentctl.Command{
		Name: "board.shared",
		Description: "Boards shared between Toru installs in this Toru's room (the room Call uses): the person's own boards they share (and who has each open), " +
			"and boards other people in the room share, with the id of this Toru's live copy once joined (\"local\"). " +
			"Join one with board.join, then build on its local id with the usual board commands.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return share.GetState(), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.join",
		Description: "Join a board someone else in the room shares (board.shared lists them): this Toru keeps a live copy, and returns its id. " +
			"Every board command then works on that id: changes go to the board's owner and theirs show here as they happen. " +
			"Give open: true to show it in a window for the person.",
		Input: schema(map[string]any{
			"board": map[string]any{"type": "string", "description": "The shared board's id, as board.shared lists it under offered."},
			"host":  map[string]any{"type": "string", "description": "Whose board it is (offered[].host); needed only when two people share boards with the same id."},
			"open":  map[string]any{"type": "boolean", "description": "Also open the board's window."},
		}, "board"),
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Board string `json:"board"`
				Host  string `json:"host"`
				Open  bool   `json:"open"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			if in.Board == "" {
				return nil, errors.New(`give "board" (board.shared lists the boards shared in the room)`)
			}
			id, err := share.Join(in.Host, in.Board)
			if err != nil {
				return nil, err
			}
			if in.Open && open != nil {
				open(id)
			}
			return map[string]any{"board": id}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.leave",
		Description: "Stop this Toru's copy of a shared board following its owner. The copy stays here as an ordinary board; nothing changes on the owner's.",
		Input: schema(map[string]any{
			"board": map[string]any{"type": "string", "description": "The copy's id (offered[].local in board.shared)."},
		}, "board"),
		Handler: func(_ context.Context, raw json.RawMessage) (any, error) {
			var in struct {
				Board string `json:"board"`
			}
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
			return share.Leave(in.Board), nil
		},
	})
}
