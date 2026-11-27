package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/StephenSHorton/toru/internal/board"
)

// newWhiteboards is the Whiteboards service over boards in a temporary
// folder, with one Library screenshot ("shot1", 1280×800), noting the
// boards it opens and the events it sends.
func newWhiteboards(t *testing.T) (*WhiteboardsService, *[]string, *[]string) {
	t.Helper()
	root := t.TempDir()
	old := board.Dir
	board.Dir = func() (string, error) { return root, nil }
	t.Cleanup(func() { board.Dir = old })
	pic, err := filepath.Abs(filepath.Join("internal", "board", "testdata", "images",
		"334fac5220a72cdbb351ea83b8e4a5d48d7bb81d438ba78bf94cd863669c346b.png"))
	if err != nil {
		t.Fatal(err)
	}
	opened, events := &[]string{}, &[]string{}
	s := &WhiteboardsService{
		boards: board.NewStore(),
		images: board.NewImageService(func() []board.LibraryItem {
			return []board.LibraryItem{{ID: "shot1", Label: "Screenshot 1", Path: pic}}
		}),
		open: func(id string) { *opened = append(*opened, id) },
		emit: func(name string, _ any) { *events = append(*events, name) },
	}
	return s, opened, events
}

func TestWhiteboardsCreateNamesAndOpens(t *testing.T) {
	s, opened, events := newWhiteboards(t)
	var names []string
	for _, in := range []string{"", "", "Sprint plan", ""} {
		b, err := s.Create(in)
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, b.Name)
	}
	want := []string{"Untitled whiteboard", "Untitled whiteboard 2", "Sprint plan", "Untitled whiteboard 3"}
	if !slices.Equal(names, want) {
		t.Errorf("names = %q, want %q", names, want)
	}
	if len(*opened) != 4 || len(*events) != 4 || (*events)[0] != evBoardsChanged {
		t.Errorf("opened %v, events %v: want each board opened and boards:changed sent", *opened, *events)
	}
	list, err := s.List()
	if err != nil || len(list) != 4 {
		t.Fatalf("List = %v, %v", list, err)
	}
}

// Importing a board exported from this computer keeps both: the import
// gets a new id and never replaces the board it came from.
func TestWhiteboardsImportNeverReplaces(t *testing.T) {
	s, opened, _ := newWhiteboards(t)
	orig, err := s.Create("Retro")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.boards.Load(orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, err := board.Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "retro"+board.Ext)
	if err := os.WriteFile(file, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Give the original a change the file doesn't have.
	if _, err := s.SendScreenshot(orig.ID, "shot1"); err != nil {
		t.Fatal(err)
	}

	imp, err := s.Import(file)
	if err != nil {
		t.Fatal(err)
	}
	if imp.ID == orig.ID || imp.Name != "Retro" {
		t.Errorf("import = %+v: want a new id, name kept", imp)
	}
	if kept, _ := s.boards.Load(orig.ID); len(kept.Blocks) != 1 {
		t.Errorf("original board has %d blocks after the import, want its 1", len(kept.Blocks))
	}
	if list, _ := s.List(); len(list) != 2 {
		t.Errorf("%d boards after import, want 2", len(list))
	}
	if (*opened)[len(*opened)-1] != imp.ID {
		t.Errorf("the imported board wasn't opened")
	}
	// A file read straight in (fresh store, as after a restart) clashes on disk too.
	s.boards = board.NewStore()
	if again, err := s.Import(file); err != nil || again.ID == orig.ID || again.ID == imp.ID {
		t.Errorf("second import = %+v, %v: want a third id", again, err)
	}

	bad := filepath.Join(t.TempDir(), "bad"+board.Ext)
	_ = os.WriteFile(bad, []byte(`{"hello":1}`), 0o644)
	if _, err := s.Import(bad); err == nil {
		t.Error("importing a file that isn't a board worked")
	}
}

func TestWhiteboardsSendScreenshot(t *testing.T) {
	s, opened, _ := newWhiteboards(t)
	dest, err := s.Create("Bugs")
	if err != nil {
		t.Fatal(err)
	}
	var ops []board.Op
	s.boards.OnOp(func(_ string, op board.Op) { ops = append(ops, op) })

	for range 2 {
		if _, err := s.SendScreenshot(dest.ID, "shot1"); err != nil {
			t.Fatal(err)
		}
	}
	b, err := s.boards.Load(dest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Blocks) != 2 {
		t.Fatalf("%d blocks, want 2", len(b.Blocks))
	}
	first, second := b.Blocks[0], b.Blocks[1]
	for _, x := range b.Blocks {
		t.Logf("board %q (%s): %s block %s at (%v, %v), %v×%v, by %s, props %s", b.Name, b.ID, x.Type, x.ID, x.X, x.Y, x.W, x.H, x.CreatedBy, x.Props)
	}
	var props struct{ Asset, Alt string }
	_ = json.Unmarshal(first.Props, &props)
	if first.Type != "image" || props.Asset == "" || props.Alt != "Screenshot 1" {
		t.Errorf("block = %+v %s: want an image block of the screenshot", first, first.Props)
	}
	// 1280×800 lands 480 wide, its shape kept, and the second beside the first.
	if first.W != 480 || first.H != 300 {
		t.Errorf("size %v×%v, want 480×300", first.W, first.H)
	}
	if second.X == first.X && second.Y == first.Y {
		t.Error("the second screenshot landed on top of the first")
	}
	if len(ops) != 2 || ops[0].Source != board.Person {
		t.Errorf("ops = %+v: want two changes by the person, sent to open windows", ops)
	}
	if (*opened)[len(*opened)-1] != dest.ID {
		t.Error("the board wasn't opened")
	}

	// No board chosen: a new one is made for it.
	made, err := s.SendScreenshot("", "shot1")
	if err != nil || made.ID == dest.ID || made.Blocks != 1 {
		t.Errorf("send to a new board = %+v, %v", made, err)
	}
	// A screenshot that isn't in the Library, or a board that doesn't exist,
	// changes nothing and makes nothing.
	if _, err := s.SendScreenshot(dest.ID, "nope"); err == nil {
		t.Error("sending a missing screenshot worked")
	}
	if _, err := s.SendScreenshot("brd-missing", "shot1"); err == nil {
		t.Error("sending to a missing board worked")
	}
	if list, _ := s.List(); len(list) != 2 {
		t.Errorf("%d boards, want 2", len(list))
	}
}
