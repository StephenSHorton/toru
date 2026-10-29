//go:build windows

package agentctl

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
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

func TestToggle(t *testing.T) {
	svc, _ := newBridge(t)
	svc.pipeName = testPipeName(t)
	reg := svc.Registry()
	reg.Register(echo("app.echo"))

	dial := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		c, err := Dial(ctx, svc.pipeName)
		if err == nil {
			_ = c.Close()
		}
		return err
	}

	// Off by default: Start makes no pipe.
	if svc.GetEnabled() {
		t.Fatal("enabled by default")
	}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	if dial() == nil {
		t.Fatal("pipe exists while off")
	}
	// On: the pipe answers, and the choice is saved.
	if err := svc.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if got := roundTrip(t, svc.pipeName, `{"id":1,"method":"call","name":"app.echo","input":{"hi":1}}`); got[0]["result"].(map[string]any)["hi"] != 1.0 {
		t.Fatalf("echo: %v", got)
	}
	if !svc.GetEnabled() {
		t.Fatal("not saved")
	}
	if err := svc.SetEnabled(true); err != nil { // already on: no-op
		t.Fatal(err)
	}
	// A fresh service with the same settings starts listening on its own.
	svc2 := New(reg)
	svc2.configPath, svc2.pipeName = svc.configPath, svc.pipeName+"-2"
	if err := svc2.Start(); err != nil {
		t.Fatal(err)
	}
	svc2.Shutdown()

	// Off: the pipe goes away.
	if err := svc.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if dial() == nil {
		t.Fatal("pipe still exists after turning off")
	}
	if svc.GetEnabled() {
		t.Fatal("off not saved")
	}
}
