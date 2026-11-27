package main

import (
	"context"
	"encoding/json"
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
