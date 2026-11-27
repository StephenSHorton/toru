package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/boardshare"
	"github.com/StephenSHorton/toru/internal/stream"
)

// TestBoardShareDemo is the shared board's demo server, not a check: it
// runs only with TORU_BOARD_SHARE_DEMO_PORT set, which Vite's boardLiveDemo
// plugin (frontend/vite.config.ts) does in its share-demo mode:
//
//	cd frontend && bun x vite --mode share-demo --port 47820
//	open http://127.0.0.1:47820/share-demo.html
//
// Two Torus, "Stephen" and "Blake", each with its own board store (its own
// folder), its own sharing service and its own agent command registry, in
// one fresh room on the real public brokers Call and LAN use. Stephen's
// Toru shares a board; Blake's joins it. Each Toru's board window is served
// over HTTP (Stephen's at /__board/live/, Blake's at /__board/live-b/), so
// share-demo.html shows both side by side. Once both windows watch,
// Stephen's agent and Blake's agent, each talking only to its own Toru,
// build on the board in turns: every change crosses the room to the other.
func TestBoardShareDemo(t *testing.T) {
	port := os.Getenv("TORU_BOARD_SHARE_DEMO_PORT")
	if port == "" {
		t.Skip("the shared board demo server runs with TORU_BOARD_SHARE_DEMO_PORT (see frontend/vite.config.ts)")
	}
	code := stream.NewCode()
	names := map[string]string{}
	type install struct {
		store *board.Store
		share *boardshare.ShareService
		reg   *agentctl.Registry
		hub   *opHub
		room  *demoRoom
	}
	open := func(self, name string) *install {
		names[self] = name
		store := board.NewStoreIn(t.TempDir())
		share := boardshare.New(store)
		hub := &opHub{subs: map[chan []byte]string{}}
		store.OnOp(func(id string, op board.Op) { hub.send(id, op); share.OnOp(id, op) })
		r := &demoRoom{self: self, name: name, code: code, names: names}
		room, err := stream.OpenRoom(code, self, func(env stream.Envelope) {
			if env.Type == "board" {
				share.Receive(env.From, env.Body)
			}
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.room = room
		share.Attach(r, nil)
		reg := agentctl.NewRegistry()
		registerBoardAgentCommands(reg, boardAgentDeps{boards: store})
		registerBoardShareAgentCommands(reg, share, nil)
		t.Cleanup(func() { share.Stop(); room.Close() })
		return &install{store: store, share: share, reg: reg, hub: hub, room: r}
	}
	stephen := open("5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a5a", "Stephen")
	blake := open("b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1b1", "Blake")
	for stephen.room.room.Connected() == 0 || blake.room.room.Connected() == 0 {
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second)

	const id = "brd-demo"
	if _, err := stephen.store.Save(board.New(id, "Launch brainstorm", time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	if _, err := stephen.store.Apply(id, board.Op{Source: board.Person, Blocks: []board.Block{{
		ID: "goal", Type: "sticky", X: 80, Y: 80, W: 220, H: 170,
		Props: json.RawMessage(`{"text":"Goal: build this board together","color":"yellow"}`),
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stephen.share.Share(id); err != nil {
		t.Fatal(err)
	}
	// Blake's agent finds the shared board and joins it, through Blake's Toru.
	ctxB := agentctl.WithCaller(context.Background(), "Claude Code")
	var local string
	for local == "" {
		st := blake.share.GetState()
		if len(st.Offered) == 1 {
			out, err := blake.reg.Call(ctxB, "board.join", json.RawMessage(`{"board":"`+id+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			local = out.(map[string]any)["board"].(string)
		} else {
			time.Sleep(200 * time.Millisecond)
		}
	}
	fmt.Printf("shared board demo: room %s, Stephen's board %s, Blake's copy %s\n", code, id, local)

	mux := http.NewServeMux()
	serve := func(prefix string, in *install) {
		svc := board.NewService(in.store)
		type input = struct {
			Board string   `json:"board"`
			Op    board.Op `json:"op"`
		}
		handle := func(path string, f func(in input) (any, error)) {
			mux.HandleFunc("POST "+prefix+path, func(w http.ResponseWriter, r *http.Request) {
				var x input
				if err := json.NewDecoder(io.LimitReader(r.Body, 32<<20)).Decode(&x); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				out, err := f(x)
				if err != nil {
					http.Error(w, err.Error(), http.StatusUnprocessableEntity)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(out)
			})
		}
		handle("load", func(x input) (any, error) { return svc.Load(x.Board) })
		handle("apply", func(x input) (any, error) { return svc.Apply(x.Board, x.Op) })
		handle("undo", func(x input) (any, error) { return svc.Undo(x.Board) })
		handle("redo", func(x input) (any, error) { return svc.Redo(x.Board) })
		mux.HandleFunc("GET "+prefix+"events", in.hub.serve)
		mux.HandleFunc("POST "+prefix+"status", func(w http.ResponseWriter, r *http.Request) {
			var x struct {
				Board string `json:"board"`
			}
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&x)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(in.share.Status(x.Board))
		})
	}
	serve("/__board/live/", stephen)
	serve("/__board/live-b/", blake)

	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	fmt.Printf("shared board demo: serving on 127.0.0.1:%s\n", port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if pid, _ := strconv.Atoi(os.Getenv("TORU_BOARD_DEMO_PARENT")); pid > 0 {
		go func() {
			for processAlive(pid) {
				time.Sleep(time.Second)
			}
			cancel()
		}()
	}
	go func() {
		for ctx.Err() == nil {
			if stephen.hub.watchers() == 0 || blake.hub.watchers() == 0 {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			time.Sleep(700 * time.Millisecond)
			if err := buildShareDemo(ctx, stephen.reg, blake.reg, id, local); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Println("shared board demo: the agents stopped:", err)
				return
			}
			time.Sleep(8 * time.Second)
			if err := clearDemo(ctx, stephen.reg, stephen.store, id); err != nil && !errors.Is(err, context.Canceled) {
				fmt.Println("shared board demo: the agents stopped:", err)
				return
			}
		}
	}()
	<-ctx.Done()
	_ = srv.Close()
}

// buildShareDemo is two agents, one at each Toru, building one shared board
// in turns, each through its own Toru's board commands.
func buildShareDemo(ctx context.Context, regA, regB *agentctl.Registry, idA, idB string) error {
	ctx = agentctl.WithCaller(ctx, "Claude Code")
	type step struct {
		reg      *agentctl.Registry
		id, cmd  string
		in       string
	}
	steps := []step{
		{regA, idA, "board.add", `{"frames":[{"id":"ideas","name":"Ideas","x":360,"y":60,"w":560,"h":250,"color":"blue"}]}`},
		{regA, idA, "board.add", `{"blocks":[{"id":"s1","type":"sticky","x":380,"y":110,"w":160,"h":150,"frame":"ideas","props":{"text":"Stephen's agent: one board, two Torus","color":"green"}}]}`},
		{regB, idB, "board.add", `{"blocks":[{"id":"b1","type":"sticky","x":560,"y":110,"w":160,"h":150,"frame":"ideas","props":{"text":"Blake's agent: building from Blake's Toru","color":"pink"}}]}`},
		{regA, idA, "board.connect", `{"links":[{"from":"goal","to":"s1","fromPort":"right","toPort":"left"}]}`},
		{regB, idB, "board.add", `{"blocks":[{"id":"b2","type":"sticky","x":740,"y":110,"w":160,"h":150,"frame":"ideas","props":{"text":"Every change crosses the room live","color":"blue"}}]}`},
		{regB, idB, "board.connect", `{"links":[{"from":"s1","to":"b1","fromPort":"right","toPort":"left"}]}`},
		{regA, idA, "board.add", `{"blocks":[{"id":"vote","type":"poll","x":80,"y":330,"w":300,"h":230,"props":{"question":"Ship shared boards?","options":[{"label":"Yes","votes":1},{"label":"Not yet"}]}}]}`},
		{regB, idB, "board.update", `{"blocks":[{"id":"vote","props":{"question":"Ship shared boards?","options":[{"label":"Yes","votes":2},{"label":"Not yet"}]}}]}`},
		{regB, idB, "board.draw", `{"strokes":[` + demoLoop(640, 185, 110, 95) + `]}`},
		{regA, idA, "board.update", `{"blocks":[{"id":"s1","props":{"text":"Stephen's agent: one board, two Torus, live"}}]}`},
	}
	for _, s := range steps {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(550 * time.Millisecond):
		}
		in := `{"id":"` + s.id + `",` + strings.TrimPrefix(s.in, "{")
		if _, err := s.reg.Call(ctx, s.cmd, json.RawMessage(in)); err != nil {
			return fmt.Errorf("%s: %w", s.cmd, err)
		}
	}
	return nil
}

// demoRoom is one demo Toru's side of the real room.
type demoRoom struct {
	self, name, code string
	room             *stream.Room
	names            map[string]string
}

func (r *demoRoom) RoomPeer() (string, string, string) { return r.self, r.name, r.code }
func (r *demoRoom) HostName(peer string) string        { return r.names[peer] }
func (r *demoRoom) SendBoard(to string, body json.RawMessage) error {
	return r.room.Send(to, "board", body)
}
