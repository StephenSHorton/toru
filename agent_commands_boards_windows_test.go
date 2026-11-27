//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/agentctl/cli"
	"github.com/StephenSHorton/toru/internal/board"
)

// These tests run the real `toru.exe ctl` and `toru.exe mcp` code (package
// cli) against Toru's real agent pipe server (package agentctl) with the
// board commands registered, on a pipe name of their own and a temporary
// boards folder, so they never touch a running Toru.

func servePipe(t *testing.T) func(context.Context) (net.Conn, error) {
	t.Helper()
	reg, _ := newBoardRegistry(t)
	name := fmt.Sprintf(`\\.\pipe\toru-agent-boardtest-%d-%d`, os.Getpid(), time.Now().UnixNano())
	srv, err := agentctl.Listen(reg, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return func(ctx context.Context) (net.Conn, error) { return agentctl.Dial(ctx, name) }
}

// TestCtlBuildsABoard builds a small board end to end with `toru.exe ctl`,
// printing each command and what it printed.
func TestCtlBuildsABoard(t *testing.T) {
	dial := servePipe(t)
	quiet := false
	ctl := func(args ...string) map[string]any {
		t.Helper()
		var out, errOut bytes.Buffer
		code := cli.RunCtl(context.Background(), args, cli.Env{Dial: dial, Enabled: func() bool { return true }, Stdout: &out, Stderr: &errOut})
		if quiet {
			fmt.Printf("ctl> %s\n     (%d bytes of board JSON)\n", quoteArgs(args), out.Len())
		} else {
			fmt.Printf("ctl> %s\n     %s%s", quoteArgs(args), compact(out.Bytes()), errOut.String())
		}
		if code != 0 {
			t.Fatalf("exit %d: %s", code, errOut.String())
		}
		var v map[string]any
		_ = json.Unmarshal(out.Bytes(), &v)
		return v
	}
	ctlFails := func(args ...string) string {
		t.Helper()
		var out, errOut bytes.Buffer
		code := cli.RunCtl(context.Background(), args, cli.Env{Dial: dial, Enabled: func() bool { return true }, Stdout: &out, Stderr: &errOut})
		fmt.Printf("ctl> %s\n     %s     (exit %d)\n", quoteArgs(args), errOut.String(), code)
		if code == 0 {
			t.Fatalf("%v: want a failure", args)
		}
		return errOut.String()
	}

	fmt.Println("Each ctl> line runs the code of `toru.exe ctl <command> --json ...` (package cli, RunCtl)")
	fmt.Println("against Toru's own agent pipe server with the board commands, on a test pipe and a")
	fmt.Println("temporary boards folder. The installed Toru app is not touched.")
	fmt.Println()

	// What an agent sees first: the board commands are listed.
	var listOut bytes.Buffer
	if code := cli.RunCtl(context.Background(), []string{"list"}, cli.Env{Dial: dial, Stdout: &listOut, Stderr: io.Discard}); code != 0 {
		t.Fatal("list failed")
	}
	var cmds []cli.CommandInfo
	if err := json.Unmarshal(listOut.Bytes(), &cmds); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, c := range cmds {
		if strings.HasPrefix(c.Name, "board.") {
			names = append(names, c.Name)
		}
	}
	fmt.Printf("ctl> list   (board commands only)\n     %s\n", strings.Join(names, " "))
	if len(names) != len(boardCommands) {
		t.Fatalf("ctl list shows %d board commands, want %d", len(names), len(boardCommands))
	}

	id := ctl("board.create", "--json", `{"name":"Launch retro"}`)["id"].(string)
	ctl("board.add", "--json", `{"id":"`+id+`",
		"frames":[{"id":"went-well","name":"Went well","x":0,"y":0,"w":520,"h":300}],
		"blocks":[
		{"id":"goal","type":"text","props":{"text":"Launch retro","size":"xl"}},
		{"id":"fast","type":"sticky","props":{"text":"Shipped on time","color":"green","tags":["good"]},"frame":"went-well","x":20,"y":60},
		{"id":"bugs","type":"sticky","props":{"text":"Too many late bugs","color":"pink","tags":["bad"]}},
		{"id":"vote","type":"poll","props":{"question":"Ship weekly?","options":[{"label":"Yes"},{"label":"No"}]}},
		{"id":"next","type":"checklist","props":{"title":"Next","items":[{"text":"Bug bash before release"}]}}]}`)
	ctl("board.connect", "--json", `{"id":"`+id+`","links":[{"from":"goal","to":"went-well"},{"from":"bugs","to":"next","label":"fix with"}]}`)
	ctl("board.update", "--json", `{"id":"`+id+`","blocks":[{"id":"vote","props":{"options":[{"label":"Yes","votes":3},{"label":"No","votes":1}]}}]}`)
	ctl("board.group", "--json", `{"id":"`+id+`","blocks":["bugs","next"]}`)
	ctl("board.move", "--json", `{"id":"`+id+`","moves":[{"id":"bugs","dx":0,"dy":40}]}`)
	ctl("board.layout", "--json", `{"id":"`+id+`","kind":"tree","blocks":["bugs","next"],"direction":"down"}`)
	ctl("board.disconnect", "--json", `{"id":"`+id+`","from":"goal","to":"went-well"}`)
	ctl("board.draw", "--json", `{"id":"`+id+`","strokes":[{"points":[[0,320],[260,330],[520,320]],"color":"ink-yellow","width":"medium"}]}`)
	md := ctl("board.read", "--json", `{"id":"`+id+`","format":"markdown"}`)["markdown"].(string)
	for _, want := range []string{"## Went well", "- Shipped on time #good", "- Poll: Ship weekly?", "  - Yes (3)", "  - [ ] Bug bash before release", "- Too many late bugs → Next: fix with"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q", want)
		}
	}

	// Every change ctl made is tagged agent:toru-ctl.
	quiet = true
	full := ctl("board.read", "--json", `{"id":"`+id+`"}`)
	quiet = false
	raw, _ := json.Marshal(full["board"])
	var b board.Board
	if err := json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	for _, x := range b.Blocks {
		if x.CreatedBy != "agent:toru-ctl" || x.UpdatedBy != "agent:toru-ctl" {
			t.Errorf("block %s: created %q updated %q", x.ID, x.CreatedBy, x.UpdatedBy)
		}
	}
	if len(b.Ink) != 1 || b.Ink[0].Color != "ink-yellow" || b.Ink[0].Width != 4 || b.Ink[0].CreatedBy != "agent:toru-ctl" {
		t.Errorf("ink = %+v", b.Ink)
	}
	if len(b.Blocks) != 5 || len(b.Links) != 1 || len(b.Frames) != 1 {
		t.Errorf("board has %d blocks, %d links, %d frames", len(b.Blocks), len(b.Links), len(b.Frames))
	}
	fmt.Printf("     every block: createdBy %s, updatedBy %s\n", b.Blocks[0].CreatedBy, b.Blocks[0].UpdatedBy)

	out := t.TempDir() + `\launch-retro`
	ctl("board.export", "--json", `{"id":"`+id+`","format":"markdown","path":`+jsonString(out)+`}`)

	// Destructive: refused without confirm, done with it.
	if msg := ctlFails("board.delete", "--json", `{"id":"`+id+`"}`); !strings.Contains(msg, `"confirm": true`) {
		t.Fatalf("delete without confirm: %s", msg)
	}
	ctl("board.delete", "--json", `{"id":"`+id+`","confirm":true}`)
	if boards := ctl("board.list")["boards"].([]any); len(boards) != 0 {
		t.Fatalf("boards left: %v", boards)
	}
	fmt.Println()
	fmt.Println("Built: 1 frame, 5 blocks (text, 2 stickies, poll, checklist), 2 lines then 1 removed, 1 stroke,")
	fmt.Println("props merged, a group moved and laid out, read as Markdown, exported, deleted with confirm.")
}

