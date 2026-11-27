package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The live board (docs/whiteboard.md, section 2) keeps a copy of a board in
// the window and applies Go's ops to it (frontend/src/whiteboard/live.ts).
// These tests tie the two together through files both sides read:
//
//   - testdata/live-ops.json: ops Go applied (agents' and the person's
//     changes, deletes that take links and empty frames, stacking moves,
//     undo, redo, a rename) and the board they make. The window's
//     `bun run test:live` replays them, in order and shuffled, doubled and
//     with gaps, and must end on exactly this board.
//   - testdata/window-ops.json: ops exactly as the window sends them (made
//     by `bun scripts/live-check.tsx --write` from edits on the real canvas)
//     and the board the window's store ended on. Go must accept each one and
//     end on the same board.

// liveEnv rewrites testdata/live-ops.json.
const liveEnv = "TORU_UPDATE_LIVE"

type liveFile struct {
	Note  string `json:"note"`
	Start Board  `json:"start"`
	Ops   []Op   `json:"ops"`
	End   Board  `json:"end"`
}

// fixedClock makes now tick a second per call from a fixed time.
func fixedClock(t *testing.T) {
	t.Helper()
	at := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	old := now
	now = func() time.Time { at = at.Add(time.Second); return at }
	t.Cleanup(func() { now = old })
}

func TestLiveOpsGolden(t *testing.T) {
	fixedClock(t)
	start := New("brd-live", "Live board", time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC))
	d := NewDoc(start)
	var ops []Op
	agent := Agent("Claude Code")
	do := func(what string, op Op, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		ops = append(ops, op)
	}
	sticky := func(text string) json.RawMessage { return props(`{"text":"` + text + `","color":"yellow"}`) }

	op, err := d.Apply(Op{Source: agent,
		Frames: []Frame{{ID: "frm-ideas", Name: "Ideas", X: -20, Y: -40, W: 700, H: 300, Color: "blue"}},
		Blocks: []Block{
			{ID: "blk-a", Type: "sticky", X: 0, Y: 0, W: 200, H: 200, Props: sticky("A")},
			{ID: "blk-b", Type: "sticky", X: 240, Y: 0, W: 200, H: 200, Props: sticky("B"), Group: "g1"},
			{ID: "blk-c", Type: "sticky", X: 480, Y: 0, W: 200, H: 200, Props: sticky("C"), Frame: "frm-ideas", Rotation: 3},
		},
		Ink:   []Stroke{{ID: "ink-1", Points: []float64{0, 300, 0.5, 40, 320, 0.7}, Color: "ink-blue", Width: 4, Frame: "frm-ideas"}},
		Links: []Link{{ID: "lnk-ab", From: "blk-a", To: "blk-b", FromPort: "right", ToPort: "left", Label: "then"}},
	})
	do("agent builds", op, err)
	op, err = d.Apply(Op{Source: Person, Blocks: []Block{{ID: "blk-a", Type: "sticky", X: 16, Y: 400, W: 200, H: 200, Props: sticky("A")}}})
	do("person moves a", op, err)
	op, err = d.Apply(Op{Source: Person, Links: []Link{{ID: "lnk-bc", From: "blk-b", To: "blk-c", FromPort: "right", ToPort: "left"}}})
	do("person links b to c", op, err)
	op, err = d.Apply(Op{Source: agent, Blocks: []Block{{ID: "blk-b", Type: "sticky", X: 240, Y: 0, W: 200, H: 200, Props: sticky("B, voted up"), Group: "g1"}}, Pos: map[string]int{"blk-b": 0}})
	do("agent edits b and sends it to the back", op, err)
	op, err = d.Apply(Op{Source: Person, Delete: []string{"frm-ideas"}})
	do("person deletes the frame", op, err)
	op, err = d.Apply(Op{Source: agent, Delete: []string{"blk-b"}})
	do("agent deletes b and its links", op, err)
	op, err = d.Undo(Person)
	do("undo the delete", op, err)
	op, err = d.Undo(Person)
	do("undo the frame delete", op, err)
	op, err = d.Redo(Person)
	do("redo the frame delete", op, err)
	name := "Live board, renamed"
	op, err = d.Apply(Op{Source: Person, Name: &name})
	do("rename", op, err)
	op, err = d.Apply(Op{Source: Person,
		Blocks: []Block{
			{ID: "blk-d", Type: "sticky", X: 0, Y: 700, W: 120, H: 120, Props: sticky("D")},
			{ID: "blk-e", Type: "sticky", X: 160, Y: 700, W: 120, H: 120, Props: sticky("E")},
		},
		Pos: map[string]int{"blk-d": 0, "blk-e": 2},
	})
	do("person adds d and e at places in the stack", op, err)

	f := liveFile{
		Note:  "Written by internal/board/live_test.go (TORU_UPDATE_LIVE=1 go test ./internal/board -run TestLiveOpsGolden). Don't edit by hand.",
		Start: start, Ops: ops, End: d.Board(),
	}
	got := encodeIndent(t, f)
	path := filepath.Join("testdata", "live-ops.json")
	if os.Getenv(liveEnv) != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with %s=1 to write it)", err, liveEnv)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s is out of date: run %s=1 go test ./internal/board -run TestLiveOpsGolden", path, liveEnv)
	}
}

