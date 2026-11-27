package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/boardexport"
)

// agent_commands_boards.go registers the whiteboard's board commands
// (docs/whiteboard.md section 2): agents build boards in Go, with the board
// window open or closed. Every change is one board op tagged
// "agent:<caller>" (the MCP client's name, or "toru-ctl"), so it goes
// through the board's undo history like a person's, and is sent to open
// board windows as a board:op event.
//
// The live controls (board.snapshot, board.act) are the board window's own
// commands and come with the live board card.

// boardStore is what the commands use of *board.Store.
type boardStore interface {
	List() ([]board.Summary, error)
	Create(name string) (board.Board, error)
	Load(id string) (board.Board, error)
	Edit(id string, f func(b board.Board) (board.Op, error)) (board.Op, error)
	Delete(id string) error
	ImportFile(path string) (board.Board, error)
}

type boardAgentDeps struct {
	boards boardStore
	// open shows a board's window (WindowsService.OpenWhiteboard).
	open func(id string)
	// emit tells windows a board was made or deleted (boards:changed).
	// Changes reach open board windows as board:op from the store itself
	// (boardOpEvent), so the window's own changes and agents' arrive in one
	// stream, in rev order.
	emit func(name string, data any)
	// exporter makes board.export's files (board_export.go). Nil: a plain
	// one, which makes the files that need no board viewer or WebView2
	// (toruboard, markdown).
	exporter *boardexport.Exporter
	// blocks is the person's block library (custom block types). Nil: no
	// library, so only built-in and board-carried types can be placed.
	blocks *board.Library
}

// Board events.
const (
	evBoardOp       = "board:op" // {board, op}: see boardOpEvent
	evBoardsChanged = "boards:changed"
)

// boardOpEvent is the board:op event's data: which board changed and the op
// as applied, which the board window applies to its copy by op.rev.
func boardOpEvent(id string, op board.Op) map[string]any {
	return map[string]any{"board": id, "op": op}
}

// Placement of blocks an agent sends without x and y.
const (
	autoGap    = 40.0
	autoPerRow = 4
	maxPerOp   = 500 // blocks, frames, links or moves in one call
)

