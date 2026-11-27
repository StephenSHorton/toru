package boardshare

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/stream"
)

var e2e = flag.Bool("e2e", false, "run the checks that use the real room (needs the internet)")

// realRoom is one Toru's side of a real room (stream.OpenRoom over the
// public MQTT brokers Call and LAN use), carrying only board messages.
type realRoom struct {
	self, name, code string
	room             *stream.Room
	names            map[string]string
}

func (r *realRoom) RoomPeer() (string, string, string) { return r.self, r.name, r.code }
func (r *realRoom) HostName(peer string) string        { return r.names[peer] }
func (r *realRoom) SendBoard(to string, body json.RawMessage) error {
	return r.room.Send(to, "board", body)
}

// TestRealRoom shares a board between two Torus through the real room:
// sealed messages on the public brokers, with big messages in parts.
// TORU_BOARDSHARE_E2E=1 runs it (it needs the internet).
func TestRealRoom(t *testing.T) {
	if os.Getenv("TORU_BOARDSHARE_E2E") == "" && !*e2e {
		t.Skip("set TORU_BOARDSHARE_E2E=1 or pass -args -e2e to run (needs the internet)")
	}
	code := stream.NewCode()
	names := map[string]string{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "Stephen", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": "Blake"}
	open := func(self string) (*board.Store, *ShareService, *realRoom) {
		store := board.NewStoreIn(t.TempDir())
		svc := New(store)
		store.OnOp(svc.OnOp)
		r := &realRoom{self: self, name: names[self], code: code, names: names}
		room, err := stream.OpenRoom(code, self, func(env stream.Envelope) {
			if env.Type == "board" {
				svc.Receive(env.From, env.Body)
			}
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.room = room
		svc.Attach(r, nil)
		t.Cleanup(func() { svc.Stop(); room.Close() })
		return store, svc, r
	}
	before := listening(t)
	t.Logf("ports this process listens on before: %v", before)
	aStore, a, ar := open("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	bStore, b, br := open("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	wait := func(what string, d time.Duration, f func() bool) {
		t.Helper()
		deadline := time.Now().Add(d)
		for !f() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	wait("both Torus to reach the brokers", 30*time.Second, func() bool { return ar.room.Connected() > 0 && br.room.Connected() > 0 })
	time.Sleep(time.Second)

	brd, _ := aStore.Create("Real room")
	id := brd.ID
	// A big note, so the board goes in parts.
	big := strings.Repeat("A long note. ", 12000)
	if _, err := aStore.Apply(id, board.Op{Source: board.Person, Blocks: []board.Block{sticky(big, 0)}}); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := a.Share(id); err != nil {
		t.Fatal(err)
	}
	wait("Blake to see the board", 20*time.Second, func() bool { return len(b.GetState().Offered) == 1 })
	local, err := b.Join("", id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("shared and joined in %v", time.Since(start))

	t0 := time.Now()
	if _, err := aStore.Apply(id, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{sticky("from Stephen's agent", 300)}}); err != nil {
		t.Fatal(err)
	}
	wait("Stephen's agent's note on Blake's copy", 10*time.Second, func() bool { return bStore.Rev(local) == aStore.Rev(id) })
	t.Logf("host op reached the copy in %v", time.Since(t0))

	t0 = time.Now()
	if _, err := bStore.Apply(local, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{sticky("from Blake's agent", 600)}}); err != nil {
		t.Fatal(err)
	}
	t.Logf("Blake's agent's change made on Stephen's Toru and back in %v", time.Since(t0))
	wait("both to agree", 10*time.Second, func() bool {
		x, _ := aStore.Load(id)
		y, _ := bStore.Load(local)
		return x.Rev == y.Rev && sameItems(x, y)
	})
	host, _ := aStore.Load(id)
	got := []string{}
	for _, v := range texts(host) {
		if len(v) < 80 {
			got = append(got, v)
		}
	}
	t.Logf("Stephen's board: %s", strings.Join(got, "; "))
	after := listening(t)
	t.Logf("ports this process listens on with the board shared, joined and edited both ways: %v", after)
	if len(after) != len(before) {
		t.Fatalf("sharing opened a port: before %v, after %v", before, after)
	}
	if !strings.Contains(fmt.Sprint(got), "from Blake's agent|agent:Claude Code (Blake)") {
		t.Fatalf("Blake's agent's note isn't on Stephen's board as theirs: %v", got)
	}
}
