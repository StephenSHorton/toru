package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/board"
)

// TestBoardShape draws every shape with board.shape, and a highlighter
// stroke with board.draw, and reads them back from the saved board.
func TestBoardShape(t *testing.T) {
	reg, _ := newBoardRegistry(t)
	id := call(t, reg, "claude-code", "board.create", `{"name":"Shapes"}`)["id"].(string)
	out := call(t, reg, "claude-code", "board.shape", `{"id":"`+id+`","shapes":[
		{"kind":"rect","from":[0,0],"to":[200,120],"color":"blue","fill":"ink-blue","width":"bold"},
		{"kind":"ellipse","from":[240,0],"to":[400,120],"color":"ink-purple"},
		{"kind":"line","from":[0,200],"to":[200,260],"width":2},
		{"kind":"arrow","from":[240,200],"to":[400,260],"color":"orange"},
		{"kind":"text","from":[0,300],"text":"Two\nlines","size":20},
		{"kind":"emoji","from":[240,300],"text":"🎉"}
	]}`)
	ids := out["shapes"].([]any)
	if len(ids) != 6 {
		t.Fatalf("shape = %v", out)
	}
	saved := readBoard(t, reg, id).Ink
	want := []board.Stroke{
		{Kind: "rect", Points: []float64{0, 0, 0.5, 200, 120, 0.5}, Color: "ink-blue", Width: 8, Fill: "ink-blue"},
		{Kind: "ellipse", Points: []float64{240, 0, 0.5, 400, 120, 0.5}, Color: "ink-purple", Width: 4},
		{Kind: "line", Points: []float64{0, 200, 0.5, 200, 260, 0.5}, Color: "ink-white", Width: 2},
		{Kind: "arrow", Points: []float64{240, 200, 0.5, 400, 260, 0.5}, Color: "ink-orange", Width: 4},
		// 5 characters at 0.6 × 20 wide, 2 lines at 1.25 × 20 tall.
		{Kind: "text", Points: []float64{0, 300, 0.5, 60, 350, 0.5}, Color: "ink-white", Width: 4, Text: "Two\nlines"},
		{Kind: "emoji", Points: []float64{240, 300, 0.5, 288, 348, 0.5}, Color: "ink-white", Width: 4, Text: "🎉"},
	}
	if len(saved) != len(want) {
		t.Fatalf("saved %d shapes", len(saved))
	}
	for i, s := range saved {
		if s.ID != ids[i] || s.CreatedBy != "agent:claude-code" {
			t.Errorf("shape %d = %+v", i, s)
		}
		s.ID, s.Meta = "", board.Meta{}
		if !reflect.DeepEqual(s, want[i]) {
			t.Errorf("shape %d = %+v, want %+v", i, s, want[i])
		}
	}

	drawn := call(t, reg, "claude-code", "board.draw", `{"id":"`+id+`","strokes":[{"points":[[0,0],[100,0]],"color":"pink","width":16,"highlight":true}]}`)
	if len(drawn["strokes"].([]any)) != 1 {
		t.Fatalf("draw = %v", drawn)
	}
	if h := readBoard(t, reg, id).Ink[6]; !h.Highlight || h.Color != "ink-pink" || h.Kind != "" {
		t.Errorf("highlighter = %+v", h)
	}

	for in, msg := range map[string]string{
		`{"id":"` + id + `","shapes":[{"kind":"star","from":[0,0],"to":[1,1]}]}`:                "isn't a shape",
		`{"id":"` + id + `","shapes":[{"kind":"rect","from":[0,0]}]}`:                           `needs "to"`,
		`{"id":"` + id + `","shapes":[{"kind":"rect","from":[0],"to":[1,1]}]}`:                  `"from" is [x, y]`,
		`{"id":"` + id + `","shapes":[{"kind":"line","from":[0,0],"to":[1,1],"fill":"red"}]}`:   "only rect and ellipse",
		`{"id":"` + id + `","shapes":[{"kind":"text","from":[0,0]}]}`:                           `needs "text"`,
		`{"id":"` + id + `","shapes":[{"kind":"rect","from":[0,0],"to":[1,1],"text":"x"}]}`:     "only text and emoji",
		`{"id":"` + id + `","shapes":[{"kind":"rect","from":[0,0],"to":[1,1],"color":"#f00"}]}`: "isn't a pen",
		`{"id":"` + id + `","shapes":[{"kind":"rect","from":[0,0],"to":[1,1],"frame":"nope"}]}`: "nope",
	} {
		if got := callErr(t, reg, "board.shape", in); !strings.Contains(got, msg) {
			t.Errorf("%s: error %q, want %q", in, got, msg)
		}
	}
	if got := len(readBoard(t, reg, id).Ink); got != 7 {
		t.Fatalf("refused shapes changed the board: %d", got)
	}
}
