package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestShapes puts every shape and a highlighter stroke on a board, reads
// them back from disk, changes one, and checks the bad ones are refused.
func TestShapes(t *testing.T) {
	s, _ := setup(t)
	b, err := s.Create("Shapes")
	if err != nil {
		t.Fatal(err)
	}
	two := []float64{10, 20, 0.5, 110, 80, 0.5}
	ink := []Stroke{
		{ID: "r", Kind: "rect", Points: two, Color: "ink-blue", Width: 4, Fill: "ink-blue"},
		{ID: "e", Kind: "ellipse", Points: two, Color: "ink-red", Width: 2},
		{ID: "l", Kind: "line", Points: two, Color: "ink-green", Width: 8},
		{ID: "a", Kind: "arrow", Points: two, Color: "ink-yellow", Width: 4},
		{ID: "t", Kind: "text", Points: two, Color: "ink-white", Width: 4, Text: "Hello\nboard"},
		{ID: "j", Kind: "emoji", Points: two, Color: "ink-white", Width: 4, Text: "👍"},
		{ID: "h", Points: []float64{0, 0, 0.5, 50, 5, 0.5, 100, 0, 0.5}, Color: "ink-yellow", Width: 16, Highlight: true},
	}
	if _, err := s.Apply(b.ID, Op{Source: Person, Ink: ink}); err != nil {
		t.Fatal(err)
	}
	disk, err := NewStore().Load(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := range ink {
		ink[i].Meta = Meta{CreatedBy: Person, UpdatedBy: Person}
	}
	if !reflect.DeepEqual(disk.Ink, ink) {
		t.Fatalf("shapes on disk = %+v", disk.Ink)
	}

	// Reshaping one keeps it a shape; dropping its fill drops it.
	moved := ink[0]
	moved.Points = []float64{0, 0, 0.5, 40, 40, 0.5}
	moved.Fill = ""
	after, err := s.Apply(b.ID, Op{Source: Person, Ink: []Stroke{moved}})
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Ink[0]; got.Kind != "rect" || got.Fill != "" || got.Points[3] != 40 {
		t.Errorf("reshaped rect = %+v", got)
	}

	for name, bad := range map[string]Stroke{
		"unknown kind":      {Kind: "star", Points: two, Color: "x", Width: 1},
		"three points":      {Kind: "rect", Points: append(two, 1, 1, 0.5), Color: "x", Width: 1},
		"one point":         {Kind: "line", Points: two[:3], Color: "x", Width: 1},
		"fill on a line":    {Kind: "line", Points: two, Color: "x", Width: 1, Fill: "x"},
		"fill on freehand":  {Points: two, Color: "x", Width: 1, Fill: "x"},
		"text on freehand":  {Points: two, Color: "x", Width: 1, Text: "x"},
		"text on a rect":    {Kind: "rect", Points: two, Color: "x", Width: 1, Text: "x"},
		"empty text":        {Kind: "text", Points: two, Color: "x", Width: 1, Text: "  "},
		"long text":         {Kind: "text", Points: two, Color: "x", Width: 1, Text: strings.Repeat("a", MaxShapeText+1)},
		"no emoji":          {Kind: "emoji", Points: two, Color: "x", Width: 1},
		"highlighter shape": {Kind: "rect", Points: two, Color: "x", Width: 1, Highlight: true},
	} {
		if _, err := s.Apply(b.ID, Op{Source: Person, Ink: []Stroke{bad}}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// TestShapesFromWindow applies every shape and a highlighter stroke exactly
// as the whiteboard window sends them (testdata/shapes-op-from-window.json,
// written by the front end's `bun run test:ink`), reads them back from the
// board file, and moves one: it stays the same kind of shape.
func TestShapesFromWindow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "shapes-op-from-window.json"))
	if err != nil {
		t.Fatal(err)
	}
	var op Op
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&op); err != nil {
		t.Fatalf("the window's op isn't a board op: %v", err)
	}
	kinds := map[string]bool{}
	for _, s := range op.Ink {
		kinds[s.Kind] = true
		if s.Highlight {
			kinds["highlight"] = true
		}
	}
	for _, k := range append(ShapeKinds, "highlight") {
		if !kinds[k] {
			t.Errorf("the window's op has no %s", k)
		}
	}

	s, dir := setup(t)
	b, err := s.Create("Shapes")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(b.ID, op); err != nil {
		t.Fatalf("the board refused the window's shapes: %v", err)
	}
	file, err := os.ReadFile(filepath.Join(dir, b.ID+Ext))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := Decode(file)
	if err != nil {
		t.Fatalf("saved board doesn't read back: %v", err)
	}
	for i := range saved.Ink {
		got := saved.Ink[i]
		got.Meta = Meta{}
		if !reflect.DeepEqual(got, op.Ink[i]) {
			t.Errorf("saved %+v, want %+v", got, op.Ink[i])
		}
	}

	moved := saved.Ink[0]
	moved.Points = append([]float64(nil), moved.Points...)
	moved.Points[0] += 40
	moved.Points[3] += 40
	after, err := s.Apply(b.ID, Op{Source: Person, Ink: []Stroke{moved}})
	if err != nil {
		t.Fatal(err)
	}
	if got := after.Ink[0]; got.Kind != op.Ink[0].Kind || got.Fill != op.Ink[0].Fill || got.Points[0] != op.Ink[0].Points[0]+40 {
		t.Errorf("moved shape = %+v", got)
	}
}
