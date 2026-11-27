package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// TestBoardLiveDemo is the live board's demo server, not a check: it runs
// only with TORU_BOARD_DEMO_PORT set, which Vite's boardLiveDemo plugin
// (frontend/vite.config.ts) does in its board-demo mode:
//
//	cd frontend && bun x vite --mode board-demo --port 47801
//	open http://127.0.0.1:47801/?view=whiteboard&id=brd-demo&link=http
//
// It serves Toru's real board store, the window's board.Service and the
// agents' board commands (agent_commands_boards.go) on a temporary boards
// folder, over HTTP and server-sent events in place of Wails (the window's
// httpLink, frontend/src/whiteboard/links.ts). Once a window is watching,
// an agent named "Claude Code" builds a board through the board commands,
// one change at a time, so the window shows an agent building live; then
// it clears what it made and builds again, while a window watches.
//
// With TORU_BOARD_DEMO_BOARD naming a .toruboard file, it serves a copy of
// that board instead, and no agent builds.
//
// It stops when the process TORU_BOARD_DEMO_PARENT names (Vite) has gone.
func TestBoardLiveDemo(t *testing.T) {
	port := os.Getenv("TORU_BOARD_DEMO_PORT")
	if port == "" {
		t.Skip("the live board demo server runs with TORU_BOARD_DEMO_PORT (see frontend/vite.config.ts)")
	}
	dir := t.TempDir()
	board.Dir = func() (string, error) { return dir, nil }

	store := board.NewStore()
	svc := board.NewService(store)
	hub := &opHub{subs: map[chan []byte]string{}}
	store.OnOp(hub.send)
	reg := agentctl.NewRegistry()
	registerBoardAgentCommands(reg, boardAgentDeps{boards: store})

	const id = "brd-demo"
	// TORU_BOARD_DEMO_BOARD shows a copy of a .toruboard file instead, still,
	// with no agent building (a theme or layout check on a real board).
	still := os.Getenv("TORU_BOARD_DEMO_BOARD")
	if still != "" {
		data, err := os.ReadFile(still)
		if err != nil {
			t.Fatal(err)
		}
		b, err := board.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		b.ID = id
		if _, err := store.Save(b); err != nil {
			t.Fatal(err)
		}
	} else if _, err := store.Save(board.New(id, "Launch brainstorm", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	// The person's note, there before the agent starts.
	if still == "" {
		if _, err := store.Apply(id, board.Op{Source: board.Person, Blocks: []board.Block{{
			ID: "goal", Type: "sticky", X: 120, Y: 110, W: 220, H: 170,
			Props: json.RawMessage(`{"text":"Goal: ship the live whiteboard","color":"yellow"}`),
		}}}); err != nil {
			t.Fatal(err)
		}
	}

	type input = struct {
		Board string   `json:"board"`
		Op    board.Op `json:"op"`
	}
	mux := http.NewServeMux()
	handle := func(path string, f func(in input) (any, error)) {
		mux.HandleFunc("POST /__board/live/"+path, func(w http.ResponseWriter, r *http.Request) {
			var in input
			if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&in); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			out, err := f(in)
			if err != nil {
				http.Error(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
		})
	}
	handle("load", func(in input) (any, error) { return svc.Load(in.Board) })
	handle("apply", func(in input) (any, error) { return svc.Apply(in.Board, in.Op) })
	handle("undo", func(in input) (any, error) { return svc.Undo(in.Board) })
	handle("redo", func(in input) (any, error) { return svc.Redo(in.Board) })
	mux.HandleFunc("GET /__board/live/events", hub.serve)

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	fmt.Printf("live board demo: serving board %s on 127.0.0.1:%s\n", id, port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Stop when Vite, which started it, has gone.
	if pid, _ := strconv.Atoi(os.Getenv("TORU_BOARD_DEMO_PARENT")); pid > 0 {
		go func() {
			for processAlive(pid) {
				time.Sleep(time.Second)
			}
			cancel()
		}()
	}
	// The agent builds while a window watches, clears what it made, and
	// builds again, so whoever opens the page sees it happen.
	go func() {
		for still == "" && ctx.Err() == nil {
			if hub.watchers() == 0 {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			time.Sleep(1200 * time.Millisecond)
			if err := buildDemo(ctx, reg, id); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Println("live board demo: the agent stopped:", err)
				return
			}
			time.Sleep(6 * time.Second)
			if err := clearDemo(ctx, reg, store, id); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Println("live board demo: the agent stopped:", err)
				return
			}
		}
	}()
	<-ctx.Done()
	_ = srv.Close()
}

// processAlive reports whether process pid still runs.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid) // on Windows this fails once it has gone
	if err != nil {
		return false
	}
	defer func() { _ = p.Release() }()
	if runtime.GOOS == "windows" {
		return true
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// buildDemo is the agent: it builds a small brainstorm through the board
// commands, as an MCP client named "Claude Code", one change at a time.
func buildDemo(ctx context.Context, reg *agentctl.Registry, id string) error {
	ctx = agentctl.WithCaller(ctx, "Claude Code")
	steps := []struct{ cmd, in string }{
		{"board.add", `{"frames":[{"id":"ideas","name":"Ideas","x":400,"y":80,"w":760,"h":260,"color":"blue"}]}`},
		{"board.add", `{"blocks":[{"id":"i1","type":"sticky","x":420,"y":130,"w":220,"h":170,"frame":"ideas","props":{"text":"Agents build boards while you watch","color":"green"}}]}`},
		{"board.add", `{"blocks":[{"id":"i2","type":"sticky","x":670,"y":130,"w":220,"h":170,"frame":"ideas","props":{"text":"Ctrl+Z undoes anyone's change","color":"pink"}}]}`},
		{"board.add", `{"blocks":[{"id":"i3","type":"sticky","x":920,"y":130,"w":220,"h":170,"frame":"ideas","props":{"text":"Every block shows who made it","color":"blue"}}]}`},
		{"board.connect", `{"links":[{"from":"goal","to":"i1","fromPort":"right","toPort":"left"}]}`},
		{"board.draw", `{"strokes":[` + demoLoop(230, 195, 145, 118) + `,{"points":[[430,320],[560,326],[700,322],[860,328],[1000,324],[1130,330]],"color":"ink-green","width":"bold"}]}`},
		{"board.add", `{"blocks":[{"id":"vote","type":"poll","x":420,"y":400,"w":300,"h":230,"props":{"question":"Which first?","options":[{"label":"Live sync","votes":2},{"label":"Export"},{"label":"Templates"}]}}]}`},
		{"board.add", `{"blocks":[{"id":"todo","type":"checklist","x":760,"y":400,"w":280,"h":230,"props":{"title":"Next","items":[{"text":"Sync window and Go","done":true},{"text":"Shared undo","done":true},{"text":"Who made what"}]}}]}`},
		{"board.connect", `{"links":[{"from":"vote","to":"todo","fromPort":"right","toPort":"left"}]}`},
		{"board.update", `{"blocks":[{"id":"i1","props":{"text":"Agents build boards live, while you watch"}}]}`},
	}
	for _, s := range steps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(650 * time.Millisecond):
		}
		in := `{"id":"` + id + `",` + strings.TrimPrefix(s.in, "{")
		if _, err := reg.Call(ctx, s.cmd, json.RawMessage(in)); err != nil {
			return fmt.Errorf("%s: %w", s.cmd, err)
		}
	}
	return nil
}

// demoLoop is a hand-drawn ring round (cx, cy), as board.draw takes a
// stroke: a little more than one turn, wobbling, in the yellow pen.
func demoLoop(cx, cy, rx, ry float64) string {
	var pts []string
	for i := 0; i <= 52; i++ {
		a := -2.2 + float64(i)/48*2*math.Pi
		w := 1 + 0.04*math.Sin(3*a)
		pts = append(pts, fmt.Sprintf("[%.1f,%.1f,%.2f]", cx+rx*w*math.Cos(a), cy+ry*w*math.Sin(a), 0.4+0.3*math.Sin(a/2)*math.Sin(a/2)))
	}
	return `{"points":[` + strings.Join(pts, ",") + `],"color":"ink-yellow","width":"medium"}`
}

// clearDemo takes the agent's blocks, frame and ink off the board again, in one
// change (their lines go with them), leaving what the person made.
func clearDemo(ctx context.Context, reg *agentctl.Registry, store *board.Store, id string) error {
	b, err := store.Load(id)
	if err != nil {
		return err
	}
	var items []string
	for _, x := range b.Blocks {
		if x.CreatedBy.IsAgent() {
			items = append(items, x.ID)
		}
	}
	for _, x := range b.Frames {
		if x.CreatedBy.IsAgent() {
			items = append(items, x.ID)
		}
	}
	for _, x := range b.Ink {
		if x.CreatedBy.IsAgent() {
			items = append(items, x.ID)
		}
	}
	if len(items) == 0 {
		return nil
	}
	in, _ := json.Marshal(map[string]any{"id": id, "items": items, "confirm": true})
	_, err = reg.Call(agentctl.WithCaller(ctx, "Claude Code"), "board.delete", in)
	return err
}

// opHub sends each board's ops to the windows watching it, as server-sent
// events, in the order the store applied them.
type opHub struct {
	mu   sync.Mutex
	subs map[chan []byte]string
}

func (h *opHub) watchers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *opHub) send(id string, op board.Op) {
	data, err := json.Marshal(op)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch, b := range h.subs {
		if b == id {
			select {
			case ch <- data:
			default: // a window that can't keep up reloads on the gap
			}
		}
	}
}

func (h *opHub) serve(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no streaming", http.StatusInternalServerError)
		return
	}
	ch := make(chan []byte, 256)
	h.mu.Lock()
	h.subs[ch] = r.URL.Query().Get("board")
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = io.WriteString(w, ": watching\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case data := <-ch:
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
