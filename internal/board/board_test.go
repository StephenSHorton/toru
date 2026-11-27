package board

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func setup(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "boards")
	old := Dir
	Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { Dir = old })
	return NewStore(), dir
}

func props(s string) json.RawMessage { return json.RawMessage(s) }

// sample is a board with one of everything, made through ops.
func sample(t *testing.T, s *Store) Board {
	t.Helper()
	b, err := s.Create("  Launch ideas ")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(b.ID, Op{
		Source: Person,
		Frames: []Frame{{ID: "f1", Name: "Ideas", X: 0, Y: 0, W: 800, H: 600, Color: "#334155"}},
		Blocks: []Block{
			{ID: "a", Type: "sticky", X: 10, Y: 20, W: 200, H: 120, Frame: "f1", Props: props(`{"text":"R&D <ship> it — ✓","color":"yellow"}`)},
			{ID: "b", Type: "poll", X: 300, Y: 20, W: 240, H: 160, Rotation: 3, Group: "g1", Props: props(`{"question":"Which?","options":["A","B"]}`)},
		},
		Links: []Link{{ID: "l1", From: "a", To: "b", FromPort: "right", ToPort: "left", Label: "leads to", Props: props(`{"arrow":"end"}`)}},
		Ink:   []Stroke{{ID: "s1", Points: []float64{0, 0, 0.5, 10, 12, 0.7, 20, 18, 1}, Color: "#ffffff", Width: 2.5, Frame: "f1"}},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestFormatRoundTrip(t *testing.T) {
	s, dir := setup(t)
	b := sample(t, s)
	if b.Name != "Launch ideas" || b.Format != Format || b.Version != Version || b.Rev != 1 {
		t.Fatalf("header = %q %q v%d rev %d", b.Name, b.Format, b.Version, b.Rev)
	}
	data, err := os.ReadFile(filepath.Join(dir, b.ID+Ext))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("saved %s:\n%s", b.ID+Ext, data)
	if !strings.Contains(string(data), `R&D <ship>`) {
		t.Error("props were HTML-escaped in the file")
	}
	var head map[string]any
	if err := json.Unmarshal(data, &head); err != nil {
		t.Fatal(err)
	}
	if head["format"] != "toruboard" || head["version"] != float64(1) {
		t.Errorf("file header = %v %v", head["format"], head["version"])
	}
	back, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, b) {
		t.Errorf("decode(encode) differs:\n got %+v\nwant %+v", back, b)
	}
	again, err := Encode(back)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Error("encode(decode(file)) isn't the same bytes")
	}
	// A fresh store reads the same board from disk.
	fresh, err := NewStore().Load(b.ID)
	if err != nil || !reflect.DeepEqual(fresh, b) {
		t.Errorf("fresh load = %+v, %v", fresh, err)
	}
	if b.Blocks[0].CreatedBy != Person || b.Links[0].UpdatedBy != Person {
		t.Errorf("who made it = %+v", b.Blocks[0].Meta)
	}
	// An empty board saves its lists as [], not null.
	e, _ := Encode(New("brd-x", "Empty", time.Unix(0, 0).UTC()))
	if !strings.Contains(string(e), `"blocks": []`) {
		t.Errorf("empty board = %s", e)
	}
}

func TestDefaultDir(t *testing.T) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	got, err := Dir()
	if want := filepath.Join(cfg, "toru", "boards"); err != nil || got != want {
		t.Errorf("Dir() = %q, %v; want %q", got, err, want)
	}
	t.Logf("boards are saved in %s", got)
}

