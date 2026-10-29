package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeToru answers the pipe protocol like Toru's agent pipe, from a command
// list the test can change between calls.
type fakeToru struct {
	mu      sync.Mutex
	cmds    []CommandInfo
	calls   []string // "name input" per call
	running bool
	dials   int
	conns   []net.Conn
}

// stop is Toru exiting: the pipe goes away and open connections break.
func (f *fakeToru) stop() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	for _, c := range f.conns {
		_ = c.Close()
	}
	f.conns = nil
}

func newFake(cmds ...CommandInfo) *fakeToru { return &fakeToru{cmds: cmds, running: true} }

func (f *fakeToru) setCommands(cmds ...CommandInfo) {
	f.mu.Lock()
	f.cmds = cmds
	f.mu.Unlock()
}

func (f *fakeToru) dial(context.Context) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.running {
		return nil, errors.New("The system cannot find the file specified.")
	}
	f.dials++
	client, server := net.Pipe()
	f.conns = append(f.conns, server)
	go f.serve(server)
	return client, nil
}

func (f *fakeToru) serve(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Name   string          `json:"name"`
			Input  json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		resp := map[string]any{"id": req.ID}
		f.mu.Lock()
		switch req.Method {
		case "list":
			resp["result"] = f.cmds
		case "call":
			f.calls = append(f.calls, req.Name+" "+string(req.Input))
			switch req.Name {
			case "editor.split":
				resp["result"] = map[string]any{"ok": true, "segments": 2}
			case "editor.frame":
				resp["result"] = map[string]any{"t": 1.5, "image": map[string]string{"mime": "image/png", "base64": base64.StdEncoding.EncodeToString([]byte("PNGDATA"))}}
			case "library.delete":
				resp["error"] = map[string]string{"message": `library.delete is destructive: pass "confirm": true`}
			default:
				resp["error"] = map[string]string{"message": `unknown command "` + req.Name + `"`}
			}
		}
		f.mu.Unlock()
		b, _ := json.Marshal(resp)
		if _, err := conn.Write(append(b, '\n')); err != nil {
			return
		}
	}
}

var (
	splitCmd  = CommandInfo{Name: "editor.split", Description: "Split the clip at t.", Input: json.RawMessage(`{"type":"object","properties":{"t":{"type":"number"}},"required":["t"]}`)}
	frameCmd  = CommandInfo{Name: "editor.frame", Description: "Preview frame at t as PNG.", Input: json.RawMessage(`{"type":"object","properties":{"t":{"type":"number"}}}`)}
	deleteCmd = CommandInfo{Name: "library.delete", Description: "Delete a Library item. Destructive.", Input: json.RawMessage(`{"type":"object"}`), Destructive: true}
)

func ctl(t *testing.T, f *fakeToru, enabled bool, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	env := Env{
		Dial:    f.dial,
		Enabled: func() bool { return enabled },
		Start: func() error {
			f.mu.Lock()
			f.running = true
			f.mu.Unlock()
			return nil
		},
		Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errb,
	}
	code = Main(append([]string{"ctl"}, args...), env)
	return code, out.String(), errb.String()
}

func TestCtlList(t *testing.T) {
	f := newFake(splitCmd, deleteCmd)
	code, out, errOut := ctl(t, f, true, "", "list")
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got []CommandInfo
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	names := []string{}
	for _, c := range got {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "app.start,editor.split,library.delete" || !got[2].Destructive {
		t.Errorf("list = %v", got)
	}
}

func TestCtlCall(t *testing.T) {
	f := newFake(splitCmd)
	code, out, errOut := ctl(t, f, true, "", "editor.split", "--json", `{"t":12.5}`)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || got["segments"] != 2.0 {
		t.Errorf("out = %s (%v)", out, err)
	}
	// --json=..., and "-" reads stdin.
	if code, _, errOut := ctl(t, f, true, "", "editor.split", `--json={"t":1}`); code != exitOK {
		t.Errorf("--json=: exit %d %s", code, errOut)
	}
	if code, _, errOut := ctl(t, f, true, ` {"t":2} `, "editor.split", "--json", "-"); code != exitOK {
		t.Errorf("--json -: exit %d %s", code, errOut)
	}
	// No --json sends an empty object.
	if code, _, _ := ctl(t, f, true, "", "editor.split"); code != exitOK {
		t.Error("no input")
	}
	want := []string{`editor.split {"t":12.5}`, `editor.split {"t":1}`, `editor.split {"t":2}`, `editor.split {}`}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls = %q", f.calls)
	}
}

