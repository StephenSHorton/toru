package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// boardCommands is every board command docs/whiteboard.md section 2 lists.
var boardCommands = []string{
	"board.list", "board.create", "board.open", "board.read", "board.catalog",
	"board.add", "board.update", "board.move", "board.group",
	"board.connect", "board.disconnect", "board.layout", "board.delete", "board.export",
}

type boardEvents struct {
	mu     sync.Mutex
	names  []string
	opened []string
}

// newBoardRegistry is a registry with the board commands over a store in a
// temporary folder.
func newBoardRegistry(t *testing.T) (*agentctl.Registry, *boardEvents) {
	t.Helper()
	dir := t.TempDir()
	old := board.Dir
	board.Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { board.Dir = old })
	ev := &boardEvents{}
	reg := agentctl.NewRegistry()
	store := board.NewStore()
	// As main.go wires it: the store sends every change as board:op.
	store.OnOp(func(id string, op board.Op) {
		ev.mu.Lock()
		ev.names = append(ev.names, evBoardOp)
		ev.mu.Unlock()
	})
	registerBoardAgentCommands(reg, boardAgentDeps{
		boards: store,
		open: func(id string) {
			ev.mu.Lock()
			ev.opened = append(ev.opened, id)
			ev.mu.Unlock()
		},
		emit: func(name string, _ any) {
			ev.mu.Lock()
			ev.names = append(ev.names, name)
			ev.mu.Unlock()
		},
	})
	return reg, ev
}

