package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// agent_commands_board_ink.go registers board.draw: agents put freehand ink
// on a board. The pen in the board window has no agent action (UI SDK's
// agent bridge leaves it out, as its parts are aria-hidden), so agents draw
// through Go like every other board change: one op tagged "agent:<caller>",
// in the board's undo history, sent to open windows as board:op.

// The pens, as the board window's toolbar has them (frontend
// src/whiteboard/ink.ts, inkColors and inkWidths; index.css --ink-*).
var (
	inkPens   = []string{"ink-white", "ink-blue", "ink-red", "ink-yellow", "ink-green", "ink-orange", "ink-purple", "ink-pink", "ink-grey"}
	inkWidths = map[string]float64{"fine": 2, "medium": 4, "bold": 8}
)

const defaultPressure = 0.5 // what the window's pen keeps when a device has none

// inkWidth reads a stroke's width: a number in world units, or a pen width
// token's name (fine, medium, bold, or ink-width-fine and so on). Left out,
// the fine pen.
func inkWidth(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return inkWidths["fine"], nil
	}
	var n float64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, errors.New(`"width" is a number or one of fine, medium, bold`)
	}
	if w, ok := inkWidths[strings.TrimPrefix(s, "ink-width-")]; ok {
		return w, nil
	}
	return 0, fmt.Errorf(`width %q isn't a pen width: give a number or one of fine, medium, bold`, s)
}

// inkPen checks a stroke's colour is one of the pens. Left out, white.
func inkPen(c string) (string, error) {
	if c == "" {
		return inkPens[0], nil
	}
	for _, p := range inkPens {
		if c == p || c == strings.TrimPrefix(p, "ink-") {
			return p, nil
		}
	}
	return "", fmt.Errorf("colour %q isn't a pen: one of %s", c, strings.Join(inkPens, ", "))
}

// inkPoints flattens [[x, y], [x, y, pressure], ...] into the board's x, y,
// pressure triples.
func inkPoints(pts [][]float64) ([]float64, error) {
	if len(pts) == 0 {
		return nil, errors.New(`a stroke needs "points"`)
	}
	if len(pts) > board.MaxStrokeFloats/3 {
		return nil, fmt.Errorf("a stroke has %d points at most", board.MaxStrokeFloats/3)
	}
	out := make([]float64, 0, 3*len(pts))
	for i, p := range pts {
		switch len(p) {
		case 2:
			out = append(out, p[0], p[1], defaultPressure)
		case 3:
			out = append(out, p[0], p[1], p[2])
		default:
			return nil, fmt.Errorf("point %d is [x, y] or [x, y, pressure]", i)
		}
	}
	return out, nil
}

func registerBoardInkCommand(reg *agentctl.Registry, d boardAgentDeps) {
	point := map[string]any{
		"type": "array", "minItems": 2, "maxItems": 3, "items": map[string]any{"type": "number"},
		"description": "[x, y] in world units (CSS pixels at zoom 1), or [x, y, pressure] with pressure 0..1 (0.5 if left out).",
	}
	stroke := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"points"},
		"properties": map[string]any{
			"points": map[string]any{"type": "array", "minItems": 1, "maxItems": board.MaxStrokeFloats / 3, "items": point,
				"description": "The line's points, in the order drawn."},
			"color": map[string]any{"type": "string", "enum": penNames(),
				"description": "The pen: a colour token, which follows the theme. Defaults to ink-white."},
			"width": map[string]any{"description": "The pen's width: fine, medium or bold (2, 4, 8), or a number of world units. Defaults to fine.",
				"oneOf": []any{map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 1000},
					map[string]any{"type": "string", "enum": []string{"fine", "medium", "bold", "ink-width-fine", "ink-width-medium", "ink-width-bold"}}}},
			"frame": map[string]any{"type": "string", "description": "The id of a frame for the stroke to sit in."},
			"highlight": map[string]any{"type": "boolean",
				"description": "A highlighter stroke: see-through and drawn under the other ink. Give it a wider width, like 16."},
		},
	}
	reg.Register(agentctl.Command{
		Name: "board.draw",
		Description: "Draw freehand ink on a whiteboard, as the pen does: each stroke is a line through its points in a pen colour and width. " +
			"All strokes go in one change, shown at once in an open board window. Returns the new strokes' ids in the order sent, and the board's rev. " +
			"Remove strokes with board.delete. For rectangles, ellipses, lines, arrows, text labels and emoji, use board.shape.",
		Input: schema(map[string]any{
			"id":      map[string]any{"type": "string", "description": "The board's id, from board.list or board.create."},
			"strokes": map[string]any{"type": "array", "minItems": 1, "maxItems": maxPerOp, "items": stroke, "description": "The strokes to draw."},
		}, "id", "strokes"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID      string `json:"id"`
				Strokes []struct {
					Points    [][]float64     `json:"points"`
					Color     string          `json:"color"`
					Width     json.RawMessage `json:"width"`
					Frame     string          `json:"frame"`
					Highlight bool            `json:"highlight"`
				} `json:"strokes"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Strokes) == 0 {
				return nil, errors.New(`give "strokes"`)
			}
			if len(v.Strokes) > maxPerOp {
				return nil, fmt.Errorf("%d strokes at most in one call", maxPerOp)
			}
			strokes := make([]board.Stroke, len(v.Strokes))
			for i, s := range v.Strokes {
				pts, err := inkPoints(s.Points)
				if err != nil {
					return nil, fmt.Errorf("strokes[%d]: %w", i, err)
				}
				color, err := inkPen(s.Color)
				if err != nil {
					return nil, fmt.Errorf("strokes[%d]: %w", i, err)
				}
				width, err := inkWidth(s.Width)
				if err != nil {
					return nil, fmt.Errorf("strokes[%d]: %w", i, err)
				}
				strokes[i] = board.Stroke{Points: pts, Color: color, Width: width, Frame: s.Frame, Highlight: s.Highlight}
			}
			rev, ids, err := addInk(ctx, d, v.ID, strokes)
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": rev, "strokes": ids}, nil
		},
	})
}

// penNames are the colours an agent may name: the pen tokens, with or
// without their "ink-".
func penNames() []string {
	out := append([]string{}, inkPens...)
	for _, p := range inkPens {
		out = append(out, strings.TrimPrefix(p, "ink-"))
	}
	return out
}

// addInk puts strokes on a board in one op under the calling agent's name,
// each with a new id. The store sends the op to open windows (board:op).
func addInk(ctx context.Context, d boardAgentDeps, boardID string, strokes []board.Stroke) (int, []string, error) {
	src := board.AgentNamed(agentctl.Caller(ctx))
	var ids []string
	op, err := d.boards.Edit(boardID, func(b board.Board) (board.Op, error) {
		taken := itemIDs(b)
		op := board.Op{Source: src}
		ids = ids[:0]
		for _, s := range strokes {
			id := board.RandomID("ink")
			for taken[id] {
				id = board.RandomID("ink")
			}
			taken[id] = true
			s.ID = id
			op.Ink = append(op.Ink, s)
			ids = append(ids, id)
		}
		return op, nil
	})
	if err != nil {
		return 0, nil, err
	}
	return op.Rev, ids, nil
}
