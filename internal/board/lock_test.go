package board

import (
	"strings"
	"testing"
)

// lockedBoard has a locked block, frame, stroke and line, and unlocked
// blocks around them.
func lockedBoard() *Doc {
	m := Meta{CreatedBy: Person, UpdatedBy: Person}
	return NewDoc(Board{
		Format: Format, Version: Version, ID: "brd-lock", Name: "Lock",
		Frames: []Frame{{ID: "f", Name: "F", X: 0, Y: 0, W: 400, H: 300, Locked: true, Meta: m}},
		Blocks: []Block{
			{ID: "a", Type: "sticky", X: 10, Y: 10, W: 200, H: 200, Props: props(`{"text":"A"}`), Locked: true, Meta: m},
			{ID: "b", Type: "sticky", X: 500, Y: 10, W: 200, H: 200, Props: props(`{"text":"B"}`), Meta: m},
			{ID: "c", Type: "sticky", X: 900, Y: 10, W: 200, H: 200, Props: props(`{"text":"C"}`), Meta: m},
		},
		Links: []Link{{ID: "l", From: "b", To: "c", Locked: true, Meta: m}},
		Ink:   []Stroke{{ID: "s", Points: []float64{0, 0, 0.5, 10, 10, 0.5}, Color: "ink-blue", Width: 4, Locked: true, Meta: m}},
	})
}

func blockOn(d *Doc, id string) Block {
	for _, x := range d.Board().Blocks {
		if x.ID == id {
			return x
		}
	}
	return Block{}
}

func TestLockHoldsAgainstAgents(t *testing.T) {
	agent := Agent("Claude Code")
	a := blockOn(lockedBoard(), "a")
	moved, resized, unlocked := a, a, a
	moved.X += 40
	resized.W = 260
	unlocked.Locked = false
	stroke := lockedBoard().Board().Ink[0]
	stroke.Points = []float64{5, 5, 0.5, 15, 15, 0.5}
	frame := lockedBoard().Board().Frames[0]
	frame.H = 500

	refused := []struct {
		name, want string
		op         Op
	}{
		{"move a block", `block "a" is locked, so it can't be moved`, Op{Blocks: []Block{moved}}},
		{"resize a block", `block "a" is locked, so it can't be resized`, Op{Blocks: []Block{resized}}},
		{"unlock a block", `block "a" is locked, so it can't be unlocked`, Op{Blocks: []Block{unlocked}}},
		{"delete a block", `block "a" is locked, so it can't be deleted`, Op{Delete: []string{"a"}}},
		{"resize a frame", `frame "f" is locked, so it can't be resized`, Op{Frames: []Frame{frame}}},
		{"move ink", `ink stroke "s" is locked, so it can't be moved`, Op{Ink: []Stroke{stroke}}},
		{"delete a line", `line "l" is locked, so it can't be deleted`, Op{Delete: []string{"l"}}},
		{"delete what a locked line hangs on", `line "l" is locked, and removing "b" would take it too`, Op{Delete: []string{"b"}}},
	}
	for _, c := range refused {
		d := lockedBoard()
		before := d.Board()
		c.op.Source = agent
		_, err := d.Apply(c.op)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "only the person can unlock it") {
			t.Errorf("%s: err = %v, want %q", c.name, err, c.want)
		}
		if after := d.Board(); after.Rev != before.Rev || len(after.Blocks) != len(before.Blocks) || len(after.Links) != len(before.Links) || blockOn(d, "a").X != 10 {
			t.Errorf("%s: the board changed", c.name)
		}
	}

	// What isn't moving, resizing, deleting or unlocking stays open to agents.
	d := lockedBoard()
	edited := a
	edited.Props = props(`{"text":"A, edited"}`)
	edited.Rotation = 8
	if _, err := d.Apply(Op{Source: agent, Blocks: []Block{edited}}); err != nil {
		t.Fatalf("an agent editing a locked block's text: %v", err)
	}
	b := blockOn(d, "b")
	b.Locked = true
	if _, err := d.Apply(Op{Source: agent, Blocks: []Block{b}}); err != nil {
		t.Fatalf("an agent locking a block: %v", err)
	}
	if _, err := d.Apply(Op{Source: agent, Delete: []string{"c"}}); err == nil {
		t.Fatal("deleting c would take the locked line l")
	}
}

func TestPersonChangesLockedItems(t *testing.T) {
	d := lockedBoard()
	a := blockOn(d, "a")
	a.X, a.W = 60, 260
	if _, err := d.Apply(Op{Source: Person, Blocks: []Block{a}}); err != nil {
		t.Fatalf("the person moving and resizing a locked block: %v", err)
	}
	a.Locked = false
	if _, err := d.Apply(Op{Source: Person, Blocks: []Block{a}}); err != nil {
		t.Fatalf("the person unlocking a block: %v", err)
	}
	// Unlocked, an agent can move it again.
	a.X = 100
	if _, err := d.Apply(Op{Source: Agent("Claude Code"), Blocks: []Block{a}}); err != nil {
		t.Fatalf("an agent moving an unlocked block: %v", err)
	}
	if _, err := d.Apply(Op{Source: Person, Delete: []string{"f", "s", "l"}}); err != nil {
		t.Fatalf("the person deleting locked items: %v", err)
	}
	// Undo puts back exact earlier states, whoever asks.
	if _, err := d.Undo(Agent("Claude Code")); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if len(d.Board().Frames) != 1 || !d.Board().Frames[0].Locked {
		t.Fatalf("undo didn't put the locked frame back: %+v", d.Board().Frames)
	}
}

func TestLayoutSkipsLockedBlocks(t *testing.T) {
	b := lockedBoard().Board()
	pos, err := Layout(b, nil, LayoutGrid, LayoutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pos["a"]; ok || len(pos) != 2 {
		t.Fatalf("layout of every block = %v, want b and c only", pos)
	}
}