// call runs a command as caller and returns its result as JSON-decoded data.
func call(t *testing.T, reg *agentctl.Registry, caller, name, input string) map[string]any {
	t.Helper()
	res, err := reg.Call(agentctl.WithCaller(context.Background(), caller), name, json.RawMessage(input))
	if err != nil {
		t.Fatalf("%s %s: %v", name, input, err)
	}
	b, _ := json.Marshal(res)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func callErr(t *testing.T, reg *agentctl.Registry, name, input string) string {
	t.Helper()
	_, err := reg.Call(agentctl.WithCaller(context.Background(), "test"), name, json.RawMessage(input))
	if err == nil {
		t.Fatalf("%s %s: no error", name, input)
	}
	return err.Error()
}

func readBoard(t *testing.T, reg *agentctl.Registry, id string) board.Board {
	t.Helper()
	res, err := reg.Call(context.Background(), "board.read", json.RawMessage(`{"id":"`+id+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	return res.(map[string]any)["board"].(board.Board)
}

func TestBoardCommandsRegisteredWithSchemas(t *testing.T) {
	reg, _ := newBoardRegistry(t)
	infos := map[string]agentctl.CommandInfo{}
	for _, c := range reg.Infos() {
		infos[c.Name] = c
	}
	for _, name := range boardCommands {
		c, ok := infos[name]
		if !ok {
			t.Errorf("%s isn't registered", name)
			continue
		}
		var s map[string]any
		if err := json.Unmarshal(c.Input, &s); err != nil || s["type"] != "object" {
			t.Errorf("%s: input isn't an object schema: %s", name, c.Input)
		}
		if c.Description == "" {
			t.Errorf("%s has no description", name)
		}
		var props []string
		for k := range s["properties"].(map[string]any) {
			props = append(props, k)
		}
		sort.Strings(props)
		fmt.Printf("%-17s destructive=%-5v input: %s\n", name, c.Destructive, strings.Join(props, ", "))
	}
	if len(infos) != len(boardCommands) {
		t.Errorf("registered %d commands, want %d", len(infos), len(boardCommands))
	}
}

func TestBoardDeleteNeedsConfirm(t *testing.T) {
	reg, _ := newBoardRegistry(t)
	for _, c := range reg.Infos() {
		if c.Name == "board.delete" && !c.Destructive {
			t.Fatal("board.delete isn't marked destructive")
		}
	}
	id := call(t, reg, "test", "board.create", `{"name":"Doomed"}`)["id"].(string)
	blk := call(t, reg, "test", "board.add", `{"id":"`+id+`","blocks":[{"type":"sticky","props":{"text":"x"}}]}`)["blocks"].([]any)[0].(string)

	if msg := callErr(t, reg, "board.delete", `{"id":"`+id+`","items":["`+blk+`"]}`); !strings.Contains(msg, `"confirm": true`) {
		t.Fatalf("items without confirm: %s", msg)
	}
	if msg := callErr(t, reg, "board.delete", `{"id":"`+id+`"}`); !strings.Contains(msg, `"confirm": true`) {
		t.Fatalf("board without confirm: %s", msg)
	}
	// An empty list is refused, never taken as "the whole board".
	if msg := callErr(t, reg, "board.delete", `{"id":"`+id+`","items":[],"confirm":true}`); !strings.Contains(msg, `"items" is empty`) {
		t.Fatalf("empty items: %s", msg)
	}
	if got := len(readBoard(t, reg, id).Blocks); got != 1 {
		t.Fatalf("blocks after refused delete = %d", got)
	}
	call(t, reg, "test", "board.delete", `{"id":"`+id+`","items":["`+blk+`"],"confirm":true}`)
	if got := len(readBoard(t, reg, id).Blocks); got != 0 {
		t.Fatalf("blocks after delete = %d", got)
	}
	call(t, reg, "test", "board.delete", `{"id":"`+id+`","confirm":true}`)
	if msg := callErr(t, reg, "board.read", `{"id":"`+id+`"}`); !strings.Contains(msg, "no board") {
		t.Fatalf("read deleted board: %s", msg)
	}
}

func TestBoardChangesTaggedWithCaller(t *testing.T) {
	reg, ev := newBoardRegistry(t)
	id := call(t, reg, "claude-code", "board.create", `{"name":"Tags"}`)["id"].(string)
	a := call(t, reg, "claude-code", "board.add", `{"id":"`+id+`","blocks":[{"id":"a","type":"sticky","props":{"text":"A"}}]}`)
	if a["blocks"].([]any)[0] != "a" {
		t.Fatalf("add: %v", a)
	}
	call(t, reg, "Cursor: v1 ✨", "board.update", `{"id":"`+id+`","blocks":[{"id":"a","props":{"color":"pink"}}]}`)
	call(t, reg, "", "board.add", `{"id":"`+id+`","blocks":[{"id":"b","type":"text","props":{"text":"B"}}]}`)

	b := readBoard(t, reg, id)
	got := map[string]board.Block{}
	for _, x := range b.Blocks {
		got[x.ID] = x
	}
	if got["a"].CreatedBy != "agent:claude-code" || got["a"].UpdatedBy != "agent:Cursor- v1 -" {
		t.Errorf("a: created %q updated %q", got["a"].CreatedBy, got["a"].UpdatedBy)
	}
	if string(got["a"].Props) != `{"color":"pink","text":"A"}` {
		t.Errorf("merged props = %s", got["a"].Props)
	}
	if got["b"].CreatedBy != "agent:unknown" {
		t.Errorf("b: created %q", got["b"].CreatedBy)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if strings.Count(strings.Join(ev.names, ","), evBoardOp) != 3 {
		t.Errorf("events = %v", ev.names)
	}
}

func TestBoardCommandsBuildAndArrange(t *testing.T) {
	reg, ev := newBoardRegistry(t)
	id := call(t, reg, "t", "board.create", `{"name":"Plan","open":true}`)["id"].(string)
	if len(ev.opened) != 1 || ev.opened[0] != id {
		t.Fatalf("opened = %v", ev.opened)
	}
	// Bad props are refused against the catalog.
	if msg := callErr(t, reg, "board.add", `{"id":"`+id+`","blocks":[{"type":"poll","props":{"question":"Q","options":[{"label":"only"}]}}]}`); !strings.Contains(msg, "at least 2") {
		t.Fatalf("bad poll: %s", msg)
	}
	if msg := callErr(t, reg, "board.add", `{"id":"`+id+`","blocks":[{"type":"rocket","props":{}}]}`); !strings.Contains(msg, "board.catalog") {
		t.Fatalf("bad type: %s", msg)
	}
	call(t, reg, "t", "board.add", `{"id":"`+id+`","frames":[{"id":"f","name":"Ideas","x":0,"y":0,"w":900,"h":600}],
		"blocks":[
		{"id":"root","type":"text","props":{"text":"Goal","tags":["b"]},"frame":"f"},
		{"id":"c1","type":"sticky","props":{"text":"One","tags":["a"]}},
		{"id":"c2","type":"sticky","props":{"text":"Two","tags":["a"]}},
		{"id":"todo","type":"checklist","props":{"title":"Next","items":[{"text":"Ship","done":true},{"text":"Tell"}]}}]}`)
	links := call(t, reg, "t", "board.connect", `{"id":"`+id+`","links":[{"from":"root","to":"c1"},{"from":"root","to":"c2","label":"maybe"}]}`)["links"].([]any)
	if len(links) != 2 {
		t.Fatalf("links = %v", links)
	}
	b := readBoard(t, reg, id)
	if b.Blocks[1].Y != b.Blocks[2].Y || b.Blocks[2].X <= b.Blocks[1].X {
		t.Errorf("auto placement: %+v %+v", b.Blocks[1], b.Blocks[2])
	}
	// Blocks without x, y go below the frame made in the same call.
	if b.Blocks[1].Y < b.Frames[0].Y+b.Frames[0].H {
		t.Errorf("auto-placed block at y %v overlaps the frame (bottom %v)", b.Blocks[1].Y, b.Frames[0].Y+b.Frames[0].H)
	}

	// Group: moving one moves both.
	g := call(t, reg, "t", "board.group", `{"id":"`+id+`","blocks":["c1","c2"]}`)["group"].(string)
	before := readBoard(t, reg, id)
	call(t, reg, "t", "board.move", `{"id":"`+id+`","moves":[{"id":"c1","dx":10,"dy":-5}]}`)
	after := readBoard(t, reg, id)
	for i := 1; i <= 2; i++ {
		if after.Blocks[i].Group != g || after.Blocks[i].X != before.Blocks[i].X+10 || after.Blocks[i].Y != before.Blocks[i].Y-5 {
			t.Errorf("grouped move %d: %+v", i, after.Blocks[i])
		}
	}
	// Moving the frame takes the block in it.
	call(t, reg, "t", "board.move", `{"id":"`+id+`","moves":[{"id":"f","x":100,"y":50}]}`)
	after = readBoard(t, reg, id)
	if after.Frames[0].X != 100 || after.Blocks[0].X != before.Blocks[0].X+100 {
		t.Errorf("frame move: %+v %+v", after.Frames[0], after.Blocks[0])
	}

	// Tree: root, then c1 and c2 one level to the right.
	call(t, reg, "t", "board.layout", `{"id":"`+id+`","kind":"tree","blocks":["root","c1","c2"],"x":0,"y":0}`)
	tb := blockIndex(readBoard(t, reg, id))
	if tb["root"].X != 0 || tb["c1"].X <= tb["root"].X || tb["c1"].X != tb["c2"].X || tb["c2"].Y <= tb["c1"].Y {
		t.Errorf("tree: root %v,%v c1 %v,%v c2 %v,%v", tb["root"].X, tb["root"].Y, tb["c1"].X, tb["c1"].Y, tb["c2"].X, tb["c2"].Y)
	}
	// Cluster: tag a first, then b, then untagged.
	call(t, reg, "t", "board.layout", `{"id":"`+id+`","kind":"cluster","x":0,"y":0}`)
	cb := blockIndex(readBoard(t, reg, id))
	if cb["c1"].X != 0 || cb["c2"].X != 0 || cb["root"].X <= 0 || cb["todo"].X <= cb["root"].X {
		t.Errorf("cluster: c1 %v c2 %v root %v todo %v", cb["c1"].X, cb["c2"].X, cb["root"].X, cb["todo"].X)
	}
	call(t, reg, "t", "board.layout", `{"id":"`+id+`","kind":"grid","columns":2,"x":0,"y":0}`)

	// Disconnect by ends.
	call(t, reg, "t", "board.disconnect", `{"id":"`+id+`","from":"root","to":"c2"}`)
	if n := len(readBoard(t, reg, id).Links); n != 1 {
		t.Errorf("links after disconnect = %d", n)
	}

	md := call(t, reg, "t", "board.read", `{"id":"`+id+`","format":"markdown"}`)["markdown"].(string)
	for _, want := range []string{"# Plan", "## Ideas", "- Goal #b", "  - [x] Ship", "  - [ ] Tell", "- Goal → One"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}

	// Export: an existing file is only replaced with confirm.
	out := filepath.Join(t.TempDir(), "plan")
	p := call(t, reg, "t", "board.export", `{"id":"`+id+`","format":"toruboard","path":`+jsonString(out)+`}`)["path"].(string)
	data, err := os.ReadFile(p)
	if err != nil || filepath.Ext(p) != board.Ext {
		t.Fatalf("export: %s %v", p, err)
	}
	if back, err := board.Decode(data); err != nil || len(back.Blocks) != 4 {
		t.Fatalf("exported board reads back: %v", err)
	}
	if msg := callErr(t, reg, "board.export", `{"id":"`+id+`","format":"toruboard","path":`+jsonString(out)+`}`); !strings.Contains(msg, "confirm") {
		t.Fatalf("overwrite without confirm: %s", msg)
	}
	call(t, reg, "t", "board.export", `{"id":"`+id+`","format":"toruboard","path":`+jsonString(out)+`,"confirm":true}`)
	if msg := callErr(t, reg, "board.export", `{"id":"`+id+`","format":"png","path":`+jsonString(out)+`}`); !strings.Contains(msg, "isn't built yet") {
		t.Fatalf("png: %s", msg)
	}

	// Rename through update, and the list shows it.
	call(t, reg, "t", "board.update", `{"id":"`+id+`","name":"Plan v2"}`)
	boards := call(t, reg, "t", "board.list", `{}`)["boards"].([]any)
	if len(boards) != 1 || boards[0].(map[string]any)["name"] != "Plan v2" {
		t.Fatalf("list = %v", boards)
	}
	if types := call(t, reg, "t", "board.catalog", `{}`)["types"].([]any); len(types) < 4 {
		t.Fatalf("catalog = %v", types)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