// compact is a ctl result on one line; a Markdown outline is shown as its
// own lines.
func compact(out []byte) string {
	var v map[string]any
	if json.Unmarshal(out, &v) == nil {
		if md, ok := v["markdown"].(string); ok {
			return "markdown:\n       " + strings.ReplaceAll(strings.TrimSpace(md), "\n", "\n       ") + "\n"
		}
	}
	var b bytes.Buffer
	if json.Compact(&b, out) != nil {
		return string(out)
	}
	return b.String() + "\n"
}

// TestMCPTagsChangesWithClientName: a change made through `toru.exe mcp`
// is tagged with the MCP client's own name from its initialize request.
func TestMCPTagsChangesWithClientName(t *testing.T) {
	dial := servePipe(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- cli.RunMCP(ctx, cli.Env{Dial: dial, Enabled: func() bool { return true }, Stdin: inR, Stdout: outW, Stderr: io.Discard})
		_ = outW.Close()
	}()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "claude-code", Version: "2.0"}, nil).
		Connect(ctx, &mcp.IOTransport{Reader: outR, Writer: inW}, nil)
	if err != nil {
		t.Fatal(err)
	}
	callTool := func(name, args string) map[string]any {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(args)})
		if err != nil {
			t.Fatal(err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s: %s", name, text)
		}
		var v map[string]any
		_ = json.Unmarshal([]byte(text), &v)
		return v
	}
	if _, err := cs.ListTools(ctx, nil); err != nil {
		t.Fatal(err)
	}
	id := callTool("board.create", `{"name":"From MCP"}`)["id"].(string)
	callTool("board.add", `{"id":"`+id+`","blocks":[{"id":"hi","type":"sticky","props":{"text":"Hello"}}]}`)
	got := callTool("board.read", `{"id":"`+id+`"}`)["board"].(map[string]any)["blocks"].([]any)[0].(map[string]any)
	fmt.Printf("MCP client \"claude-code\" added block %v: createdBy %v\n", got["id"], got["createdBy"])
	if got["createdBy"] != "agent:claude-code" {
		t.Fatalf("createdBy = %v", got["createdBy"])
	}
	// Ink through MCP: a pen width by name and by number both pass the
	// tool's input schema.
	drawn := callTool("board.draw", `{"id":"`+id+`","strokes":[
		{"points":[[0,0],[50,30],[100,0]],"color":"ink-blue","width":"bold"},
		{"points":[[0,60],[100,60,0.8]],"color":"green","width":3}]}`)
	ink := callTool("board.read", `{"id":"`+id+`"}`)["board"].(map[string]any)["ink"].([]any)
	fmt.Printf("MCP client \"claude-code\" drew strokes %v\n", drawn["strokes"])
	if len(ink) != 2 || ink[0].(map[string]any)["width"] != 8.0 || ink[1].(map[string]any)["color"] != "ink-green" || ink[0].(map[string]any)["createdBy"] != "agent:claude-code" {
		t.Fatalf("ink = %v", ink)
	}
	_ = cs.Close()
	_ = inW.Close()
	<-done
}

func quoteArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " {\"\n") {
			a = "'" + strings.Join(strings.Fields(a), " ") + "'"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}