func registerBoardAgentCommands(reg *agentctl.Registry, d boardAgentDeps) {
	emit := func(name string, data any) {
		if d.emit != nil {
			d.emit(name, data)
		}
	}
	// change runs one op worked out from the board, as the caller.
	change := func(ctx context.Context, id string, f func(b board.Board, src board.Source) (board.Op, error)) (board.Op, error) {
		src := board.AgentNamed(agentctl.Caller(ctx))
		op, err := d.boards.Edit(id, func(b board.Board) (board.Op, error) {
			op, err := f(b, src)
			op.Source = src
			return op, err
		})
		if err != nil {
			return board.Op{}, err
		}
		// The store sends the op to open windows (board:op, wired in
		// main.go with the store's OnOp), in rev order.
		return op, nil
	}

	propBoard := map[string]any{"type": "string", "description": "The board's id, from board.list or board.create."}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	numP := func(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }
	ids := func(desc string) map[string]any {
		return map[string]any{"type": "array", "minItems": 1, "maxItems": maxPerOp, "items": map[string]any{"type": "string"}, "description": desc}
	}
	list := func(item map[string]any, desc string) map[string]any {
		return map[string]any{"type": "array", "minItems": 1, "maxItems": maxPerOp, "items": item, "description": desc}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	propProps := map[string]any{"type": "object", "description": "The block's props, as its type's schema in board.catalog says."}
	frameIn := obj(map[string]any{
		"id":    str("Optional id of your own (letters, digits, - and _), so blocks in this call can sit in the frame."),
		"name":  str("The frame's name, shown as its heading."),
		"x":     numP("Left edge, world units (CSS pixels at zoom 1)."),
		"y":     numP("Top edge."),
		"w":     map[string]any{"type": "number", "minimum": 0},
		"h":     map[string]any{"type": "number", "minimum": 0},
		"color": str("Optional colour."),
	}, "name", "x", "y", "w", "h")

	reg.Register(agentctl.Command{
		Name:        "board.list",
		Description: "List the whiteboards: id, name, when made and changed, and how many blocks each has. Most recently changed first.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			boards, err := d.boards.List()
			if err != nil {
				return nil, err
			}
			return map[string]any{"boards": boards}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.create",
		Description: "Make a new whiteboard: empty, or ready to fill from a brainstorm template (board.templates lists them; the board's name heads it). " +
			"Returns its id. Give open: true to show it in a window so the person can watch it fill.",
		Input: schema(map[string]any{
			"name":     map[string]any{"type": "string", "minLength": 1, "maxLength": board.MaxNameLen},
			"template": map[string]any{"type": "string", "enum": board.TemplateNames(), "description": "Start from this template instead of an empty board."},
			"open":     map[string]any{"type": "boolean", "description": "Also open the board's window."},
		}, "name"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Name     string `json:"name"`
				Template string `json:"template"`
				Open     bool   `json:"open"`
			}](in)
			if err != nil {
				return nil, err
			}
			var tplOp board.Op
			if v.Template != "" {
				// Checked before the board is made, so a bad name makes nothing.
				if tplOp, err = board.TemplateOp(v.Template, v.Name); err != nil {
					return nil, err
				}
			}
			b, err := d.boards.Create(v.Name)
			if err != nil {
				return nil, err
			}
			blocks := 0
			if v.Template != "" {
				// The template goes on as the caller's first change, so it is
				// theirs in who-did-what and can be undone like any other.
				if _, err := change(ctx, b.ID, func(board.Board, board.Source) (board.Op, error) { return tplOp, nil }); err != nil {
					_ = d.boards.Delete(b.ID)
					return nil, fmt.Errorf("template %s: %w", v.Template, err)
				}
				blocks = len(tplOp.Blocks)
			}
			emit(evBoardsChanged, nil)
			if v.Open && d.open != nil {
				d.open(b.ID)
			}
			out := map[string]any{"id": b.ID, "name": b.Name, "opened": v.Open && d.open != nil}
			if v.Template != "" {
				out["template"], out["blocks"] = v.Template, blocks
			}
			return out, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.templates",
		Description: "The brainstorm templates board.create can start a board from: name, title and what each lays out.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"templates": board.Templates()}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.open",
		Description: "Show a whiteboard in its window (brings it forward if it's already open), so the person sees each change as it lands.",
		Input:       schema(map[string]any{"id": propBoard}, "id"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID string `json:"id"`
			}](in)
			if err != nil {
				return nil, err
			}
			if _, err := d.boards.Load(v.ID); err != nil {
				return nil, err
			}
			if d.open == nil {
				return nil, errors.New("board windows can't be opened right now")
			}
			d.open(v.ID)
			return map[string]any{"opened": v.ID}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.read",
		Description: "Read a whiteboard: format json (default) is the whole board document (frames, blocks with props, links, ink, who made and last changed each), " +
			"format markdown is an outline for reading (frames as headings, blocks as bullets, links as A → B lines).",
		Input: schema(map[string]any{
			"id":     propBoard,
			"format": map[string]any{"type": "string", "enum": []string{"json", "markdown"}},
		}, "id"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID     string `json:"id"`
				Format string `json:"format"`
			}](in)
			if err != nil {
				return nil, err
			}
			b, err := d.boards.Load(v.ID)
			if err != nil {
				return nil, err
			}
			switch v.Format {
			case "", "json":
				return map[string]any{"board": b}, nil
			case "markdown":
				return map[string]any{"markdown": board.Markdown(b)}, nil
			}
			return nil, fmt.Errorf("no format %q (use json or markdown)", v.Format)
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.catalog",
		Description: "Every block type you can place with board.add: types are Toru's built-in ones (type name, what it is, default size, props as JSON Schema); " +
			"custom are the person's own blocks from their block library (board.block.define makes them), each with its props schema, defaults and layout. " +
			"Give id to also list the custom types that board carries (boardTypes), which may have come from someone else. " +
			"parts and icons are what a custom block's layout may use.",
		Input: schema(map[string]any{"id": map[string]any{"type": "string", "description": "Optional: a board's id, to list the custom types it carries too."}}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			var v struct {
				ID string `json:"id"`
			}
			if t := strings.TrimSpace(string(in)); t != "" && t != "null" {
				if err := json.Unmarshal(in, &v); err != nil {
					return nil, fmt.Errorf("bad input: %w", err)
				}
			}
			out := map[string]any{"types": board.Catalog(), "custom": []board.BlockDef{}}
			if d.blocks != nil {
				lib, err := d.blocks.List()
				if err != nil {
					return nil, err
				}
				out["custom"] = lib.Blocks
				if len(lib.Broken) > 0 {
					out["brokenCustom"] = lib.Broken
				}
			}
			if v.ID != "" {
				b, err := d.boards.Load(v.ID)
				if err != nil {
					return nil, err
				}
				out["boardTypes"] = orEmptyDefs(b.Types)
			}
			var parts map[string]json.RawMessage
			_ = json.Unmarshal(board.BlockParts(), &parts)
			out["parts"], out["icons"], out["propKinds"] = parts["parts"], parts["icons"], parts["propKinds"]
			return out, nil
		},
	})

	blockIn := obj(map[string]any{
		"id":       str("Optional id of your own (letters, digits, - and _), to refer to the block later in this or another call. One is made if left out."),
		"type":     str("A block type from board.catalog: a built-in type, one of the person's own blocks, or a custom type the board carries."),
		"props":    map[string]any{"type": "object", "description": "The block's props, as its type's schema in board.catalog says. For a custom block, its defaults fill in the props you leave out."},
		"x":        numP("Left edge. Leave x and y out to place the block below what's on the board."),
		"y":        numP("Top edge."),
		"w":        map[string]any{"type": "number", "minimum": 0, "description": "Default: the type's size."},
		"h":        map[string]any{"type": "number", "minimum": 0},
		"rotation": numP("Degrees, clockwise."),
		"frame":    str("The id of a frame to sit in."),
		"group":    str("A group id: blocks with the same group move together."),
	}, "type")

	reg.Register(agentctl.Command{
		Name: "board.add",
		Description: "Place blocks (and frames) on a whiteboard in one change. Blocks without x and y are laid out in rows below what's there. " +
			"Returns the new ids in the order sent, and the board's rev.",
		Input: schema(map[string]any{
			"id":     propBoard,
			"blocks": list(blockIn, "The blocks to place."),
			"frames": list(frameIn, "Frames (named regions) to make first."),
		}, "id"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID     string `json:"id"`
				Blocks []struct {
					ID       string          `json:"id"`
					Type     string          `json:"type"`
					Props    json.RawMessage `json:"props"`
					X, Y     *float64
					W, H     float64
					Rotation float64 `json:"rotation"`
					Frame    string  `json:"frame"`
					Group    string  `json:"group"`
				} `json:"blocks"`
				Frames []board.Frame `json:"frames"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Blocks) == 0 && len(v.Frames) == 0 {
				return nil, errors.New(`give "blocks" or "frames" to add`)
			}
			var blockIDs, frameIDs []string
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				taken := itemIDs(b)
				newID := func(given, prefix, what string) (string, error) {
					if given == "" {
						for {
							id := board.RandomID(prefix)
							if !taken[id] {
								taken[id] = true
								return id, nil
							}
						}
					}
					if !board.ValidID(given) {
						return "", fmt.Errorf("bad %s id %q (letters, digits, - and _)", what, given)
					}
					if taken[given] {
						return "", fmt.Errorf("%q is already on the board: change it with board.update", given)
					}
					taken[given] = true
					return given, nil
				}
				var op board.Op
				for _, f := range v.Frames {
					id, err := newID(f.ID, "frm", "frame")
					if err != nil {
						return op, err
					}
					f.ID, f.Meta = id, board.Meta{}
					f.Name = strings.TrimSpace(f.Name)
					op.Frames = append(op.Frames, f)
					frameIDs = append(frameIDs, id)
				}
				var auto []int // blocks sent without x and y
				types := newTypeResolver(b, d.blocks)
				for i, x := range v.Blocks {
					rt, err := types.resolve(x.Type)
					if err != nil {
						return op, err
					}
					props := x.Props
					if rt.def != nil {
						if props, err = withDefaults(rt.def.Defaults, x.Props); err != nil {
							return op, fmt.Errorf("blocks[%d] (%s): %w", i, x.Type, err)
						}
					}
					if err := rt.check(props); err != nil {
						return op, fmt.Errorf("blocks[%d] (%s): %w", i, x.Type, err)
					}
					id, err := newID(x.ID, "blk", "block")
					if err != nil {
						return op, err
					}
					w, h := x.W, x.H
					if w == 0 {
						w = rt.w
					}
					if h == 0 {
						h = rt.h
					}
					blk := board.Block{ID: id, Type: x.Type, W: w, H: h, Rotation: x.Rotation, Frame: x.Frame, Group: x.Group, Props: props}
					if x.X != nil && x.Y != nil {
						blk.X, blk.Y = *x.X, *x.Y
					} else {
						auto = append(auto, i)
					}
					op.Blocks = append(op.Blocks, blk)
					blockIDs = append(blockIDs, id)
				}
				if len(auto) > 0 {
					// Below everything: what's on the board, and the frames
					// and placed blocks of this call.
					seed := board.Board{Frames: append(append([]board.Frame{}, b.Frames...), op.Frames...)}
					seed.Blocks = append([]board.Block{}, b.Blocks...)
					isAuto := map[int]bool{}
					for _, i := range auto {
						isAuto[i] = true
					}
					for i, x := range op.Blocks {
						if !isAuto[i] {
							seed.Blocks = append(seed.Blocks, x)
						}
					}
					place := newPlacer(seed)
					for _, i := range auto {
						x := &op.Blocks[i]
						x.X, x.Y = place.next(x.W, x.H)
					}
				}
				op.Types = types.added
				return op, nil
			})
			if err != nil {
				return nil, err
			}
			out := map[string]any{"rev": op.Rev, "blocks": orEmpty(blockIDs)}
			if len(op.Types) > 0 {
				var copied []string
				for _, t := range op.Types {
					copied = append(copied, fmt.Sprintf("%s v%d", t.Type, t.Version))
				}
				out["typesCopied"] = copied
			}
			if len(frameIDs) > 0 {
				out["frames"] = frameIDs
			}
			return out, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.update",
		Description: "Change blocks and frames on a whiteboard in one change, and rename the board. A block's props are merged key by key " +
			"(a key set to null is removed) unless replaceProps is true. To move blocks use board.move.",
		Input: schema(map[string]any{
			"id":   propBoard,
			"name": map[string]any{"type": "string", "minLength": 1, "maxLength": board.MaxNameLen, "description": "A new name for the board."},
			"blocks": list(obj(map[string]any{
				"id":           str("The block's id."),
				"props":        propProps,
				"replaceProps": map[string]any{"type": "boolean", "description": "Replace the props whole instead of merging."},
				"w":            map[string]any{"type": "number", "minimum": 0},
				"h":            map[string]any{"type": "number", "minimum": 0},
				"rotation":     numP("Degrees, clockwise."),
				"frame":        str("A frame id to sit in, or \"\" to leave its frame."),
				"group":        str("A group id, or \"\" to leave its group."),
				"locked":       map[string]any{"type": "boolean", "description": "Lock it: then no agent can move, resize, delete or unlock it, and only the person unlocks it, in the board window. Its text and style can still change."},
			}, "id"), "Blocks to change."),
			"frames": list(obj(map[string]any{
				"id":     str("The frame's id."),
				"name":   map[string]any{"type": "string", "minLength": 1, "maxLength": board.MaxNameLen},
				"w":      map[string]any{"type": "number", "minimum": 0},
				"h":      map[string]any{"type": "number", "minimum": 0},
				"color":  str("A colour."),
				"locked": map[string]any{"type": "boolean", "description": "Lock it: then no agent can move, resize, delete or unlock it, and only the person unlocks it, in the board window. Its text and style can still change."},
			}, "id"), "Frames to change."),
		}, "id"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID     string  `json:"id"`
				Name   *string `json:"name"`
				Blocks []struct {
					ID           string          `json:"id"`
					Props        json.RawMessage `json:"props"`
					ReplaceProps bool            `json:"replaceProps"`
					W, H         *float64
					Rotation     *float64 `json:"rotation"`
					Frame        *string  `json:"frame"`
					Group        *string  `json:"group"`
					Locked       *bool    `json:"locked"`
				} `json:"blocks"`
				Frames []struct {
					ID     string  `json:"id"`
					Name   *string `json:"name"`
					W, H   *float64
					Color  *string `json:"color"`
					Locked *bool   `json:"locked"`
				} `json:"frames"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.Name == nil && len(v.Blocks) == 0 && len(v.Frames) == 0 {
				return nil, errors.New(`give "name", "blocks" or "frames" to change`)
			}
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				var op board.Op
				op.Name = v.Name
				blocks, frames := blockIndex(b), frameIndex(b)
				for _, u := range v.Blocks {
					x, ok := blocks[u.ID]
					if !ok {
						return op, fmt.Errorf("no block %q on the board", u.ID)
					}
					if len(u.Props) > 0 && string(u.Props) != "null" {
						p, err := mergeProps(x.Props, u.Props, u.ReplaceProps)
						if err != nil {
							return op, fmt.Errorf("block %q: %w", u.ID, err)
						}
						if err := board.CheckBlockProps(&b, x.Type, p); err != nil {
							return op, fmt.Errorf("block %q (%s): %w", u.ID, x.Type, err)
						}
						x.Props = p
					}
					setF(&x.W, u.W)
					setF(&x.H, u.H)
					setF(&x.Rotation, u.Rotation)
					if u.Frame != nil {
						x.Frame = *u.Frame
					}
					if u.Group != nil {
						x.Group = *u.Group
					}
					if u.Locked != nil {
						x.Locked = *u.Locked
					}
					op.Blocks = append(op.Blocks, x)
				}
				for _, u := range v.Frames {
					f, ok := frames[u.ID]
					if !ok {
						return op, fmt.Errorf("no frame %q on the board", u.ID)
					}
					if u.Name != nil {
						f.Name = strings.TrimSpace(*u.Name)
					}
					setF(&f.W, u.W)
					setF(&f.H, u.H)
					if u.Color != nil {
						f.Color = *u.Color
					}
					if u.Locked != nil {
						f.Locked = *u.Locked
					}
					op.Frames = append(op.Frames, f)
				}
				return op, nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": op.Rev}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.move",
		Description: "Move blocks and frames in one change: to x, y, or by dx, dy. A block in a group takes its group with it; a frame takes the blocks and ink in it. " +
			"z: front or back puts the item at the top or bottom of the stack. A locked item (board.read shows locked) can't be moved, nor a frame or group that would carry one.",
		Input: schema(map[string]any{
			"id": propBoard,
			"moves": list(obj(map[string]any{
				"id": str("A block's or frame's id."),
				"x":  numP("New left edge (with y)."),
				"y":  numP("New top edge (with x)."),
				"dx": numP("Move right by (with dy)."),
				"dy": numP("Move down by."),
				"z":  map[string]any{"type": "string", "enum": []string{"front", "back"}},
			}, "id"), "What to move."),
		}, "id", "moves"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID    string `json:"id"`
				Moves []struct {
					ID     string `json:"id"`
					X, Y   *float64
					DX, DY *float64
					Z      string `json:"z"`
				} `json:"moves"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Moves) == 0 {
				return nil, errors.New(`give "moves"`)
			}
			moved := 0
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				m := newMover(b)
				for _, mv := range v.Moves {
					var dx, dy float64
					cx, cy, kind, ok := m.pos(mv.ID)
					if !ok {
						return board.Op{}, fmt.Errorf("no block or frame %q on the board", mv.ID)
					}
					switch {
					case mv.X != nil && mv.Y != nil:
						dx, dy = *mv.X-cx, *mv.Y-cy
					case mv.DX != nil || mv.DY != nil:
						dx, dy = deref(mv.DX), deref(mv.DY)
					case mv.Z == "":
						return board.Op{}, fmt.Errorf("move %q: give x and y, dx and dy, or z", mv.ID)
					}
					if err := m.move(mv.ID, kind, dx, dy, mv.Z); err != nil {
						return board.Op{}, err
					}
				}
				op := m.op()
				moved = len(op.Blocks) + len(op.Frames) + len(op.Ink)
				return op, nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": op.Rev, "moved": moved}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.group",
		Description: "Group blocks so they move together (a new group id is made unless you give one), ungroup them, " +
			"or put them in a frame (frame: a frame's id, or \"\" to take them out of their frame).",
		Input: schema(map[string]any{
			"id":      propBoard,
			"blocks":  ids("The blocks' ids."),
			"group":   str("A group id to use (letters, digits, - and _)."),
			"ungroup": map[string]any{"type": "boolean", "description": "Take the blocks out of their group."},
			"frame":   str("Put the blocks in this frame, or \"\" to take them out of their frame. Leaves their group alone."),
		}, "id", "blocks"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID      string   `json:"id"`
				Blocks  []string `json:"blocks"`
				Group   string   `json:"group"`
				Ungroup bool     `json:"ungroup"`
				Frame   *string  `json:"frame"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Blocks) == 0 {
				return nil, errors.New(`give "blocks"`)
			}
			if v.Ungroup && v.Group != "" {
				return nil, errors.New(`give "group" or "ungroup", not both`)
			}
			group := v.Group
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				var op board.Op
				blocks := blockIndex(b)
				if v.Frame == nil {
					switch {
					case v.Ungroup:
						group = ""
					case group == "":
						taken := itemIDs(b)
						for _, x := range b.Blocks {
							taken[x.Group] = true
						}
						for group = board.RandomID("grp"); taken[group]; group = board.RandomID("grp") {
						}
					case !board.ValidID(group):
						return op, fmt.Errorf("bad group id %q (letters, digits, - and _)", group)
					}
				}
				for _, id := range v.Blocks {
					x, ok := blocks[id]
					if !ok {
						return op, fmt.Errorf("no block %q on the board", id)
					}
					if v.Frame != nil {
						x.Frame = *v.Frame
					} else {
						x.Group = group
					}
					op.Blocks = append(op.Blocks, x)
				}
				return op, nil
			})
			if err != nil {
				return nil, err
			}
			out := map[string]any{"rev": op.Rev}
			if v.Frame != nil {
				out["frame"] = *v.Frame
			} else {
				out["group"] = group
			}
			return out, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.connect",
		Description: "Draw lines between blocks or frames, in one change. Returns the new links' ids in the order sent.",
		Input: schema(map[string]any{
			"id": propBoard,
			"links": list(obj(map[string]any{
				"from":     str("A block's or frame's id."),
				"to":       str("A block's or frame's id."),
				"fromPort": str("Optional port on the from end (top, right, bottom, left)."),
				"toPort":   str("Optional port on the to end."),
				"label":    map[string]any{"type": "string", "maxLength": board.MaxLabelLen},
				"style":    map[string]any{"type": "object", "description": "Optional line style: start and end (\"arrow\" or \"none\"; end defaults to none), dash (\"solid\", \"dashed\", \"dotted\") and color (a block colour: yellow, pink, blue, green, purple, orange, grey)."},
			}, "from", "to"), "The lines to draw."),
		}, "id", "links"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID    string `json:"id"`
				Links []struct {
					From     string          `json:"from"`
					To       string          `json:"to"`
					FromPort string          `json:"fromPort"`
					ToPort   string          `json:"toPort"`
					Label    string          `json:"label"`
					Style    json.RawMessage `json:"style"`
				} `json:"links"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Links) == 0 {
				return nil, errors.New(`give "links"`)
			}
			var linkIDs []string
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				taken := itemIDs(b)
				var op board.Op
				for _, l := range v.Links {
					id := board.RandomID("lnk")
					for taken[id] {
						id = board.RandomID("lnk")
					}
					taken[id] = true
					op.Links = append(op.Links, board.Link{ID: id, From: l.From, To: l.To, FromPort: l.FromPort, ToPort: l.ToPort, Label: l.Label, Props: l.Style})
					linkIDs = append(linkIDs, id)
				}
				return op, nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": op.Rev, "links": linkIDs}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.disconnect",
		Description: "Remove lines: by their ids, or every line from one block to another (either direction with both: true). One change, so it can be undone.",
		Input: schema(map[string]any{
			"id":    propBoard,
			"links": ids("Link ids, from board.read or board.connect."),
			"from":  str("With to: remove the lines from this block or frame."),
			"to":    str("With from."),
			"both":  map[string]any{"type": "boolean", "description": "With from and to: lines in either direction."},
		}, "id"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID    string   `json:"id"`
				Links []string `json:"links"`
				From  string   `json:"from"`
				To    string   `json:"to"`
				Both  bool     `json:"both"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Links) == 0 && (v.From == "" || v.To == "") {
				return nil, errors.New(`give "links", or "from" and "to"`)
			}
			var removed []string
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				isLink := map[string]bool{}
				for _, l := range b.Links {
					isLink[l.ID] = true
				}
				removed = nil
				for _, id := range v.Links {
					if !isLink[id] {
						return board.Op{}, fmt.Errorf("no link %q on the board", id)
					}
					removed = append(removed, id)
				}
				if v.From != "" && v.To != "" {
					for _, l := range b.Links {
						if (l.From == v.From && l.To == v.To) || (v.Both && l.From == v.To && l.To == v.From) {
							removed = append(removed, l.ID)
						}
					}
				}
				removed = dedupe(removed)
				if len(removed) == 0 {
					return board.Op{}, fmt.Errorf("no line from %q to %q", v.From, v.To)
				}
				return board.Op{Delete: removed}, nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": op.Rev, "removed": removed}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.layout",
		Description: "Arrange blocks in one change: grid (rows and columns), tree (follows the lines: roots first, each level further right, or down), " +
			"or cluster (one column per tag: a block's first props.tags entry). Every block the person hasn't locked, unless blocks lists some (a locked one there is refused). They start at their top-left corner, or at x, y.",
		Input: schema(map[string]any{
			"id":        propBoard,
			"kind":      map[string]any{"type": "string", "enum": []string{board.LayoutGrid, board.LayoutTree, board.LayoutCluster}},
			"blocks":    ids("Only these blocks."),
			"gap":       map[string]any{"type": "number", "minimum": 0, "description": "Space between blocks (default 40)."},
			"columns":   map[string]any{"type": "integer", "minimum": 1, "description": "grid: blocks per row."},
			"direction": map[string]any{"type": "string", "enum": []string{"right", "down"}, "description": "tree: which way levels go (default right)."},
			"x":         numP("Where the layout starts (with y)."),
			"y":         numP("Where the layout starts (with x)."),
		}, "id", "kind"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID        string   `json:"id"`
				Kind      string   `json:"kind"`
				Blocks    []string `json:"blocks"`
				Gap       float64  `json:"gap"`
				Columns   int      `json:"columns"`
				Direction string   `json:"direction"`
				X, Y      *float64
			}](in)
			if err != nil {
				return nil, err
			}
			moved := 0
			op, err := change(ctx, v.ID, func(b board.Board, _ board.Source) (board.Op, error) {
				pos, err := board.Layout(b, v.Blocks, v.Kind, board.LayoutOptions{Gap: v.Gap, Columns: v.Columns, Direction: v.Direction, X: v.X, Y: v.Y})
				if err != nil {
					return board.Op{}, err
				}
				var op board.Op
				for _, x := range b.Blocks {
					p, ok := pos[x.ID]
					if ok && (p.X != x.X || p.Y != x.Y) {
						x.X, x.Y = p.X, p.Y
						op.Blocks = append(op.Blocks, x)
					}
				}
				moved = len(op.Blocks)
				if moved == 0 {
					return op, errors.New("the blocks are already laid out that way")
				}
				return op, nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": op.Rev, "moved": moved}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.delete",
		Description: "Destructive. With items: remove those blocks, frames, lines or ink from the board (lines to them go too; it can be undone in the board window). A locked item can't be removed. " +
			"Without items: delete the whole board and its file for good. Needs confirm: true.",
		Destructive: true,
		Input: schema(map[string]any{
			"id":      propBoard,
			"items":   ids("Ids of blocks, frames, links or strokes to remove. Leave out to delete the whole board."),
			"confirm": propConfirm,
		}, "id", "confirm"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID    string    `json:"id"`
				Items *[]string `json:"items"`
			}](in)
			if err != nil {
				return nil, err
			}
			// An empty list is a mistake, never "the whole board".
			if v.Items != nil && len(*v.Items) == 0 {
				return nil, errors.New(`"items" is empty: list what to remove, or leave "items" out to delete the whole board`)
			}
			if v.Items == nil {
				if err := d.boards.Delete(v.ID); err != nil {
					return nil, err
				}
				emit(evBoardsChanged, nil)
				return map[string]any{"deleted": v.ID}, nil
			}
			op, err := change(ctx, v.ID, func(board.Board, board.Source) (board.Op, error) {
				return board.Op{Delete: dedupe(*v.Items)}, nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": op.Rev, "removed": op.Delete}, nil
		},
	})

	exporter := d.exporter
	if exporter == nil {
		exporter = &boardexport.Exporter{}
	}
	reg.Register(agentctl.Command{
		Name: "board.export",
		Description: "Write a whiteboard to a file: toruboard (the board with its pictures inlined; board.import or Toru's Import reads it back), " +
			"png (the whole board drawn at 2×, however big), pdf (one vector page the board's size), html (one standalone file that opens offline in any browser and stays live: polls vote, checklists tick) " +
			"or markdown (the outline board.read gives). The board's window needn't be open. The extension is added if missing; an existing file is only replaced with confirm: true. Returns the path written.",
		Input: schema(map[string]any{
			"id":      propBoard,
			"format":  map[string]any{"type": "string", "enum": []string{"toruboard", "png", "pdf", "html", "markdown"}},
			"path":    str("Absolute output path."),
			"confirm": map[string]any{"type": "boolean", "description": "Replace the file if it exists."},
		}, "id", "format", "path"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID      string `json:"id"`
				Format  string `json:"format"`
				Path    string `json:"path"`
				Confirm bool   `json:"confirm"`
			}](in)
			if err != nil {
				return nil, err
			}
			if !filepath.IsAbs(v.Path) {
				return nil, errors.New(`"path" must be absolute`)
			}
			f, err := boardexport.ParseFormat(v.Format)
			if err != nil {
				return nil, err
			}
			b, err := d.boards.Load(v.ID)
			if err != nil {
				return nil, err
			}
			path := v.Path
			if !strings.EqualFold(filepath.Ext(path), f.Ext()) {
				path += f.Ext()
			}
			if _, err := os.Stat(path); err == nil && !v.Confirm {
				return nil, fmt.Errorf("%s already exists; send \"confirm\": true to replace it", path)
			}
			data, err := exporter.Export(ctx, b, f)
			if err != nil {
				return nil, err
			}
			if err := writeExport(path, data); err != nil {
				return nil, err
			}
			return map[string]any{"path": path, "bytes": len(data)}, nil
		},
	})

	registerBoardInkCommand(reg, d)
	registerBoardShapeCommand(reg, d)

	reg.Register(agentctl.Command{
		Name: "board.import",
		Description: "Read a .toruboard file (from board.export or Toru's Export) into Toru as a board, with its pictures. " +
			"It keeps the file's board id unless a board already has it, then gets a new one: an import never replaces a board. Returns the board's id and name.",
		Input: schema(map[string]any{
			"path": str("Absolute path of the .toruboard file."),
			"open": map[string]any{"type": "boolean", "description": "Also show the board's window."},
		}, "path"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Path string `json:"path"`
				Open bool   `json:"open"`
			}](in)
			if err != nil {
				return nil, err
			}
			if !filepath.IsAbs(v.Path) {
				return nil, errors.New(`"path" must be absolute`)
			}
			b, err := d.boards.ImportFile(v.Path)
			if err != nil {
				return nil, err
			}
			emit(evBoardsChanged, nil)
			if v.Open && d.open != nil {
				d.open(b.ID)
			}
			return map[string]any{"id": b.ID, "name": b.Name, "blocks": len(b.Blocks)}, nil
		},
	})

	registerBoardBlockCommands(reg, d) // agent_commands_board_blocks.go
}