func TestCtlErrors(t *testing.T) {
	f := newFake(deleteCmd)
	if code, _, errOut := ctl(t, f, true, "", "library.delete", "--json", `{"id":"x"}`); code != exitCommand || !strings.Contains(errOut, "confirm") {
		t.Errorf("command error: exit %d %q", code, errOut)
	}
	if code, _, errOut := ctl(t, f, true, "", "editor.split", "--json", `[1]`); code != exitUsage || !strings.Contains(errOut, "JSON object") {
		t.Errorf("array input: exit %d %q", code, errOut)
	}
	if code, _, errOut := ctl(t, f, true, "", "editor.split", "--json", `{bad`); code != exitUsage || errOut == "" {
		t.Errorf("bad JSON: exit %d %q", code, errOut)
	}
	if code, _, _ := ctl(t, f, true, "", "editor.split", "extra"); code != exitUsage {
		t.Errorf("stray argument: exit %d", code)
	}
	if code, _, errOut := ctl(t, f, true, ""); code != exitUsage || !strings.Contains(errOut, "Usage") {
		t.Errorf("no command: exit %d %q", code, errOut)
	}

	f.stop()
	if code, _, errOut := ctl(t, f, true, "", "list"); code != exitUnreachable || !strings.Contains(errOut, "isn't running") || !strings.Contains(errOut, "app.start") {
		t.Errorf("not running: exit %d %q", code, errOut)
	}
	if code, _, errOut := ctl(t, f, false, "", "editor.split"); code != exitUnreachable || !strings.Contains(errOut, "Settings > General") {
		t.Errorf("toggle off: exit %d %q", code, errOut)
	}
}

func TestCtlImageSavedToFile(t *testing.T) {
	f := newFake(frameCmd)
	code, out, errOut := ctl(t, f, true, "", "editor.frame", "--json", `{"t":1.5}`)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	var got struct {
		T     float64 `json:"t"`
		Image struct{ Mime, Path string }
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(got.Image.Path) }()
	data, err := os.ReadFile(got.Image.Path)
	if err != nil || string(data) != "PNGDATA" || got.T != 1.5 || got.Image.Mime != "image/png" || strings.Contains(out, "base64") {
		t.Errorf("out = %s, file %q (%v)", out, data, err)
	}
}

func TestCtlAppStart(t *testing.T) {
	f := newFake(splitCmd)
	code, out, _ := ctl(t, f, true, "", "app.start")
	if code != exitOK || !strings.Contains(out, `"started": false`) {
		t.Errorf("already running: exit %d %s", code, out)
	}
	f.stop()
	code, out, errOut := ctl(t, f, true, "", "app.start")
	if code != exitOK || !strings.Contains(out, `"started": true`) {
		t.Errorf("start: exit %d %s %s", code, out, errOut)
	}
	f.stop()
	if code, _, errOut := ctl(t, f, false, "", "app.start"); code != exitUnreachable || !strings.Contains(errOut, "Settings > General") {
		t.Errorf("toggle off: exit %d %q", code, errOut)
	}
}

// The client redials once when Toru drops the connection (restarted).
func TestClientRedials(t *testing.T) {
	f := newFake(splitCmd)
	c := NewClient(f.dial)
	defer func() { _ = c.Close() }()
	ctx := context.Background()
	if _, err := c.List(ctx); err != nil {
		t.Fatal(err)
	}
	_ = c.conn.Close() // what a Toru restart looks like from here
	if _, err := c.Call(ctx, "editor.split", json.RawMessage(`{"t":1}`)); err != nil {
		t.Fatalf("after drop: %v", err)
	}
	if f.dials != 2 {
		t.Errorf("dials = %d, want 2", f.dials)
	}
}

