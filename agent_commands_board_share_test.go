package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/boardshare"
)

// lanRoom is a room in memory: each Toru's messages reach the others.
type lanRoom struct {
	mu    sync.Mutex
	peers map[string]*memberRoom
}

type memberRoom struct {
	r          *lanRoom
	self, name string
	share      *boardshare.ShareService
}

func (m *memberRoom) RoomPeer() (string, string, string) { return m.self, m.name, "TEST-ROOM" }
func (m *memberRoom) HostName(peer string) string {
	m.r.mu.Lock()
	defer m.r.mu.Unlock()
	if p := m.r.peers[peer]; p != nil {
		return p.name
	}
	return ""
}
func (m *memberRoom) SendBoard(to string, body json.RawMessage) error {
	m.r.mu.Lock()
	defer m.r.mu.Unlock()
	for id, p := range m.r.peers {
		if id != m.self && (to == "" || to == id) {
			go p.share.Receive(m.self, append(json.RawMessage(nil), body...))
		}
	}
	return nil
}

// Each person's agent, through its own Toru's commands, finds the board
// shared in the room, joins it and builds on it; the owner's board shows
// the change as that agent's, with its person's name.
func TestBoardShareAgentCommands(t *testing.T) {
	room := &lanRoom{peers: map[string]*memberRoom{}}
	install := func(self, name string) (*board.Store, *boardshare.ShareService, *agentctl.Registry) {
		store := board.NewStoreIn(t.TempDir())
		share := boardshare.New(store)
		store.OnOp(share.OnOp)
		m := &memberRoom{r: room, self: self, name: name, share: share}
		room.mu.Lock()
		room.peers[self] = m
		room.mu.Unlock()
		share.Attach(m, nil)
		t.Cleanup(share.Stop)
		reg := agentctl.NewRegistry()
		registerBoardAgentCommands(reg, boardAgentDeps{boards: store})
		registerBoardShareAgentCommands(reg, share, nil)
		return store, share, reg
	}
	aStore, aShare, aReg := install("a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0", "Stephen")
	_, _, bReg := install("b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0", "Blake")
	ctx := agentctl.WithCaller(context.Background(), "Claude Code")

	out, err := aReg.Call(ctx, "board.create", json.RawMessage(`{"name":"Plan"}`))
	if err != nil {
		t.Fatal(err)
	}
	id := out.(map[string]any)["id"].(string)
	if _, err := aShare.Share(id); err != nil {
		t.Fatal(err)
	}
	// Agents can't share: there is no command for it.
	if _, err := aReg.Call(ctx, "board.share", json.RawMessage(`{"board":"`+id+`"}`)); err == nil {
		t.Fatal("an agent shared a board")
	}

	var local string
	deadline := time.Now().Add(10 * time.Second)
	for local == "" {
		if time.Now().After(deadline) {
			t.Fatal("Blake's agent never saw the shared board")
		}
		st, err := bReg.Call(ctx, "board.shared", nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(st.(boardshare.State).Offered) == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		j, err := bReg.Call(ctx, "board.join", json.RawMessage(`{"board":"`+id+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		local = j.(map[string]any)["board"].(string)
	}
	if _, err := bReg.Call(ctx, "board.add", json.RawMessage(`{"id":"`+local+`","blocks":[{"type":"sticky","props":{"text":"from Blake's agent"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := aReg.Call(ctx, "board.add", json.RawMessage(`{"id":"`+id+`","blocks":[{"type":"sticky","props":{"text":"from Stephen's agent"}}]}`)); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		md, err := bReg.Call(ctx, "board.read", json.RawMessage(`{"id":"`+local+`","format":"markdown"}`))
		if err == nil && strings.Contains(toJSON(md), "from Stephen's agent") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Stephen's agent's note never reached Blake's copy: %v", md)
		}
		time.Sleep(50 * time.Millisecond)
	}
	b, _ := aStore.Load(id)
	found := false
	for _, x := range b.Blocks {
		if strings.Contains(string(x.Props), "from Blake's agent") {
			found = true
			if x.CreatedBy != board.Agent("Claude Code (Blake)") {
				t.Fatalf("Blake's agent's note on Stephen's board is by %q", x.CreatedBy)
			}
		}
	}
	if !found {
		t.Fatal("Blake's agent's note isn't on Stephen's board")
	}
}

func toJSON(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}
