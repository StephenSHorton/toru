//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/agentctl/cli"
	"github.com/StephenSHorton/toru/internal/board"
)

// TestMCPBuildsCustomBlocks is whiteboard#27's end-to-end run: an MCP client
// named "claude-code" talks to the real `toru.exe mcp` code (cli.RunMCP)
// over Toru's real agent pipe server (on a test pipe, with temporary boards
// and blocks folders, so a running Toru is never touched). It designs two
// block types (a feature idea with votes, a bug report), places them on a
// board, fills them, and exports the board as PNG (drawn by Toru's board
// viewer in its export WebView2) and HTML.
//
// With TORU_CUSTOM_BLOCKS_OUT=<folder> the board, its PNG and HTML, and the
// two block files are written there to look at.
func TestMCPBuildsCustomBlocks(t *testing.T) {
	exporter := newBoardExporter()
	if _, err := exporter.Viewer(); err != nil {
		if os.Getenv("TORU_REQUIRE_VIEWER") != "" {
			t.Fatal(err)
		}
		t.Skip("the board viewer isn't built into this binary (run `bun run build` in frontend)")
	}
	exporter.WebViewDir = webViewDir(t)

	dir := t.TempDir()
	oldB, oldL := board.Dir, board.BlocksDir
	board.Dir = func() (string, error) { return filepath.Join(dir, "boards"), nil }
	board.BlocksDir = func() (string, error) { return filepath.Join(dir, "blocks"), nil }
	t.Cleanup(func() { board.Dir, board.BlocksDir = oldB, oldL })
	reg := agentctl.NewRegistry()
	store := board.NewStore()
	registerBoardAgentCommands(reg, boardAgentDeps{boards: store, exporter: exporter, blocks: board.NewLibrary()})
	name := fmt.Sprintf(`\\.\pipe\toru-agent-blocktest-%d-%d`, os.Getpid(), time.Now().UnixNano())
	srv, err := agentctl.Listen(reg, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	dial := func(ctx context.Context) (net.Conn, error) { return agentctl.Dial(ctx, name) }

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- cli.RunMCP(ctx, cli.Env{Dial: dial, Stdin: inR, Stdout: outW, Stderr: io.Discard})
		_ = outW.Close()
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "2.0"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callTool := func(tool, args string) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s: %s", tool, text)
		}
		short := text
		if len(short) > 160 {
			short = short[:160] + "…"
		}
		fmt.Printf("mcp> %s\n     %s\n", tool, short)
		var v map[string]any
		_ = json.Unmarshal([]byte(text), &v)
		return v
	}

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, tl := range tools.Tools {
		have[tl.Name] = true
	}
	for _, want := range []string{"board.block.define", "board.block.list", "board.block.update", "board.block.remove", "board.block.save"} {
		if !have[want] {
			t.Fatalf("MCP doesn't list %s", want)
		}
	}

	// The agent reads what it can build from, then designs two blocks.
	cat := callTool("board.catalog", `{}`)
	if cat["parts"] == nil {
		t.Fatal("board.catalog has no parts")
	}
	for _, in := range sampleBlockInputs(t) {
		callTool("board.block.define", string(in))
	}
	lib := callTool("board.block.list", `{}`)["blocks"].([]any)
	if len(lib) != 2 {
		t.Fatalf("library = %v", lib)
	}

	id := callTool("board.create", `{"name":"Toru roadmap"}`)["id"].(string)
	callTool("board.add", `{"id":"`+id+`","blocks":[
		{"id":"title","type":"text","x":0,"y":-90,"w":760,"h":60,"props":{"text":"Toru roadmap: ideas and bugs","size":"xl"}},
		{"id":"idea-dark","type":"feature-idea","x":0,"y":0,"props":{"title":"Dark mode for exports","pitch":"People paste board pictures into dark chat apps; a white board glares.","votes":7,"status":"planned","effort":3,"tags":["export","polish"]}},
		{"id":"idea-share","type":"feature-idea","x":0,"y":560,"props":{"title":"Share a board with a link","pitch":"Friends without Toru open a live board in a browser.","votes":12,"status":"building","effort":8,"tags":["sharing"]}},
		{"id":"bug-paste","type":"bug-report","x":400,"y":0,"props":{"title":"Pasting two images makes one block","severity":"high","steps":[{"text":"Copy two screenshots","done":true},{"text":"Paste on the board","done":true},{"text":"See only one image block"}],"expected":"Two image blocks","actual":"One block, the second image lost"}},
		{"id":"bug-zoom","type":"bug-report","x":400,"y":540,"props":{"title":"Zoom jumps after undo","severity":"low","fixed":true,"steps":[{"text":"Zoom in"},{"text":"Undo a move"}],"expected":"The view stays","actual":"The view jumps to 100%"}}]}`)
	// It votes on an idea like a person would, through the board.
	callTool("board.update", `{"id":"`+id+`","blocks":[{"id":"idea-dark","props":{"votes":8}}]}`)

	b, _ := store.Load(id)
	if len(b.Types) != 2 {
		t.Fatalf("the board carries %d types", len(b.Types))
	}
	md := callTool("board.read", `{"id":"`+id+`","format":"markdown"}`)["markdown"].(string)
	fmt.Println(md)
	for _, want := range []string{"Feature idea: Dark mode for exports", "  - votes: 8", "Bug report: Pasting two images makes one block", "    - [x] Copy two screenshots"} {
		if !strings.Contains(md, want) {
			t.Errorf("outline lacks %q", want)
		}
	}

	out := os.Getenv("TORU_CUSTOM_BLOCKS_OUT")
	if out == "" {
		out = t.TempDir()
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	export := func(format string) string {
		t.Helper()
		return callTool("board.export", `{"id":"`+id+`","format":"`+format+`","path":`+jsonString(filepath.Join(out, "custom-blocks-board"))+`,"confirm":true}`)["path"].(string)
	}
	pngPath := export("png")
	data, err := os.ReadFile(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("png %s: %v\n", pngPath, img.Bounds().Size())
	html, err := os.ReadFile(export("html"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte(`"feature-idea"`)) || !bytes.Contains(html, []byte("Pasting two images")) {
		t.Error("the HTML doesn't carry the custom types and blocks")
	}
	tb := export("toruboard")
	// Another Toru, with an empty library, imports it and the types come along.
	imported, err := board.NewStore().ImportFile(tb)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Types) != 2 {
		t.Fatalf("an imported board carries %d types", len(imported.Types))
	}
	_ = cs.Close()
	_ = inW.Close()
	<-done
}
