package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

func TestBoardImageAgentCommand(t *testing.T) {
	root := t.TempDir()
	old := board.Dir
	board.Dir = func() (string, error) { return root, nil }
	t.Cleanup(func() { board.Dir = old })

	const asset = "334fac5220a72cdbb351ea83b8e4a5d48d7bb81d438ba78bf94cd863669c346b.png"
	pic, err := filepath.Abs(filepath.Join("internal", "board", "testdata", "images", asset))
	if err != nil {
		t.Fatal(err)
	}
	reg := agentctl.NewRegistry()
	registerBoardImageAgentCommands(reg, board.NewImageService(func() []board.LibraryItem {
		return []board.LibraryItem{{ID: "shot1", Label: "Screenshot", Path: pic}}
	}))
	call := func(in string) (string, error) {
		res, err := reg.Call(context.Background(), "board.image.import", json.RawMessage(in))
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(res)
		return string(b), nil
	}

	for _, in := range []string{`{"path":` + jsonStr(pic) + `}`, `{"libraryId":"shot1"}`} {
		out, err := call(in)
		if err != nil || !strings.Contains(out, `"asset":"`+asset+`"`) || !strings.Contains(out, `"width":1280`) {
			t.Errorf("%s = %s, %v", in, out, err)
		}
	}
	for _, in := range []string{`{}`, `{"libraryId":"nope"}`, `{"path":"x","libraryId":"shot1"}`, `{"path":` + jsonStr(filepath.Join(root, "missing.png")) + `}`} {
		if out, err := call(in); err == nil {
			t.Errorf("%s = %s, want an error", in, out)
		}
	}
	// What it returns is what an image block's props take.
	if err := board.CheckProps("image", json.RawMessage(`{"asset":"`+asset+`","alt":"Test card"}`)); err != nil {
		t.Fatal(err)
	}
}

// An agent sees every catalog type in board.catalog and can place each one:
// the window's sample board, built through the board commands, picture and all.
func TestAgentPlacesEveryBlockType(t *testing.T) {
	reg, _ := newBoardRegistry(t)
	registerBoardImageAgentCommands(reg, board.NewImageService(nil))

	var names []string
	for _, x := range call(t, reg, "t", "board.catalog", `{}`)["types"].([]any) {
		names = append(names, x.(map[string]any)["type"].(string))
	}
	if got := strings.Join(names, ","); got != "card,checklist,frame,image,poll,rating,sticky,table,tag-list,text" {
		t.Fatalf("board.catalog types = %s", got)
	}

	data, err := os.ReadFile(filepath.Join("frontend", "src", "whiteboard", "sample-board.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sample struct {
		Frames []json.RawMessage `json:"frames"`
		Blocks []map[string]any  `json:"blocks"`
	}
	if err := json.Unmarshal(data, &sample); err != nil {
		t.Fatal(err)
	}
	pic, _ := filepath.Abs(filepath.Join("internal", "board", "testdata", "images", "334fac5220a72cdbb351ea83b8e4a5d48d7bb81d438ba78bf94cd863669c346b.png"))
	asset := call(t, reg, "t", "board.image.import", `{"path":`+jsonStr(pic)+`}`)["asset"].(string)
	for _, b := range sample.Blocks {
		if p := b["props"].(map[string]any); p["asset"] != nil {
			p["asset"] = asset
		}
	}
	id := call(t, reg, "t", "board.create", `{"name":"Every block"}`)["id"].(string)
	in, _ := json.Marshal(map[string]any{"id": id, "frames": sample.Frames, "blocks": sample.Blocks})
	call(t, reg, "t", "board.add", string(in))

	md := call(t, reg, "t", "board.read", `{"id":"`+id+`","format":"markdown"}`)["markdown"].(string)
	for _, want := range []string{"Rating: How useful is this board?: 4/5", "| Catalog | Beetle | Fri |", "Themes: speed, agents", "Image: Test card screenshot"} {
		if !strings.Contains(md, want) {
			t.Errorf("outline lacks %q:\n%s", want, md)
		}
	}
	if msg := callErr(t, reg, "board.add", `{"id":"`+id+`","blocks":[{"type":"frame","props":{"name":"Not a block"}}]}`); !strings.Contains(msg, "frames") {
		t.Errorf("frame as a block: %s", msg)
	}
}