func TestStoreCRUD(t *testing.T) {
	s, dir := setup(t)
	t.Logf("store folder for this test: %s", dir)
	if l, err := s.List(); err != nil || len(l) != 0 {
		t.Fatalf("empty list = %v, %v", l, err)
	}
	a, err := s.Create("Alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.ID, "brd-") || a.CreatedAt.IsZero() {
		t.Errorf("created %+v", a)
	}
	if _, err := os.Stat(filepath.Join(dir, a.ID+".toruboard")); err != nil {
		t.Fatalf("no file: %v", err)
	}
	if _, err := s.Create("   "); err == nil {
		t.Error("blank name accepted")
	}
	if _, err := s.Create(strings.Repeat("x", MaxNameLen+1)); err == nil {
		t.Error("long name accepted")
	}
	b := sample(t, s)

	list, err := s.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].ID != b.ID || list[0].Blocks != 2 || list[1].ID != a.ID {
		t.Errorf("list order or counts = %+v", list)
	}

	if _, err := s.Rename(a.ID, "  Beta  ", Agent("claude-code")); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewStore().Load(a.ID); got.Name != "Beta" {
		t.Errorf("rename not saved: %q", got.Name)
	}
	if _, err := s.Rename(a.ID, "", Person); err == nil {
		t.Error("blank rename accepted")
	}
	if _, err := s.Undo(a.ID, Person); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Load(a.ID); got.Name != "Alpha" {
		t.Errorf("undo rename: %q", got.Name)
	}

	// Save replaces a whole board, after checking it.
	b.Name = "Imported"
	b.Blocks[0].X = 999
	if _, err := s.Save(b); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewStore().Load(b.ID); got.Name != "Imported" || got.Blocks[0].X != 999 {
		t.Errorf("save not on disk: %+v", got)
	}
	if _, err := s.Undo(b.ID, Person); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("history after save = %v", err)
	}
	bad := b
	bad.Links = append([]Link{}, Link{ID: "lx", From: "a", To: "nope"})
	if _, err := s.Save(bad); err == nil {
		t.Error("saved a link to nothing")
	}

	if err := s.Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(a.ID); err == nil {
		t.Error("deleted twice")
	}
	if _, err := s.Load(a.ID); err == nil {
		t.Error("loaded a deleted board")
	}
	if list, _ := s.List(); len(list) != 1 {
		t.Errorf("list after delete = %+v", list)
	}
	for _, id := range []string{"../x", "", "a/b", `..\x`} {
		if _, err := s.Load(id); err == nil {
			t.Errorf("loaded id %q", id)
		}
		if err := s.Delete(id); err == nil {
			t.Errorf("deleted id %q", id)
		}
	}
}

