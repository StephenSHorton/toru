package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestInkFromWindow applies a stroke exactly as the whiteboard window's pen
// makes it (testdata/ink-op-from-window.json, written by the front end's
// `bun run test:ink` from a stroke drawn through the real pen primitive) and
// checks it is saved in the board file and reads back unchanged.
func TestInkFromWindow(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ink-op-from-window.json"))
	if err != nil {
		t.Fatal(err)
	}
	var op Op
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&op); err != nil {
		t.Fatalf("the window's op isn't a board op: %v", err)
	}
	if len(op.Ink) != 1 {
		t.Fatalf("want one stroke, got %d", len(op.Ink))
	}
	want := op.Ink[0]

	s, dir := setup(t)
	b, err := s.Create("Ink")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(b.ID, op); err != nil {
		t.Fatalf("the board refused the window's stroke: %v", err)
	}

	// Read the file itself, through the full check, not the store's copy.
	file, err := os.ReadFile(filepath.Join(dir, b.ID+Ext))
	if err != nil {
		t.Fatal(err)
	}
	saved, err := Decode(file)
	if err != nil {
		t.Fatalf("saved board doesn't read back: %v", err)
	}
	if len(saved.Ink) != 1 {
		t.Fatalf("saved board has %d strokes", len(saved.Ink))
	}
	got := saved.Ink[0]
	if got.ID != want.ID || got.Color != want.Color || got.Width != want.Width || !reflect.DeepEqual(got.Points, want.Points) {
		t.Fatalf("saved stroke = %+v, want %+v", got, want)
	}
	if got.CreatedBy != Person {
		t.Fatalf("stroke made by %q, want person", got.CreatedBy)
	}

	// Moving a stroke on the board moves its points; deleting it removes it.
	moved := got
	moved.Points = append([]float64(nil), got.Points...)
	for i := 0; i < len(moved.Points); i += 3 {
		moved.Points[i] += 40
		moved.Points[i+1] += 24
	}
	if _, err := s.Apply(b.ID, Op{Source: Person, Ink: []Stroke{moved}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(b.ID, Op{Source: Person, Delete: []string{want.ID}}); err != nil {
		t.Fatal(err)
	}
	after, err := s.Load(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Ink) != 0 {
		t.Fatalf("deleted stroke still on the board: %+v", after.Ink)
	}
}
