package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// newBlockRegistry is newBoardRegistry with a block library in a temporary
// folder too, so the board.block commands are registered.
func newBlockRegistry(t *testing.T) (*agentctl.Registry, *board.Store, string) {
	t.Helper()
	dir := t.TempDir()
	oldB, oldL := board.Dir, board.BlocksDir
	board.Dir = func() (string, error) { return filepath.Join(dir, "boards"), nil }
	board.BlocksDir = func() (string, error) { return filepath.Join(dir, "blocks"), nil }
	t.Cleanup(func() { board.Dir, board.BlocksDir = oldB, oldL })
	reg := agentctl.NewRegistry()
	store := board.NewStore()
	registerBoardAgentCommands(reg, boardAgentDeps{boards: store, blocks: board.NewLibrary()})
	return reg, store, filepath.Join(dir, "blocks")
}

// sampleBlockInputs are internal/board/testdata/custom-blocks.json's two
// block types, as board.block.define inputs.
func sampleBlockInputs(t *testing.T) []json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("internal", "board", "testdata", "custom-blocks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Blocks []json.RawMessage `json:"blocks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	return f.Blocks
}

func TestBoardBlockCommands(t *testing.T) {
	reg, store, dir := newBlockRegistry(t)
	in := sampleBlockInputs(t)
	have := map[string]bool{}
	for _, c := range reg.List() {
		have[c.Name] = true
	}
	for _, name := range []string{"board.block.define", "board.block.list", "board.block.update", "board.block.remove", "board.block.save"} {
		if !have[name] {
			t.Fatalf("%s isn't registered", name)
		}
	}

	out := call(t, reg, "Claude Code", "board.block.define", string(in[0]))
	if out["type"] != "feature-idea" || out["version"] != 1.0 {
		t.Fatalf("define: %v", out)
	}
	call(t, reg, "Claude Code", "board.block.define", string(in[1]))
	if _, err := os.Stat(filepath.Join(dir, "bug-report.json")); err != nil {
		t.Fatal(err)
	}
	if msg := callErr(t, reg, "board.block.define", string(in[0])); !strings.Contains(msg, "already has") {
		t.Fatalf("define twice: %s", msg)
	}
	if msg := callErr(t, reg, "board.block.define", `{"type":"evil","label":"Evil","icon":"bug","w":200,"h":200,"props":{"type":"object","additionalProperties":false,"properties":{}},"defaults":{},"layout":{"part":"html","html":"<img src=x onerror=alert(1)>"}}`); !strings.Contains(msg, `no part "html"`) {
		t.Fatalf("a markup layout: %s", msg)
	}

	// board.catalog lists them under custom, with the parts and icons.
	cat := call(t, reg, "Claude Code", "board.catalog", `{}`)
	custom, _ := cat["custom"].([]any)
	if len(custom) != 2 || cat["parts"] == nil || cat["icons"] == nil {
		t.Fatalf("catalog custom = %v", custom)
	}
	if call(t, reg, "Claude Code", "board.catalog", ``)["types"] == nil {
		t.Fatal("board.catalog without input")
	}

	// board.add places one: defaults fill in, the board keeps a copy.
	id := call(t, reg, "Claude Code", "board.create", `{"name":"Roadmap"}`)["id"].(string)
	add := call(t, reg, "Claude Code", "board.add", `{"id":"`+id+`","blocks":[
		{"id":"idea1","type":"feature-idea","props":{"title":"Dark mode","votes":3}},
		{"id":"bug1","type":"bug-report","props":{"title":"Crash on paste","severity":"high","steps":[{"text":"Copy an image"},{"text":"Paste it twice"}]}}]}`)
	if copied, _ := add["typesCopied"].([]any); len(copied) != 2 {
		t.Fatalf("typesCopied = %v", add["typesCopied"])
	}
	b, _ := store.Load(id)
	if b.BoardType("feature-idea") == nil || b.BoardType("bug-report") == nil {
		t.Fatalf("board types = %+v", b.Types)
	}
	var p map[string]any
	_ = json.Unmarshal(b.Blocks[0].Props, &p)
	if p["title"] != "Dark mode" || p["votes"] != 3.0 || p["status"] != "idea" || b.Blocks[0].W != 340 {
		t.Fatalf("idea props %v, w %v", p, b.Blocks[0].W)
	}
	if msg := callErr(t, reg, "board.add", `{"id":"`+id+`","blocks":[{"type":"bug-report","props":{"severity":"apocalyptic"}}]}`); !strings.Contains(msg, "must be one of") {
		t.Fatalf("bad custom props: %s", msg)
	}
	if msg := callErr(t, reg, "board.add", `{"id":"`+id+`","blocks":[{"type":"no-such-block","props":{}}]}`); !strings.Contains(msg, "board.block.define") {
		t.Fatalf("unknown type: %s", msg)
	}

	// board.update checks custom props against the board's copy.
	call(t, reg, "Claude Code", "board.update", `{"id":"`+id+`","blocks":[{"id":"idea1","props":{"votes":4,"status":"planned"}}]}`)
	if msg := callErr(t, reg, "board.update", `{"id":"`+id+`","blocks":[{"id":"idea1","props":{"votes":-1}}]}`); !strings.Contains(msg, "at least") {
		t.Fatalf("bad update: %s", msg)
	}

	// A new version in the library reaches the board on the next add, when
	// the blocks there still fit it.
	up := call(t, reg, "Claude Code", "board.block.update", `{"type":"feature-idea","label":"Feature request"}`)
	if up["version"] != 2.0 {
		t.Fatalf("update: %v", up)
	}
	call(t, reg, "Claude Code", "board.add", `{"id":"`+id+`","blocks":[{"type":"feature-idea","props":{"title":"Offline mode"}}]}`)
	b, _ = store.Load(id)
	if d := b.BoardType("feature-idea"); d == nil || d.Version != 2 || d.Label != "Feature request" {
		t.Fatalf("board copy after update: %+v", d)
	}
	if md := call(t, reg, "Claude Code", "board.read", `{"id":"`+id+`","format":"markdown"}`)["markdown"].(string); !strings.Contains(md, "Feature request: Dark mode") || !strings.Contains(md, "Bug report: Crash on paste") {
		t.Fatalf("markdown:\n%s", md)
	}

	// Removing it from the library leaves the board's copy; saving the
	// board's copy brings it back, same block, same version.
	if msg := callErr(t, reg, "board.block.remove", `{"type":"feature-idea"}`); !strings.Contains(msg, "confirm") {
		t.Fatalf("remove without confirm: %s", msg)
	}
	call(t, reg, "Claude Code", "board.block.remove", `{"type":"feature-idea","confirm":true}`)
	if l := call(t, reg, "Claude Code", "board.block.list", ``)["blocks"].([]any); len(l) != 1 {
		t.Fatalf("after remove: %v", l)
	}
	b, _ = store.Load(id)
	if b.BoardType("feature-idea") == nil {
		t.Fatal("removing from the library took the board's copy")
	}
	saved := call(t, reg, "Claude Code", "board.block.save", `{"id":"`+id+`","type":"feature-idea"}`)
	if saved["saved"] != true || saved["version"] != 2.0 {
		t.Fatalf("save: %v", saved)
	}
	if again := call(t, reg, "Claude Code", "board.block.save", `{"id":"`+id+`","type":"feature-idea"}`); again["saved"] != false {
		t.Fatalf("saving it again changed something: %v", again)
	}
}