// itemIDs is every id on b.
func itemIDs(b board.Board) map[string]bool {
	m := map[string]bool{}
	for _, x := range b.Frames {
		m[x.ID] = true
	}
	for _, x := range b.Blocks {
		m[x.ID] = true
	}
	for _, x := range b.Links {
		m[x.ID] = true
	}
	for _, x := range b.Ink {
		m[x.ID] = true
	}
	return m
}

func blockIndex(b board.Board) map[string]board.Block {
	m := make(map[string]board.Block, len(b.Blocks))
	for _, x := range b.Blocks {
		m[x.ID] = x
	}
	return m
}

func frameIndex(b board.Board) map[string]board.Frame {
	m := make(map[string]board.Frame, len(b.Frames))
	for _, x := range b.Frames {
		m[x.ID] = x
	}
	return m
}

func setF(dst *float64, v *float64) {
	if v != nil {
		*dst = *v
	}
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func dedupe(s []string) []string {
	seen := map[string]bool{}
	out := s[:0:0]
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// mergeProps is a block's props after an update: patch's keys replace old's
// (null removes a key), or patch whole with replace.
func mergeProps(old, patch json.RawMessage, replace bool) (json.RawMessage, error) {
	var p map[string]json.RawMessage
	if err := json.Unmarshal(patch, &p); err != nil || p == nil {
		return nil, errors.New("props must be a JSON object")
	}
	if replace {
		return patch, nil
	}
	m := map[string]json.RawMessage{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &m); err != nil {
			return nil, err
		}
	}
	for k, v := range p {
		if string(v) == "null" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return json.Marshal(m)
}

// placer puts blocks sent without a position in rows below the board's
// content, autoPerRow to a row.
type placer struct {
	x0, x, y, rowH float64
	n              int
}

func newPlacer(b board.Board) *placer {
	minX, maxY := math.Inf(1), math.Inf(-1)
	for _, x := range b.Blocks {
		minX, maxY = math.Min(minX, x.X), math.Max(maxY, x.Y+x.H)
	}
	for _, f := range b.Frames {
		minX, maxY = math.Min(minX, f.X), math.Max(maxY, f.Y+f.H)
	}
	p := &placer{}
	if !math.IsInf(minX, 1) {
		p.x0, p.y = minX, maxY+autoGap*2
	}
	p.x = p.x0
	return p
}

func (p *placer) next(w, h float64) (float64, float64) {
	if p.n > 0 && p.n%autoPerRow == 0 {
		p.x, p.y, p.rowH = p.x0, p.y+p.rowH+autoGap, 0
	}
	x, y := p.x, p.y
	p.x += w + autoGap
	p.rowH = math.Max(p.rowH, h)
	p.n++
	return x, y
}

// mover gathers the changes of a board.move into one op.
type mover struct {
	b                board.Board
	blocks           map[string]*board.Block
	frames           map[string]*board.Frame
	ink              map[string]*board.Stroke
	touched          []string
	seen, inkTouched map[string]bool
	z                map[string]string
}

func newMover(b board.Board) *mover {
	m := &mover{b: b, blocks: map[string]*board.Block{}, frames: map[string]*board.Frame{}, ink: map[string]*board.Stroke{},
		seen: map[string]bool{}, inkTouched: map[string]bool{}, z: map[string]string{}}
	for i := range b.Blocks {
		m.blocks[b.Blocks[i].ID] = &b.Blocks[i]
	}
	for i := range b.Frames {
		m.frames[b.Frames[i].ID] = &b.Frames[i]
	}
	for i := range b.Ink {
		m.ink[b.Ink[i].ID] = &b.Ink[i]
	}
	return m
}

func (m *mover) pos(id string) (x, y float64, kind string, ok bool) {
	if b, ok := m.blocks[id]; ok {
		return b.X, b.Y, "block", true
	}
	if f, ok := m.frames[id]; ok {
		return f.X, f.Y, "frame", true
	}
	return 0, 0, "", false
}

func (m *mover) touch(id string) {
	if !m.seen[id] {
		m.seen[id] = true
		m.touched = append(m.touched, id)
	}
}

func (m *mover) move(id, kind string, dx, dy float64, z string) error {
	if z != "" {
		m.z[id] = z
		m.touch(id)
	}
	if dx == 0 && dy == 0 {
		return nil
	}
	shiftBlock := func(x *board.Block) {
		x.X += dx
		x.Y += dy
		m.touch(x.ID)
	}
	switch kind {
	case "block":
		x := m.blocks[id]
		if x.Group == "" {
			shiftBlock(x)
			return nil
		}
		for _, o := range m.blocks {
			if o.Group == x.Group {
				shiftBlock(o)
			}
		}
	case "frame":
		f := m.frames[id]
		f.X += dx
		f.Y += dy
		m.touch(id)
		for _, o := range m.blocks {
			if o.Frame == id {
				shiftBlock(o)
			}
		}
		for _, s := range m.ink {
			if s.Frame == id {
				for i := 0; i+1 < len(s.Points); i += 3 {
					s.Points[i] += dx
					s.Points[i+1] += dy
				}
				m.inkTouched[s.ID] = true
				m.touch(s.ID)
			}
		}
	}
	return nil
}

func (m *mover) op() board.Op {
	var op board.Op
	top := map[string]int{"block": len(m.b.Blocks) - 1, "frame": len(m.b.Frames) - 1}
	for _, id := range m.touched {
		var kind string
		switch {
		case m.blocks[id] != nil:
			op.Blocks = append(op.Blocks, *m.blocks[id])
			kind = "block"
		case m.frames[id] != nil:
			op.Frames = append(op.Frames, *m.frames[id])
			kind = "frame"
		case m.ink[id] != nil:
			s := *m.ink[id]
			s.Points = append([]float64(nil), s.Points...)
			op.Ink = append(op.Ink, s)
		}
		if z, ok := m.z[id]; ok && kind != "" {
			if op.Pos == nil {
				op.Pos = map[string]int{}
			}
			if z == "front" {
				op.Pos[id] = top[kind]
			} else {
				op.Pos[id] = 0
			}
		}
	}
	return op
}
