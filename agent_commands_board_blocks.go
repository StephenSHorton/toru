package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// agent_commands_board_blocks.go: custom block types (whiteboard#27).
// Agents design new blocks with board.block.define; each is kept in the
// person's own block library (%AppData%\toru\blocks), listed by
// board.catalog and in the board window's gallery, and copied into every
// board that uses it so the board renders on any Toru.
//
// A definition is data only: a props schema and a layout tree of the parts
// in internal/board/blockparts.json. Toru draws each part itself; nothing
// an agent sends is ever run as markup or code.

// Library events.
const evBlocksChanged = "blocks:changed" // the block library changed

// typeResolver works out, for board.add, what each block type is: built in,
// carried by the board, or from the person's library (then copied onto the
// board with the op).
type typeResolver struct {
	b     board.Board
	lib   *board.Library
	added []board.BlockDef
	seen  map[string]resolvedType
}

type resolvedType struct {
	w, h  float64
	def   *board.BlockDef // nil for a built-in type
	check func(json.RawMessage) error
}

func newTypeResolver(b board.Board, lib *board.Library) *typeResolver {
	return &typeResolver{b: b, lib: lib, seen: map[string]resolvedType{}}
}

func (r *typeResolver) resolve(t string) (resolvedType, error) {
	if rt, ok := r.seen[t]; ok {
		return rt, nil
	}
	rt, err := r.find(t)
	if err == nil {
		r.seen[t] = rt
	}
	return rt, err
}

func (r *typeResolver) find(t string) (resolvedType, error) {
	if bt, ok := board.LookupType(t); ok {
		if bt.Region {
			return resolvedType{}, board.CheckProps(t, json.RawMessage(`{}`))
		}
		return resolvedType{w: bt.W, h: bt.H, check: func(p json.RawMessage) error { return board.CheckProps(t, p) }}, nil
	}
	use := func(d board.BlockDef, copyIn bool) resolvedType {
		if copyIn {
			r.added = append(r.added, d)
		}
		dd := d
		return resolvedType{w: d.W, h: d.H, def: &dd, check: dd.CheckProps}
	}
	var mine *board.BlockDef
	if r.lib != nil {
		if d, err := r.lib.Get(t); err == nil {
			mine = &d
		}
	}
	if have := r.b.BoardType(t); have != nil {
		// The board's own copy wins, unless the library has a newer version
		// of the same block that every block of the type already on the
		// board still fits.
		if mine != nil && mine.Origin == have.Origin && mine.Version > have.Version && r.fitsAll(t, mine) {
			return use(*mine, true), nil
		}
		return use(*have, false), nil
	}
	if mine != nil {
		return use(*mine, true), nil
	}
	return resolvedType{}, fmt.Errorf("no block type %q: board.catalog lists the built-in types, your own blocks and the board's; make a new one with board.block.define", t)
}

// fitsAll reports whether every block of type t on the board passes d.
func (r *typeResolver) fitsAll(t string, d *board.BlockDef) bool {
	for _, x := range r.b.Blocks {
		if x.Type == t && d.CheckProps(x.Props) != nil {
			return false
		}
	}
	return true
}

// withDefaults is a custom block's props: its type's defaults with the
// props sent laid over them, key by key.
func withDefaults(defaults, props json.RawMessage) (json.RawMessage, error) {
	if len(props) == 0 || string(props) == "null" {
		return append(json.RawMessage(nil), defaults...), nil
	}
	return mergeProps(defaults, props, false)
}

func orEmptyDefs(s []board.BlockDef) []board.BlockDef {
	if s == nil {
		return []board.BlockDef{}
	}
	return s
}

// blockDefIn is what board.block.define and update take.
type blockDefIn struct {
	Type        string          `json:"type"`
	Label       *string         `json:"label"`
	Description *string         `json:"description"`
	Icon        *string         `json:"icon"`
	W           *float64        `json:"w"`
	H           *float64        `json:"h"`
	Props       json.RawMessage `json:"props"`
	Defaults    json.RawMessage `json:"defaults"`
	Layout      json.RawMessage `json:"layout"`
	NameProp    *string         `json:"nameProp"`
}

func (in blockDefIn) apply(d *board.BlockDef) {
	if in.Label != nil {
		d.Label = strings.TrimSpace(*in.Label)
	}
	if in.Description != nil {
		d.Description = strings.TrimSpace(*in.Description)
	}
	if in.Icon != nil {
		d.Icon = *in.Icon
	}
	setF(&d.W, in.W)
	setF(&d.H, in.H)
	if len(in.Props) > 0 && string(in.Props) != "null" {
		d.Props = in.Props
	}
	if len(in.Defaults) > 0 && string(in.Defaults) != "null" {
		d.Defaults = in.Defaults
	}
	if len(in.Layout) > 0 && string(in.Layout) != "null" {
		d.Layout = in.Layout
	}
	if in.NameProp != nil {
		d.NameProp = *in.NameProp
	}
}

