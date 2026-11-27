package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// agent_commands_board_shapes.go registers board.shape: agents put shapes on
// a board's ink layer, the ones the drawing bar's shape tools draw
// (frontend src/whiteboard/shapes.ts). A shape is a board.Stroke with a Kind
// and two points, so the person can select, move and reshape it after.

// Text labels and emoji are sized by their font size when no "to" is given,
// the same way the window lays them out (shapes.ts, textBox): one line is
// textLineHeight font sizes tall, and a character about textCharWidth wide.
const (
	textLineHeight   = 1.25
	textCharWidth    = 0.6
	defaultTextSize  = 24
	defaultEmojiSize = 48
	maxShapeSize     = 2000
)

// textBox is the box a text label of this size takes from its top left.
func textBox(text string, size float64) (w, h float64) {
	lines := strings.Split(text, "\n")
	longest := 1
	for _, l := range lines {
		longest = max(longest, len([]rune(l)))
	}
	return math.Ceil(float64(longest) * size * textCharWidth), math.Ceil(float64(len(lines)) * size * textLineHeight)
}

func registerBoardShapeCommand(reg *agentctl.Registry, d boardAgentDeps) {
	point := map[string]any{"type": "array", "minItems": 2, "maxItems": 2, "items": map[string]any{"type": "number"}}
	shape := map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"kind", "from"},
		"properties": map[string]any{
			"kind": map[string]any{"type": "string", "enum": board.ShapeKinds,
				"description": "rect and ellipse fill the box from one corner to the other; line and arrow run from one end to the other (the arrow's head at \"to\"); text is a label and emoji one emoji, at \"from\"."},
			"from": withDesc(point, "[x, y] in world units (CSS pixels at zoom 1): a corner, a line's start, or a label's top left."),
			"to":   withDesc(point, "[x, y]: the opposite corner or the line's end. Text and emoji may leave it out and give size instead."),
			"color": map[string]any{"type": "string", "enum": penNames(),
				"description": "The outline, line or text colour: a pen token, which follows the theme. Defaults to ink-white."},
			"width": map[string]any{"description": "The line's width: fine, medium or bold (2, 4, 8), or a number of world units. Defaults to medium.",
				"oneOf": []any{map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": 1000},
					map[string]any{"type": "string", "enum": []string{"fine", "medium", "bold", "ink-width-fine", "ink-width-medium", "ink-width-bold"}}}},
			"fill": map[string]any{"type": "string", "enum": penNames(),
				"description": "rect and ellipse only: a pen token to fill the inside with, drawn see-through. Left out, no fill."},
			"text":  map[string]any{"type": "string", "maxLength": board.MaxShapeText, "description": "text: the label's words (\\n for a new line). emoji: one emoji."},
			"size":  map[string]any{"type": "number", "exclusiveMinimum": 0, "maximum": maxShapeSize, "description": "text and emoji without \"to\": the font size in world units. Defaults to 24 for text, 48 for emoji."},
			"frame": map[string]any{"type": "string", "description": "The id of a frame for the shape to sit in."},
		},
	}
	reg.Register(agentctl.Command{
		Name: "board.shape",
		Description: "Draw shapes on a whiteboard's ink layer, as the drawing bar's shape tools do: rectangles, ellipses, lines, arrows, text labels and emoji. " +
			"Each stays a shape the person can select, move, resize and recolour. All go in one change, shown at once in an open board window. " +
			"Returns the new shapes' ids in the order sent, and the board's rev. Remove them with board.delete; freehand lines are board.draw.",
		Input: schema(map[string]any{
			"id":     map[string]any{"type": "string", "description": "The board's id, from board.list or board.create."},
			"shapes": map[string]any{"type": "array", "minItems": 1, "maxItems": maxPerOp, "items": shape, "description": "The shapes to draw."},
		}, "id", "shapes"),
		Handler: func(ctx context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ID     string `json:"id"`
				Shapes []struct {
					Kind  string          `json:"kind"`
					From  []float64       `json:"from"`
					To    []float64       `json:"to"`
					Color string          `json:"color"`
					Width json.RawMessage `json:"width"`
					Fill  string          `json:"fill"`
					Text  string          `json:"text"`
					Size  float64         `json:"size"`
					Frame string          `json:"frame"`
				} `json:"shapes"`
			}](in)
			if err != nil {
				return nil, err
			}
			if len(v.Shapes) == 0 {
				return nil, errors.New(`give "shapes"`)
			}
			if len(v.Shapes) > maxPerOp {
				return nil, fmt.Errorf("%d shapes at most in one call", maxPerOp)
			}
			strokes := make([]board.Stroke, len(v.Shapes))
			for i, s := range v.Shapes {
				fail := func(err error) (any, error) { return nil, fmt.Errorf("shapes[%d]: %w", i, err) }
				if !slices.Contains(board.ShapeKinds, s.Kind) {
					return fail(fmt.Errorf("kind %q isn't a shape: one of %s", s.Kind, strings.Join(board.ShapeKinds, ", ")))
				}
				if len(s.From) != 2 {
					return fail(errors.New(`"from" is [x, y]`))
				}
				words := s.Kind == "text" || s.Kind == "emoji"
				if words && strings.TrimSpace(s.Text) == "" {
					return fail(fmt.Errorf("a %s needs \"text\"", s.Kind))
				}
				if !words && s.Text != "" {
					return fail(errors.New(`only text and emoji take "text"`))
				}
				to := s.To
				switch {
				case len(to) == 2:
				case len(to) != 0:
					return fail(errors.New(`"to" is [x, y]`))
				case words:
					size := s.Size
					if size == 0 {
						size = defaultTextSize
						if s.Kind == "emoji" {
							size = defaultEmojiSize
						}
					}
					if size < 0 || size > maxShapeSize {
						return fail(errors.New(`"size" is a font size above 0`))
					}
					w, h := size, size
					if s.Kind == "text" {
						w, h = textBox(s.Text, size)
					}
					to = []float64{s.From[0] + w, s.From[1] + h}
				default:
					return fail(fmt.Errorf("a %s needs \"to\"", s.Kind))
				}
				color, err := inkPen(s.Color)
				if err != nil {
					return fail(err)
				}
				width := 4.0
				if len(s.Width) > 0 {
					if width, err = inkWidth(s.Width); err != nil {
						return fail(err)
					}
				}
				fill := ""
				if s.Fill != "" {
					if s.Kind != "rect" && s.Kind != "ellipse" {
						return fail(errors.New(`only rect and ellipse take "fill"`))
					}
					if fill, err = inkPen(s.Fill); err != nil {
						return fail(err)
					}
				}
				strokes[i] = board.Stroke{
					Kind:   s.Kind,
					Points: []float64{s.From[0], s.From[1], defaultPressure, to[0], to[1], defaultPressure},
					Color:  color, Width: width, Fill: fill, Text: s.Text, Frame: s.Frame,
				}
			}
			rev, ids, err := addInk(ctx, d, v.ID, strokes)
			if err != nil {
				return nil, err
			}
			return map[string]any{"rev": rev, "shapes": ids}, nil
		},
	})
}

// withDesc is a copy of a schema with a description.
func withDesc(s map[string]any, desc string) map[string]any {
	out := make(map[string]any, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	out["description"] = desc
	return out
}
