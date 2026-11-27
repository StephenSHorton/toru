package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/board"
)

// TestBoardDraw puts strokes on a board with board.draw and reads them back
// from the board file itself.
func TestBoardDraw(t *testing.T) {
	reg, ev := newBoardRegistry(t)
	id := call(t, reg, "claude-code", "board.create", `{"name":"Ink"}`)["id"].(string)
	out := call(t, reg, "claude-code", "board.draw", `{"id":"`+id+`","strokes":[
		{"points":[[0,0],[40,20],[80,0,0.9]],"color":"ink-blue","width":"bold"},
		{"points":[[10,100],[90,100]],"color":"red","width":3},
		{"points":[[5,5]]}
	]}`)
	ids := out["strokes"].([]any)
	if len(ids) != 3 || out["rev"].(float64) < 1 {
		t.Fatalf("draw = %v", out)
	}

	dir, _ := board.Dir()
	file, err := os.ReadFile(filepath.Join(dir, id+board.Ext))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := board.Decode(file)
	if err != nil {
		t.Fatalf("saved board doesn't read back: %v", err)
	}
	if len(saved.Ink) != 3 {
		t.Fatalf("saved %d strokes", len(saved.Ink))
	}
	want := []board.Stroke{
		{Points: []float64{0, 0, 0.5, 40, 20, 0.5, 80, 0, 0.9}, Color: "ink-blue", Width: 8},
		{Points: []float64{10, 100, 0.5, 90, 100, 0.5}, Color: "ink-red", Width: 3},
		{Points: []float64{5, 5, 0.5}, Color: "ink-white", Width: 2},
	}
	for i, s := range saved.Ink {
		t.Logf("saved in %s: %s %s width %g, %d points %v, made by %s", filepath.Base(dir)+"/"+id+board.Ext, s.ID, s.Color, s.Width, len(s.Points)/3, s.Points, s.CreatedBy)
		if s.ID != ids[i] || s.Color != want[i].Color || s.Width != want[i].Width || !reflect.DeepEqual(s.Points, want[i].Points) {
			t.Errorf("stroke %d = %+v, want %+v", i, s, want[i])
		}
		if s.CreatedBy != "agent:claude-code" {
			t.Errorf("stroke %d made by %q", i, s.CreatedBy)
		}
	}
	ev.mu.Lock()
	if n := strings.Count(strings.Join(ev.names, ","), evBoardOp); n != 1 {
		t.Errorf("want one board:op for the draw, got %d (%v)", n, ev.names)
	}
	ev.mu.Unlock()

	// One undoable change, removable with board.delete.
	call(t, reg, "t", "board.delete", `{"id":"`+id+`","items":["`+ids[0].(string)+`"],"confirm":true}`)
	if got := len(readBoard(t, reg, id).Ink); got != 2 {
		t.Fatalf("strokes after delete = %d", got)
	}

	for in, msg := range map[string]string{
		`{"id":"` + id + `","strokes":[]}`:                                  "strokes",
		`{"id":"` + id + `","strokes":[{"points":[]}]}`:                     "needs \"points\"",
		`{"id":"` + id + `","strokes":[{"points":[[1]]}]}`:                  "[x, y]",
		`{"id":"` + id + `","strokes":[{"points":[[1,2]],"color":"#f00"}]}`: "isn't a pen",
		`{"id":"` + id + `","strokes":[{"points":[[1,2]],"width":"huge"}]}`: "isn't a pen width",
		`{"id":"` + id + `","strokes":[{"points":[[1,2]],"width":0}]}`:      "bad width",
		`{"id":"` + id + `","strokes":[{"points":[[1,2,3]]}]}`:              "pressure",
		`{"id":"` + id + `","strokes":[{"points":[[1,2]],"frame":"nope"}]}`: "nope",
		`{"id":"brd-missing","strokes":[{"points":[[1,2]]}]}`:               "",
	} {
		if got := callErr(t, reg, "board.draw", in); !strings.Contains(got, msg) {
			t.Errorf("%s: error %q, want %q", in, got, msg)
		}
	}
	if got := len(readBoard(t, reg, id).Ink); got != 2 {
		t.Fatalf("refused draws changed the board: %d strokes", got)
	}
}
