package agentctl

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func echo(name string) Command {
	return Command{Name: name, Description: "echoes", Handler: func(_ context.Context, in json.RawMessage) (any, error) {
		return json.RawMessage(in), nil
	}}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(echo("b.echo"))
	r.Register(echo("a.echo"))
	r.Register(Command{Name: "a.boom", Handler: func(context.Context, json.RawMessage) (any, error) { panic("kaboom") }})
	r.Register(Command{Name: "library.delete", Destructive: true, Handler: func(context.Context, json.RawMessage) (any, error) { return "deleted", nil }})

	infos := r.Infos()
	var names []string
	for _, i := range infos {
		names = append(names, i.Name)
	}
	if strings.Join(names, ",") != "a.boom,a.echo,b.echo,library.delete" {
		t.Fatalf("list %v", names)
	}
	if string(infos[1].Input) != `{"type":"object","properties":{}}` {
		t.Fatalf("default schema %s", infos[1].Input)
	}

	ctx := context.Background()
	got, err := r.Call(ctx, "a.echo", json.RawMessage(`{"x":1}`))
	if err != nil || string(got.(json.RawMessage)) != `{"x":1}` {
		t.Fatalf("call: %v, %v", got, err)
	}
	if got, err := r.Call(ctx, "a.echo", nil); err != nil || string(got.(json.RawMessage)) != `{}` {
		t.Fatalf("empty input: %v, %v", got, err)
	}
	if _, err := r.Call(ctx, "nope", nil); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := r.Call(ctx, "a.echo", json.RawMessage(`[1]`)); err == nil {
		t.Fatal("non-object input: want an error")
	}
	if _, err := r.Call(ctx, "a.boom", nil); err == nil || !strings.Contains(err.Error(), "kaboom") {
		t.Fatalf("panic: %v", err)
	}
	if _, err := r.Call(ctx, "library.delete", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), `"confirm": true`) {
		t.Fatalf("destructive without confirm: %v", err)
	}
	if _, err := r.Call(ctx, "library.delete", json.RawMessage(`{"confirm":"yes"}`)); err == nil {
		t.Fatal(`destructive with "yes": want an error`)
	}
	if got, err := r.Call(ctx, "library.delete", json.RawMessage(`{"confirm":true}`)); err != nil || got != "deleted" {
		t.Fatalf("destructive with confirm: %v, %v", got, err)
	}
	r.Unregister("b.echo")
	if _, err := r.Call(ctx, "b.echo", nil); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("after unregister: %v", err)
	}
}

func TestRegisterNeedsHandler(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("no panic for a command without a handler")
		}
	}()
	NewRegistry().Register(Command{Name: "x"})
}

func TestHandle(t *testing.T) {
	r := NewRegistry()
	r.Register(echo("a.echo"))
	ctx := context.Background()
	if resp := handle(ctx, r, Request{ID: json.RawMessage(`7`), Method: "list"}); resp.Error != nil || len(resp.Result.([]CommandInfo)) != 1 || string(resp.ID) != "7" {
		t.Fatalf("list: %+v", resp)
	}
	if resp := handle(ctx, r, Request{Method: "fly"}); resp.Error == nil {
		t.Fatal("unknown method: want an error")
	}
	if resp := handle(ctx, r, Request{Method: "call", Name: "nope"}); resp.Error == nil || !strings.Contains(resp.Error.Message, "unknown command") {
		t.Fatalf("unknown command: %+v", resp)
	}
}

// fakeWindows stands in for the webview: it answers "agent:call" events.
type fakeWindows struct {
	mu     sync.Mutex
	calls  []WindowCall
	answer func(c WindowCall) (any, string, bool) // ok=false: never answer
	svc    *Service
	alive  map[string]bool
}

func newBridge(t *testing.T) (*Service, *fakeWindows) {
	t.Helper()
	svc := New(NewRegistry())
	dir := t.TempDir()
	svc.configPath = func() (string, error) { return filepath.Join(dir, "agent.json"), nil }
	fw := &fakeWindows{svc: svc, alive: map[string]bool{}}
	svc.windowAlive = func(name string) bool {
		fw.mu.Lock()
		defer fw.mu.Unlock()
		return fw.alive[name]
	}
	svc.emit = func(event string, data any) {
		if event != EventCall {
			return
		}
		c := data.(WindowCall)
		fw.mu.Lock()
		fw.calls = append(fw.calls, c)
		answer := fw.answer
		fw.mu.Unlock()
		if answer == nil {
			return
		}
		res, errMsg, ok := answer(c)
		if ok {
			go svc.Respond(c.ID, res, errMsg)
		}
	}
	return svc, fw
}