func registerBoardBlockCommands(reg *agentctl.Registry, d boardAgentDeps) {
	if d.blocks == nil {
		return
	}
	lib := d.blocks
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	defFields := func() map[string]any {
		return map[string]any{
			"type":        map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{0,39}$", "description": "The type's name, like feature-idea: lower-case letters, digits and -. Not a built-in type's name."},
			"label":       map[string]any{"type": "string", "minLength": 1, "maxLength": 60, "description": "What people call it, like Feature idea. Shown in the gallery and on each block's name."},
			"description": map[string]any{"type": "string", "maxLength": 500, "description": "What it's for, one or two sentences."},
			"icon":        map[string]any{"type": "string", "description": "An icon name from board.catalog's icons, like lightbulb or bug."},
			"w":           map[string]any{"type": "number", "minimum": 40, "maximum": 4000, "description": "Default width, world units."},
			"h":           map[string]any{"type": "number", "minimum": 40, "maximum": 4000, "description": "Default height."},
			"props": map[string]any{"type": "object", "description": "The props as JSON Schema: {\"type\": \"object\", \"additionalProperties\": false, \"properties\": {...}, \"required\": [...]}, " +
				"using only type, properties, required, additionalProperties, items, enum, minLength, maxLength, minItems, maxItems, minimum, maximum and description. " +
				"Each layout part binds to a prop of a kind it takes (board.catalog's propKinds)."},
			"defaults": map[string]any{"type": "object", "description": "A new block's props. They must pass the props schema."},
			"layout": map[string]any{"type": "object", "description": "What the block shows: a tree of parts from board.catalog's parts, like " +
				`{"part": "stack", "children": [{"part": "field", "prop": "title", "label": "Title"}, {"part": "counter", "prop": "votes", "label": "Vote", "icon": "thumbs-up"}]}. ` +
				"Data only: no HTML, script or style."},
			"nameProp": str("The text prop that names each block, like title. Default: the first required text prop."),
		}
	}

	reg.Register(agentctl.Command{
		Name: "board.block.define",
		Description: "Design a new block type and keep it in the person's block library, so they can place it on any board later (it shows in board.catalog's custom list " +
			"and the board window's gallery under their own blocks). A block type is data only: a props schema, defaults, and a layout built from Toru's parts " +
			"(text, fields, checkboxes, choices, counters, sliders, ratings, checklists, lists, tags, polls, tables, images), each bound to a prop. " +
			"Read board.catalog first for the parts, prop kinds and icons. Place one with board.add and its type name; the board keeps a copy, so it shows on other Toru installs and in exports.",
		Input: schema(defFields(), "type", "label", "icon", "w", "h", "props", "defaults", "layout"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[blockDefIn](in)
			if err != nil {
				return nil, err
			}
			def := board.BlockDef{Type: v.Type}
			v.apply(&def)
			saved, err := lib.Define(def)
			if err != nil {
				return nil, err
			}
			return map[string]any{"type": saved.Type, "version": saved.Version, "block": saved}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.block.list",
		Description: "The person's own block types (their block library), each in full: label, icon, size, props schema, defaults, layout, version. Files that can't be read are listed under broken.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			l, err := lib.List()
			if err != nil {
				return nil, err
			}
			return l, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.block.update",
		Description: "Change one of the person's block types: send its type and only the fields to change. It is saved as the next version. " +
			"Boards that use it keep their copy; board.add brings a board up to the new version when every block of the type on it still fits the new props schema.",
		Input: schema(defFields(), "type"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[blockDefIn](in)
			if err != nil {
				return nil, err
			}
			saved, err := lib.Update(v.Type, func(d *board.BlockDef) error {
				v.apply(d)
				return nil
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"type": saved.Type, "version": saved.Version, "block": saved}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "board.block.remove",
		Description: "Destructive. Take a block type out of the person's block library for good. Boards that use it keep their own copy and still show it. Needs confirm: true.",
		Destructive: true,
		Input: schema(map[string]any{
			"type":    str("The block type's name."),
			"confirm": propConfirm,
		}, "type", "confirm"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Type string `json:"type"`
			}](in)
			if err != nil {
				return nil, err
			}
			if err := lib.Remove(v.Type); err != nil {
				return nil, err
			}
			return map[string]any{"removed": v.Type}, nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "board.block.save",
		Description: "Save a custom block type a board carries (from someone else's board, a shared board or an import) into the person's own block library, " +
			"so they can use it on their boards. as saves it under another name, when their library already has a different block with that name.",
		Input: schema(map[string]any{
			"id":   map[string]any{"type": "string", "description": "The board's id."},
			"type": str("The custom block type's name on that board (board.catalog with id lists them as boardTypes)."),
			"as":   map[string]any{"type": "string", "pattern": "^[a-z][a-z0-9-]{0,39}$", "description": "Optional: save it under this name instead."},
		}, "id", "type"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID   string `json:"id"`
				Type string `json:"type"`
				As   string `json:"as"`
			}](in)
			if err != nil {
				return nil, err
			}
			b, err := d.boards.Load(v.ID)
			if err != nil {
				return nil, err
			}
			def := b.BoardType(v.Type)
			if def == nil {
				return nil, fmt.Errorf("board %q carries no custom block type %q", v.ID, v.Type)
			}
			saved, changed, err := lib.Save(*def, v.As)
			if err != nil {
				return nil, err
			}
			return map[string]any{"type": saved.Type, "version": saved.Version, "saved": changed}, nil
		},
	})
}