func TestOpApply(t *testing.T) {
	s, _ := setup(t)
	b := sample(t, s)
	bot := Agent("claude-code")

	// An agent changes a person's block: createdBy stays, updatedBy is the agent.
	moved := b.Blocks[0]
	moved.X, moved.CreatedBy = 50, bot
	op, err := s.Apply(b.ID, Op{Source: bot, Blocks: []Block{moved}})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.Marshal(op)
	t.Logf("an agent moves a person's block; the op as applied:\n%s", j)
	if op.Rev != 2 || op.Kind != KindChange || op.Source != bot || op.At.IsZero() {
		t.Errorf("applied op = %+v", op)
	}
	got, _ := s.Load(b.ID)
	if x := got.Blocks[0]; x.X != 50 || x.CreatedBy != Person || x.UpdatedBy != bot {
		t.Errorf("after move = %+v", x)
	}

	// New items without ids get one; they go on top.
	op, err = s.Apply(b.ID, Op{Source: bot, Blocks: []Block{{Type: "sticky", W: 100, H: 100, Props: props(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	newID := op.Blocks[0].ID
	if !strings.HasPrefix(newID, "blk-") || op.Pos[newID] != 2 {
		t.Errorf("new block op = %+v", op)
	}

	// Pos restacks: bring the new block to the back.
	if _, err := s.Apply(b.ID, Op{Source: Person, Blocks: []Block{got.Blocks[1]}, Pos: map[string]int{"b": 0}}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Load(b.ID)
	if ids := blockIDs(got); !reflect.DeepEqual(ids, []string{"b", "a", newID}) {
		t.Errorf("stack after pos = %v", ids)
	}

	// Deleting a frame keeps its blocks and ink, out of the frame; deleting
	// a block takes its links.
	op, err = s.Apply(b.ID, Op{Source: Person, Delete: []string{"f1", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	got, _ = s.Load(b.ID)
	if len(got.Frames) != 0 || len(got.Links) != 0 || len(got.Blocks) != 2 || got.Blocks[0].Frame != "" || got.Ink[0].Frame != "" {
		t.Errorf("after delete = %+v", got)
	}
	if !reflect.DeepEqual(op.Delete, []string{"f1", "b", "l1"}) {
		t.Errorf("applied delete = %v", op.Delete)
	}

	// Ops that fail change nothing.
	before, _ := s.Load(b.ID)
	for name, bad := range map[string]Op{
		"no source":     {Blocks: []Block{{Type: "sticky", Props: props(`{}`)}}},
		"bad source":    {Source: "robot", Blocks: []Block{{Type: "sticky", Props: props(`{}`)}}},
		"empty agent":   {Source: "agent:", Blocks: []Block{{Type: "sticky", Props: props(`{}`)}}},
		"empty":         {Source: Person},
		"kind":          {Source: Person, Kind: KindUndo, Name: new(string)},
		"no props":      {Source: Person, Blocks: []Block{{Type: "sticky"}}},
		"array props":   {Source: Person, Blocks: []Block{{Type: "sticky", Props: props(`[1]`)}}},
		"bad type":      {Source: Person, Blocks: []Block{{Type: "Sticky!", Props: props(`{}`)}}},
		"negative size": {Source: Person, Blocks: []Block{{Type: "sticky", W: -1, Props: props(`{}`)}}},
		"no frame":      {Source: Person, Blocks: []Block{{Type: "sticky", Frame: "gone", Props: props(`{}`)}}},
		"dangling link": {Source: Person, Links: []Link{{From: "a", To: "gone"}}},
		"self link":     {Source: Person, Links: []Link{{From: "a", To: "a"}}},
		"kind clash":    {Source: Person, Frames: []Frame{{ID: "a", Name: "x"}}},
		"twice":         {Source: Person, Blocks: []Block{{ID: "z", Type: "sticky", Props: props(`{}`)}, {ID: "z", Type: "sticky", Props: props(`{}`)}}},
		"delete none":   {Source: Person, Delete: []string{"nope"}},
		"bad stroke":    {Source: Person, Ink: []Stroke{{Points: []float64{1, 2}, Color: "#fff", Width: 1}}},
		"bad pressure":  {Source: Person, Ink: []Stroke{{Points: []float64{1, 2, 3}, Color: "#fff", Width: 1}}},
		// The second half fails, so the first half must not land either.
		"half": {Source: Person, Blocks: []Block{{ID: "ok", Type: "sticky", Props: props(`{}`)}}, Links: []Link{{From: "ok", To: "gone"}}},
	} {
		if _, err := s.Apply(b.ID, bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	after, _ := s.Load(b.ID)
	if !reflect.DeepEqual(before, after) {
		t.Error("a failed op changed the board")
	}
	if disk, _ := NewStore().Load(b.ID); !reflect.DeepEqual(disk, after) {
		t.Error("disk and memory differ")
	}

	// The block catalog's validator is consulted.
	old := ValidateProps
	ValidateProps = func(typ string, p json.RawMessage) error {
		if typ == "poll" && !strings.Contains(string(p), "question") {
			return errors.New("a poll needs a question")
		}
		return nil
	}
	t.Cleanup(func() { ValidateProps = old })
	if _, err := s.Apply(b.ID, Op{Source: Person, Blocks: []Block{{Type: "poll", Props: props(`{}`)}}}); err == nil || !strings.Contains(err.Error(), "needs a question") {
		t.Errorf("catalog check = %v", err)
	}
}

func blockIDs(b Board) []string {
	var ids []string
	for _, x := range b.Blocks {
		ids = append(ids, x.ID)
	}
	return ids
}

func TestUndoRedo(t *testing.T) {
	s, _ := setup(t)
	b := sample(t, s)
	s = NewStore() // a history that starts at the sample board
	bot := Agent("claude-code")
	states := []Board{b}

	step := func(op Op) {
		t.Helper()
		if _, err := s.Apply(b.ID, op); err != nil {
			t.Fatal(err)
		}
		got, _ := s.Load(b.ID)
		states = append(states, got)
	}
	m := b.Blocks[1]
	m.X, m.Props = 400, props(`{"question":"Which one?","options":["A","B","C"]}`)
	step(Op{Source: bot, Blocks: []Block{m}})
	step(Op{Source: Person, Blocks: []Block{{ID: "c", Type: "sticky", W: 1, H: 1, Props: props(`{"text":"new"}`)}}, Links: []Link{{ID: "l2", From: "c", To: "a"}}})
	step(Op{Source: Person, Blocks: []Block{b.Blocks[0]}, Pos: map[string]int{"a": 2}}) // restack
	step(Op{Source: bot, Delete: []string{"f1", "a"}})                                  // cascades: links, frame refs
	n := "Renamed"
	step(Op{Source: bot, Name: &n})

	same := func(want, got Board) bool {
		want.Rev, got.Rev = 0, 0
		want.UpdatedAt, got.UpdatedAt = time.Time{}, time.Time{}
		return reflect.DeepEqual(want, got)
	}
	// Undo walks back through every state, whoever made the change.
	for i := len(states) - 2; i >= 0; i-- {
		op, err := s.Undo(b.ID, Person)
		if i == len(states)-2 {
			j, _ := json.Marshal(op)
			t.Logf("undo of the agent's rename, asked by the person:\n%s", j)
		}
		if err != nil {
			t.Fatalf("undo to %d: %v", i, err)
		}
		if op.Kind != KindUndo || op.Source != Person {
			t.Errorf("undo op = %+v", op)
		}
		if got, _ := s.Load(b.ID); !same(states[i], got) {
			t.Fatalf("undo to state %d:\n got %+v\nwant %+v", i, got, states[i])
		}
	}
	if _, err := s.Undo(b.ID, Person); !errors.Is(err, ErrNothingToUndo) {
		t.Errorf("undo past the start = %v", err)
	}
	// Redo walks forward again, keeping who made each item.
	for i := 1; i < len(states); i++ {
		op, err := s.Redo(b.ID, bot)
		if err != nil {
			t.Fatalf("redo to %d: %v", i, err)
		}
		if op.Kind != KindRedo {
			t.Errorf("redo op kind = %q", op.Kind)
		}
		if got, _ := s.Load(b.ID); !same(states[i], got) {
			t.Fatalf("redo to state %d:\n got %+v\nwant %+v", i, got, states[i])
		}
	}
	if _, err := s.Redo(b.ID, Person); !errors.Is(err, ErrNothingToRedo) {
		t.Errorf("redo past the end = %v", err)
	}
	// Rev only goes up, and every step was saved.
	got, _ := s.Load(b.ID)
	if got.Rev != 1+5+5+5 {
		t.Errorf("rev = %d", got.Rev)
	}
	if disk, _ := NewStore().Load(b.ID); !reflect.DeepEqual(disk, got) {
		t.Error("disk and memory differ after undo/redo")
	}
	// A new change clears redo.
	_, _ = s.Undo(b.ID, Person)
	step(Op{Source: Person, Name: &n})
	if _, err := s.Redo(b.ID, Person); !errors.Is(err, ErrNothingToRedo) {
		t.Errorf("redo after a new change = %v", err)
	}
}

// The op Apply returns, sent to another copy of the board, makes the same
// change: that is how the board window will follow.
func TestAppliedOpReplays(t *testing.T) {
	s, _ := setup(t)
	b := sample(t, s)
	mirror := NewDoc(b)
	ops := []Op{
		{Source: Person, Blocks: []Block{{Type: "sticky", Props: props(`{}`)}}, Pos: nil},
		{Source: Agent("x"), Delete: []string{"f1"}},
		{Source: Person, Blocks: []Block{b.Blocks[1]}, Pos: map[string]int{"b": 0}},
	}
	for _, op := range ops {
		applied, err := s.Apply(b.ID, op)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := mirror.apply(applied, KindChange, true); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := s.Undo(b.ID, Person)
	if _, _, err := mirror.apply(u, KindUndo, true); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Load(b.ID)
	m := mirror.Board()
	m.UpdatedAt, got.UpdatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(m, got) {
		t.Errorf("mirror differs:\n got %+v\nwant %+v", m, got)
	}
}

func TestCorruptFile(t *testing.T) {
	s, dir := setup(t)
	good := sample(t, s)
	data, _ := os.ReadFile(filepath.Join(dir, good.ID+Ext))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"brd-trunc":   string(data[:len(data)/2]),
		"brd-garbage": "\x00\x01not json at all",
		"brd-empty":   "",
		"brd-format":  `{"format":"other","version":1}`,
		"brd-newer":   strings.Replace(string(data), `"version": 1`, `"version": 7`, 1),
		"brd-dangle":  strings.Replace(string(data), `"to": "b"`, `"to": "zzz"`, 1),
		"brd-wrongid": string(data), // its file says it's another board
		"brd-trail":   string(data) + "{}",
		"brd-negsize": strings.Replace(string(data), `"w": 200`, `"w": -5`, 1),
	}
	for id, body := range cases {
		if err := os.WriteFile(filepath.Join(dir, id+Ext), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fresh := NewStore()
	for id := range cases {
		if _, err := fresh.Load(id); err == nil {
			t.Errorf("%s: loaded a damaged board", id)
		} else {
			t.Logf("%-12s refused: %v", id, err)
		}
		if _, err := fresh.Apply(id, Op{Source: Person, Name: new(string)}); err == nil {
			t.Errorf("%s: changed a damaged board", id)
		}
	}
	if _, err := fresh.Load("brd-newer"); err == nil || !strings.Contains(err.Error(), "newer Toru") {
		t.Errorf("newer file error = %v", err)
	}
	// Damaged boards stay in the list, marked, and the good one still loads.
	list, err := fresh.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(cases)+1 {
		t.Fatalf("list = %+v", list)
	}
	damaged := 0
	for _, l := range list {
		if l.Error != "" {
			damaged++
		} else if l.ID != good.ID {
			t.Errorf("listed %s as fine", l.ID)
		}
	}
	if damaged != len(cases) {
		t.Errorf("%d marked damaged, want %d", damaged, len(cases))
	}
	if _, err := fresh.Load(good.ID); err != nil {
		t.Errorf("good board: %v", err)
	}
	// A damaged file is never rewritten.
	after, _ := os.ReadFile(filepath.Join(dir, "brd-trunc"+Ext))
	if string(after) != cases["brd-trunc"] {
		t.Error("damaged file was changed")
	}
}

func TestSource(t *testing.T) {
	for _, s := range []Source{Person, Agent("claude-code"), Agent("Claude Desktop 1.2"), "agent:cursor"} {
		if err := s.Check(); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []Source{"", "people", "agent:", "agent: x", "Person", "agent:a\nb", Source("agent:" + strings.Repeat("a", 65))} {
		if err := s.Check(); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
	if !Agent("x").IsAgent() || Person.IsAgent() {
		t.Error("IsAgent")
	}
}