func TestWindowCommandProxy(t *testing.T) {
	svc, fw := newBridge(t)
	fw.alive["edit-a"], fw.alive["edit-b"] = true, true
	fw.answer = func(c WindowCall) (any, string, bool) {
		if c.Name == "editor.fail" {
			return nil, "nothing to split there", true
		}
		return map[string]any{"from": c.Window, "input": string(c.Input)}, "", true
	}
	split := WindowCommand{Name: "editor.split", Description: "Split at t", Input: map[string]any{"type": "object", "properties": map[string]any{"t": map[string]any{"type": "number"}}}}
	if err := svc.RegisterWindowCommands("edit-a", []WindowCommand{split, {Name: "editor.fail"}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.RegisterWindowCommands("edit-b", []WindowCommand{split}); err != nil {
		t.Fatal(err)
	}
	reg := svc.Registry()
	infos := reg.Infos()
	if len(infos) != 2 || infos[1].Name != "editor.split" || !strings.Contains(string(infos[1].Input), `"t"`) {
		t.Fatalf("registered %+v", infos)
	}

	ctx := context.Background()
	// No window named: the most recently registered (edit-b).
	got, err := reg.Call(ctx, "editor.split", json.RawMessage(`{"t":12.5}`))
	if err != nil || got.(map[string]any)["from"] != "edit-b" || got.(map[string]any)["input"] != `{"t":12.5}` {
		t.Fatalf("default window: %v, %v", got, err)
	}
	// Touching edit-a makes it the default; naming a window picks it.
	svc.TouchWindow("edit-a")
	if got, _ := reg.Call(ctx, "editor.split", nil); got.(map[string]any)["from"] != "edit-a" {
		t.Fatalf("after touch: %v", got)
	}
	if got, _ := reg.Call(ctx, "editor.split", json.RawMessage(`{"window":"edit-b"}`)); got.(map[string]any)["from"] != "edit-b" {
		t.Fatalf("named window: %v", got)
	}
	if _, err := reg.Call(ctx, "editor.split", json.RawMessage(`{"window":"edit-z"}`)); err == nil {
		t.Fatal("unknown window: want an error")
	}
	// The window's error comes back as the call's error.
	if _, err := reg.Call(ctx, "editor.fail", nil); err == nil || err.Error() != "nothing to split there" {
		t.Fatalf("window error: %v", err)
	}

	// A window that closed without unregistering is dropped, and the call
	// goes to the other one.
	fw.mu.Lock()
	fw.alive["edit-a"] = false
	fw.mu.Unlock()
	if got, err := reg.Call(ctx, "editor.split", nil); err != nil || got.(map[string]any)["from"] != "edit-b" {
		t.Fatalf("dead window skipped: %v, %v", got, err)
	}
	if _, err := reg.Call(ctx, "editor.fail", nil); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("dead window's only command still listed: %v", err)
	}

	// The last window going away removes the command.
	svc.UnregisterWindowCommands("edit-b")
	if len(reg.Infos()) != 0 {
		t.Fatalf("left %+v", reg.Infos())
	}
}

func TestWindowCommandTimeout(t *testing.T) {
	svc, fw := newBridge(t)
	svc.callTimeout = 80 * time.Millisecond
	fw.alive["edit"] = true
	fw.answer = func(WindowCall) (any, string, bool) { return nil, "", false } // hung window
	if err := svc.RegisterWindowCommands("edit", []WindowCommand{{Name: "editor.get"}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := svc.Registry().Call(context.Background(), "editor.get", nil)
	if err == nil || !strings.Contains(err.Error(), "didn't answer") || time.Since(start) > 2*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	// A late answer for a timed-out call is ignored, and nothing is left pending.
	svc.Respond("1", "late", "")
	svc.mu.Lock()
	n := len(svc.pending)
	svc.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d calls still pending", n)
	}
	// A cancelled caller stops waiting too.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	svc.callTimeout = time.Minute
	if _, err := svc.Registry().Call(ctx, "editor.get", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestRegisterWindowCommandsChecks(t *testing.T) {
	svc, _ := newBridge(t)
	if err := svc.RegisterWindowCommands("", nil); err == nil {
		t.Fatal("no window: want an error")
	}
	if err := svc.RegisterWindowCommands("w", []WindowCommand{{Name: ""}}); err == nil {
		t.Fatal("no name: want an error")
	}
	// Re-registering replaces the window's set.
	_ = svc.RegisterWindowCommands("w", []WindowCommand{{Name: "a.one"}, {Name: "a.two"}})
	_ = svc.RegisterWindowCommands("w", []WindowCommand{{Name: "a.two", Destructive: true}})
	infos := svc.Registry().Infos()
	if len(infos) != 1 || infos[0].Name != "a.two" || !infos[0].Destructive {
		t.Fatalf("after replace %+v", infos)
	}
}