// TestWindowOps applies the ops the board window sent, in order, through the
// store and the window's service, and checks the board ends as the window's
// own store said.
func TestWindowOps(t *testing.T) { replayWindowOps(t, "window-ops.json") }

// TestPanelOps does the same for the ops the window's properties panel sent
// (frontend/scripts/props-panel-check.tsx): props, box, rotation, frame,
// lock, a link's label and style, a stroke's colour, each one op, with undos.
func TestPanelOps(t *testing.T) { replayWindowOps(t, "panel-ops.json") }

func replayWindowOps(t *testing.T, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var f liveFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if len(f.Ops) < 5 {
		t.Fatalf("want the window's ops, got %d", len(f.Ops))
	}
	s, _ := setup(t)
	if _, err := s.Save(f.Start); err != nil {
		t.Fatalf("start board: %v", err)
	}
	var heard []Op
	s.OnOp(func(id string, op Op) {
		if id == f.Start.ID {
			heard = append(heard, op)
		}
	})
	svc := NewService(s)
	for i, op := range f.Ops {
		switch op.Kind {
		case KindUndo:
			if got, err := svc.Undo(f.Start.ID); err != nil || got == nil {
				t.Fatalf("op %d: undo: %v", i, err)
			}
		case KindRedo:
			if got, err := svc.Redo(f.Start.ID); err != nil || got == nil {
				t.Fatalf("op %d: redo: %v", i, err)
			}
		default:
			if _, err := svc.Apply(f.Start.ID, op); err != nil {
				t.Fatalf("op %d from the window (%s) was refused: %v", i, op.Source, err)
			}
		}
	}
	if len(heard) != len(f.Ops) {
		t.Fatalf("OnOp heard %d ops, want %d", len(heard), len(f.Ops))
	}
	for i, op := range heard {
		if op.Rev != f.Start.Rev+i+1 {
			t.Fatalf("op %d has rev %d, want %d (in order, none twice)", i, op.Rev, f.Start.Rev+i+1)
		}
	}
	got, err := s.Load(f.Start.ID)
	if err != nil {
		t.Fatal(err)
	}
	strip := func(b Board) Board {
		b.CreatedAt, b.UpdatedAt = time.Time{}, time.Time{}
		return b
	}
	g, w := encodeIndent(t, strip(got)), encodeIndent(t, strip(f.End))
	if !bytes.Equal(g, w) {
		t.Fatalf("Go's board after the window's ops differs from the window's:\n--- go\n%s\n--- window\n%s", g, w)
	}
}

// TestServiceSource checks what the window's service takes as a source.
func TestServiceSource(t *testing.T) {
	s, _ := setup(t)
	b, err := s.Create("Sources")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(s)
	block := func(id string) []Block {
		return []Block{{ID: id, Type: "sticky", W: 10, H: 10, Props: props(`{"text":"x"}`)}}
	}
	op, err := svc.Apply(b.ID, Op{Blocks: block("a")})
	if err != nil || op.Source != Person || op.Blocks[0].CreatedBy != Person {
		t.Fatalf("no source: %v %+v", err, op)
	}
	op, err = svc.Apply(b.ID, Op{Source: Agent("claude-code"), Blocks: block("b")})
	if err != nil || op.Blocks[0].CreatedBy != "agent:claude-code" {
		t.Fatalf("agent source: %v %+v", err, op)
	}
	if _, err := svc.Apply(b.ID, Op{Source: "root", Blocks: block("c")}); err == nil {
		t.Fatal("a made-up source was accepted")
	}
	if _, err := svc.Apply(b.ID, Op{Source: Person, Kind: KindUndo, Rev: 99, Blocks: block("d")}); err != nil {
		t.Fatalf("rev and kind from the window should be ignored: %v", err)
	}
	// Undo takes back the agent's change as well as the person's.
	u, err := svc.Undo(b.ID)
	if err != nil || u == nil || u.Kind != KindUndo || u.Source != Person {
		t.Fatalf("undo: %v %+v", err, u)
	}
	for range 3 {
		if _, err := svc.Undo(b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if u, err := svc.Undo(b.ID); err != nil || u != nil {
		t.Fatalf("nothing to undo should be nil, nil: %v %+v", err, u)
	}
	if r, err := svc.Redo(b.ID); err != nil || r == nil || r.Kind != KindRedo {
		t.Fatalf("redo: %v %+v", err, r)
	}
}

func encodeIndent(t *testing.T, v any) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