// A board from someone else carries a block type the person's library has
// under the same name but as a different block: the board's copy is used
// for its blocks, and saving it needs another name.
func TestBoardBlockClash(t *testing.T) {
	reg, store, _ := newBlockRegistry(t)
	in := sampleBlockInputs(t)
	call(t, reg, "Claude Code", "board.block.define", string(in[0]))

	theirs := board.BlockDef{}
	if err := json.Unmarshal(in[0], &theirs); err != nil {
		t.Fatal(err)
	}
	theirs.Version, theirs.Origin, theirs.Label = 4, "blt-friend", "Their idea"
	b, err := store.Create("From a friend")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Apply(b.ID, board.Op{Source: board.Person, Types: []board.BlockDef{theirs}}); err != nil {
		t.Fatal(err)
	}
	call(t, reg, "Claude Code", "board.add", `{"id":"`+b.ID+`","blocks":[{"type":"feature-idea","props":{"title":"Theirs"}}]}`)
	got, _ := store.Load(b.ID)
	if d := got.BoardType("feature-idea"); d.Origin != "blt-friend" || d.Version != 4 {
		t.Fatalf("the board's copy was replaced: %+v", d)
	}
	if msg := callErr(t, reg, "board.block.save", `{"id":"`+b.ID+`","type":"feature-idea"}`); !strings.Contains(msg, "different block") {
		t.Fatalf("clash: %s", msg)
	}
	saved := call(t, reg, "Claude Code", "board.block.save", `{"id":"`+b.ID+`","type":"feature-idea","as":"their-idea"}`)
	if saved["type"] != "their-idea" || saved["version"] != 1.0 {
		t.Fatalf("save as: %v", saved)
	}
	cat := call(t, reg, "Claude Code", "board.catalog", `{"id":"`+b.ID+`"}`)
	if bt, _ := cat["boardTypes"].([]any); len(bt) != 1 {
		t.Fatalf("boardTypes = %v", cat["boardTypes"])
	}
}
