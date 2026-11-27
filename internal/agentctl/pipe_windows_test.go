//go:build windows

package agentctl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func testPipeName(t *testing.T) string {
	return fmt.Sprintf(`\\.\pipe\toru-agent-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

// roundTrip sends each request line and returns the decoded responses.
func roundTrip(t *testing.T, name string, lines ...string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx, name)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	rd := bufio.NewReader(conn)
	var out []map[string]any
	for _, l := range lines {
		if _, err := conn.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
		resp, err := rd.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read reply to %s: %v", l, err)
		}
		var m map[string]any
		if err := json.Unmarshal(resp, &m); err != nil {
			t.Fatalf("reply %q: %v", resp, err)
		}
		out = append(out, m)
	}
	return out
}

func TestPipeRoundTrip(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Command{Name: "app.add", Description: "adds", Handler: func(_ context.Context, in json.RawMessage) (any, error) {
		var v struct{ A, B int }
		if err := json.Unmarshal(in, &v); err != nil {
			return nil, err
		}
		return map[string]int{"sum": v.A + v.B}, nil
	}})
	name := testPipeName(t)
	srv, err := Listen(reg, name)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	got := roundTrip(t, name,
		`{"id":1,"method":"list"}`,
		`{"id":"two","method":"call","name":"app.add","input":{"a":2,"b":40}}`,
		`{"id":3,"method":"call","name":"app.nope"}`,
		`not json`,
	)
	if got[0]["id"] != 1.0 || len(got[0]["result"].([]any)) != 1 {
		t.Fatalf("list: %v", got[0])
	}
	if got[1]["id"] != "two" || got[1]["result"].(map[string]any)["sum"] != 42.0 {
		t.Fatalf("call: %v", got[1])
	}
	if e, _ := got[2]["error"].(map[string]any); e == nil || !strings.Contains(e["message"].(string), "unknown command") {
		t.Fatalf("unknown: %v", got[2])
	}
	if e, _ := got[3]["error"].(map[string]any); e == nil || !strings.Contains(e["message"].(string), "bad request") {
		t.Fatalf("bad line: %v", got[3])
	}

	// Closing the server removes the pipe.
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if c, err := Dial(ctx, name); err == nil {
		_ = c.Close()
		t.Fatal("pipe still there after Close")
	}
}

func TestPipeSDDLIsCurrentUserOnly(t *testing.T) {
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sddl := pipeSDDL(sid)
	if sddl != "D:P(A;;GA;;;"+sid+")" || strings.Count(sddl, "(A;") != 1 {
		t.Fatalf("sddl %q", sddl)
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("dacl %v, %v", dacl, err)
	}
	if name, _ := PipeName(); name != `\\.\pipe\toru-agent-`+sid {
		t.Fatalf("pipe name %q", name)
	}
}

// The pipe is always on: Start opens it with no setting, and an old
// agent.json from the removed toggle (saved off) changes nothing.
func TestStartAlwaysListens(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir) // os.UserConfigDir on Windows
	if err := os.MkdirAll(filepath.Join(dir, "toru"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "toru", "agent.json"), []byte(`{"enabled": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	svc, _ := newBridge(t)
	svc.pipeName = testPipeName(t)
	svc.Registry().Register(echo("app.echo"))

	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, svc.pipeName, `{"id":1,"method":"call","name":"app.echo","input":{"hi":1}}`); got[0]["result"].(map[string]any)["hi"] != 1.0 {
		t.Fatalf("echo: %v", got)
	}
	if err := svc.Start(); err != nil { // already open: no-op
		t.Fatal(err)
	}
	svc.Shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if c, err := Dial(ctx, svc.pipeName); err == nil {
		_ = c.Close()
		t.Fatal("pipe still exists after Shutdown")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "toru", "agent.json")); err != nil || string(data) != `{"enabled": false}` {
		t.Fatalf("old setting file changed: %q %v", data, err)
	}
}