// mcpSession connects an MCP client to newMCPServer in memory.
func mcpSession(t *testing.T, f *fakeToru, enabled bool) *mcp.ClientSession {
	t.Helper()
	s := newMCPServer(Env{Dial: f.dial, Enabled: func() bool { return enabled }, Start: func() error {
		f.mu.Lock()
		f.running = true
		f.mu.Unlock()
		return nil
	}})
	t.Cleanup(func() { _ = s.client.Close() })
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := s.srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func toolNames(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	return strings.Join(names, ",")
}

func TestMCPListsAndRefreshesTools(t *testing.T) {
	f := newFake(splitCmd, deleteCmd)
	cs := mcpSession(t, f, true)
	if got := toolNames(t, cs); got != "app.start,editor.split,library.delete" {
		t.Errorf("tools = %s", got)
	}
	res, _ := cs.ListTools(context.Background(), nil)
	for _, tl := range res.Tools {
		if tl.Name == "library.delete" && (tl.Annotations == nil || tl.Annotations.DestructiveHint == nil || !*tl.Annotations.DestructiveHint) {
			t.Error("library.delete isn't marked destructive")
		}
		if tl.Name == "editor.split" {
			b, _ := json.Marshal(tl.InputSchema)
			if !strings.Contains(string(b), `"required":["t"]`) {
				t.Errorf("schema = %s", b)
			}
		}
	}
	// An editor window opened (new commands) and another command went away.
	f.setCommands(splitCmd, frameCmd)
	if got := toolNames(t, cs); got != "app.start,editor.frame,editor.split" {
		t.Errorf("after change, tools = %s", got)
	}
}

func TestMCPCallTool(t *testing.T) {
	f := newFake(splitCmd, frameCmd, deleteCmd)
	cs := mcpSession(t, f, true)
	ctx := context.Background()
	toolNames(t, cs)

	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "editor.split", Arguments: map[string]any{"t": 12.5}})
	if err != nil || res.IsError || len(res.Content) != 1 {
		t.Fatalf("split: %+v %v", res, err)
	}
	if txt := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, `"segments":2`) {
		t.Errorf("split result = %s", txt)
	}
	if f.calls[0] != `editor.split {"t":12.5}` {
		t.Errorf("forwarded %q", f.calls[0])
	}

	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "editor.frame", Arguments: map[string]any{"t": 1.5}})
	if err != nil || res.IsError || len(res.Content) != 2 {
		t.Fatalf("frame: %+v %v", res, err)
	}
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok || string(img.Data) != "PNGDATA" || img.MIMEType != "image/png" {
		t.Errorf("image content = %+v", res.Content[0])
	}
	if txt := res.Content[1].(*mcp.TextContent).Text; txt != `{"t":1.5}` {
		t.Errorf("rest = %s", txt)
	}

	// A command error is a tool error the model can read, not a protocol error.
	res, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "library.delete", Arguments: map[string]any{}})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "confirm") {
		t.Errorf("delete: %+v %v", res, err)
	}
}

// A window command registered after the last tools/list is still callable.
func TestMCPCallsCommandAddedSinceList(t *testing.T) {
	f := newFake(splitCmd)
	cs := mcpSession(t, f, true)
	toolNames(t, cs)
	f.setCommands(splitCmd, frameCmd)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "editor.frame", Arguments: map[string]any{"t": 0}})
	if err != nil || res.IsError {
		t.Fatalf("frame: %+v %v", res, err)
	}
}

func TestMCPWhenToruIsUnreachable(t *testing.T) {
	f := newFake(splitCmd)
	cs := mcpSession(t, f, true)
	toolNames(t, cs)
	f.stop()
	if got := toolNames(t, cs); got != "app.start" {
		t.Errorf("tools while Toru is down = %s", got)
	}
	// app.start brings it back and the commands return.
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "app.start"})
	if err != nil || res.IsError {
		t.Fatalf("app.start: %+v %v", res, err)
	}
	if got := toolNames(t, cs); got != "app.start,editor.split" {
		t.Errorf("after start, tools = %s", got)
	}

	off := newFake(splitCmd)
	off.running = false
	cs = mcpSession(t, off, false)
	res, err = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "app.start"})
	if err != nil || !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "Settings > General") {
		t.Errorf("toggle off: %+v %v", res, err)
	}
}

func TestToolForBadSchema(t *testing.T) {
	for _, in := range []string{``, `null`, `{"type":"string"}`, `[1]`} {
		tl := toolFor(CommandInfo{Name: "x", Input: json.RawMessage(in)})
		if b, _ := json.Marshal(tl.InputSchema); string(b) != `{"type":"object"}` {
			t.Errorf("schema %q -> %s", in, b)
		}
	}
}
