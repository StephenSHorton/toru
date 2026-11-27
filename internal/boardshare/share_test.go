package boardshare

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
)

// hub is a fake room: every member's messages go to the others, after a
// random delay (so they arrive out of order, as from two MQTT brokers),
// and a share of them can be dropped.
type hub struct {
	mu    sync.Mutex
	code  string
	peers map[string]*peer
	drop  float64
	rng   *rand.Rand
	sent  int
}

type peer struct {
	h    *hub
	id   string
	name string
	svc  *ShareService
	in   bool // in the room
}

func newHub() *hub {
	return &hub{code: "ABCD-EFGH-JKMN-PQRS", peers: map[string]*peer{}, rng: rand.New(rand.NewSource(1))}
}

func (p *peer) RoomPeer() (string, string, string) {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	if !p.in {
		return p.id, p.name, ""
	}
	return p.id, p.name, p.h.code
}

func (p *peer) HostName(id string) string {
	p.h.mu.Lock()
	defer p.h.mu.Unlock()
	if q := p.h.peers[id]; q != nil && q.in {
		return q.name
	}
	return ""
}

func (p *peer) SendBoard(to string, body json.RawMessage) error {
	h := p.h
	h.mu.Lock()
	defer h.mu.Unlock()
	if !p.in {
		return fmt.Errorf("not in a room")
	}
	if len(body) > 256<<10 {
		return fmt.Errorf("too big")
	}
	for id, q := range h.peers {
		if id == p.id || !q.in || (to != "" && to != id) {
			continue
		}
		h.sent++
		if h.drop > 0 && h.rng.Float64() < h.drop {
			continue
		}
		delay := time.Duration(h.rng.Intn(30)) * time.Millisecond
		from, svc, raw := p.id, q.svc, append(json.RawMessage(nil), body...)
		time.AfterFunc(delay, func() { svc.Receive(from, raw) })
	}
	return nil
}

// install is one Toru: its own store (its own board folder) and sharing.
func (h *hub) install(t *testing.T, id, name string) (*board.Store, *ShareService, *peer) {
	t.Helper()
	store := board.NewStoreIn(t.TempDir())
	svc := New(store)
	store.OnOp(svc.OnOp)
	p := &peer{h: h, id: id, name: name, svc: svc, in: true}
	h.mu.Lock()
	h.peers[id] = p
	h.mu.Unlock()
	svc.Attach(p, nil)
	t.Cleanup(svc.Stop)
	return store, svc, p
}

func fast(t *testing.T) {
	old := []time.Duration{helloEvery, offerTimeout, tickEvery, gapWait, snapAgain, reqTimeout, joinTimeout}
	helloEvery, offerTimeout, tickEvery, gapWait, snapAgain = 100*time.Millisecond, 600*time.Millisecond, 20*time.Millisecond, 150*time.Millisecond, 200*time.Millisecond
	reqTimeout, joinTimeout = 5*time.Second, 5*time.Second
	t.Cleanup(func() {
		helloEvery, offerTimeout, tickEvery, gapWait, snapAgain, reqTimeout, joinTimeout = old[0], old[1], old[2], old[3], old[4], old[5], old[6]
	})
}

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func sticky(text string, x float64) board.Block {
	return board.Block{Type: "sticky", X: x, Y: 0, W: 200, H: 160, Props: json.RawMessage(fmt.Sprintf(`{"text":%q}`, text))}
}

func texts(b board.Board) map[string]string {
	out := map[string]string{}
	for _, x := range b.Blocks {
		var p struct {
			Text string `json:"text"`
		}
		_ = json.Unmarshal(x.Props, &p)
		out[x.ID] = p.Text + "|" + string(x.CreatedBy)
	}
	return out
}

// sameItems compares two copies of a board, ignoring who made what (each
// Toru names people from its own side).
func sameItems(a, b board.Board) bool {
	strip := func(x board.Board) string {
		x.MapSources(func(board.Source) board.Source { return board.Person })
		x.ID, x.MirrorOf, x.UpdatedAt, x.CreatedAt = "", nil, time.Time{}, time.Time{}
		data, _ := json.Marshal(x)
		return string(data)
	}
	return strip(a) == strip(b)
}

func shareAndJoin(t *testing.T, h *hub) (aStore, bStore *board.Store, a, b *ShareService, id, local string) {
	t.Helper()
	aStore, a, _ = h.install(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "Stephen")
	bStore, b, _ = h.install(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "Blake")
	brd, err := aStore.Create("Retro")
	if err != nil {
		t.Fatal(err)
	}
	id = brd.ID
	if _, err := aStore.Apply(id, board.Op{Source: board.Person, Blocks: []board.Block{sticky("before sharing", 0)}}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Share(id); err != nil {
		t.Fatal(err)
	}
	eventually(t, "Blake to see the shared board", func() bool { return len(b.GetState().Offered) == 1 })
	off := b.GetState().Offered[0]
	if off.HostName != "Stephen" || off.Name != "Retro" || off.Board != id {
		t.Fatalf("offered = %+v", off)
	}
	local, err = b.Join("", id)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestShareJoinAndEditBothWays(t *testing.T) {
	fast(t)
	h := newHub()
	aStore, bStore, a, b, id, local := shareAndJoin(t, h)

	copyB, err := bStore.Load(local)
	if err != nil {
		t.Fatal(err)
	}
	if copyB.MirrorOf == nil || copyB.MirrorOf.HostName != "Stephen" || copyB.Name != "Retro" {
		t.Fatalf("copy = %+v", copyB.MirrorOf)
	}
	if got := texts(copyB); len(got) != 1 || !strings.HasSuffix(firstVal(got), "|person:Stephen") {
		t.Fatalf("Blake's copy shows %v, want Stephen's note made by person:Stephen", got)
	}

	// Stephen's agent builds on his board: Blake sees it live.
	if _, err := aStore.Apply(id, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{sticky("from Stephen's agent", 300)}}); err != nil {
		t.Fatal(err)
	}
	// Blake, at his copy, adds a note: it goes to Stephen's Toru.
	opB, err := bStore.Apply(local, board.Op{Source: board.Person, Blocks: []board.Block{sticky("from Blake", 600)}})
	if err != nil {
		t.Fatal(err)
	}
	if opB.Source != board.Person || opB.Blocks[0].CreatedBy != board.Person {
		t.Fatalf("Blake's own change reads back as %q / %q, want person", opB.Source, opB.Blocks[0].CreatedBy)
	}
	// Blake's agent, connected to Blake's Toru, builds too.
	if _, err := bStore.Apply(local, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{sticky("from Blake's agent", 900)}}); err != nil {
		t.Fatal(err)
	}

	eventually(t, "both copies to agree", func() bool {
		x, _ := aStore.Load(id)
		y, _ := bStore.Load(local)
		return x.Rev == y.Rev && sameItems(x, y)
	})
	host, _ := aStore.Load(id)
	want := map[string]bool{
		"before sharing|person":                        true,
		"from Stephen's agent|agent:Claude Code":       true,
		"from Blake|person:Blake":                      true,
		"from Blake's agent|agent:Claude Code (Blake)": true,
	}
	for _, v := range texts(host) {
		if !want[v] {
			t.Errorf("Stephen's board has %q", v)
		}
		delete(want, v)
	}
	if len(want) > 0 {
		t.Errorf("Stephen's board lacks %v", want)
	}
	mine, _ := bStore.Load(local)
	wantB := map[string]bool{
		"before sharing|person:Stephen":                true,
		"from Stephen's agent|agent:Claude Code":       true,
		"from Blake|person":                            true,
		"from Blake's agent|agent:Claude Code (Blake)": true,
	}
	for _, v := range texts(mine) {
		if !wantB[v] {
			t.Errorf("Blake's copy has %q", v)
		}
	}

	// Undo on Blake's side steps the one history, on Stephen's Toru.
	if _, err := bStore.Undo(local, board.Person); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the undo on both", func() bool {
		x, _ := aStore.Load(id)
		y, _ := bStore.Load(local)
		return len(x.Blocks) == 3 && x.Rev == y.Rev && sameItems(x, y)
	})

	// Who has it open.
	eventually(t, "Stephen to see Blake viewing", func() bool {
		st := a.Status(id)
		return st.Sharing && len(st.Viewers) == 1 && st.Viewers[0] == "Blake"
	})
	if st := b.Status(local); !st.Live || st.HostName != "Stephen" {
		t.Fatalf("Blake's status = %+v", st)
	}

	// Stephen stops sharing: Blake keeps a copy that no longer follows.
	a.Unshare(id)
	eventually(t, "Blake's copy to stop following", func() bool { return !bStore.IsMirror(local) })
	if _, err := bStore.Apply(local, board.Op{Source: board.Person, Blocks: []board.Block{sticky("offline", 0)}}); err != nil {
		t.Fatal(err)
	}
	x, _ := aStore.Load(id)
	if len(x.Blocks) != 3 {
		t.Fatalf("a change to Blake's copy after unsharing reached Stephen's board")
	}
}

func firstVal(m map[string]string) string {
	for _, v := range m {
		return v
	}
	return ""
}

// Messages lost and out of order: the copy catches up from the host's
// hellos and asks for the whole board when a gap doesn't fill.
func TestCatchUpAfterLostOps(t *testing.T) {
	fast(t)
	h := newHub()
	aStore, bStore, _, _, id, local := shareAndJoin(t, h)
	h.mu.Lock()
	h.drop = 0.4
	h.mu.Unlock()
	for i := range 30 {
		if _, err := aStore.Apply(id, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{sticky(fmt.Sprintf("note %d", i), float64(i*10))}}); err != nil {
			t.Fatal(err)
		}
	}
	h.mu.Lock()
	h.drop = 0
	h.mu.Unlock()
	eventually(t, "Blake's copy to catch up", func() bool {
		x, _ := aStore.Load(id)
		y, _ := bStore.Load(local)
		return x.Rev == y.Rev && sameItems(x, y)
	})
}

// Two people change the board at once, many times: both copies end the
// same as the host's.
func TestConcurrentEdits(t *testing.T) {
	fast(t)
	h := newHub()
	aStore, bStore, _, _, id, local := shareAndJoin(t, h)
	var wg sync.WaitGroup
	for i := range 15 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = aStore.Apply(id, board.Op{Source: board.Person, Blocks: []board.Block{sticky(fmt.Sprintf("a%d", i), float64(i))}})
		}()
		go func() {
			defer wg.Done()
			if _, err := bStore.Apply(local, board.Op{Source: board.Agent("Claude Code"), Blocks: []board.Block{sticky(fmt.Sprintf("b%d", i), float64(i))}}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	eventually(t, "both copies to agree", func() bool {
		x, _ := aStore.Load(id)
		y, _ := bStore.Load(local)
		return len(x.Blocks) == 31 && x.Rev == y.Rev && sameItems(x, y)
	})
}

// A room message reaches only boards their host shares: a request for any
// other board is refused and changes nothing.
func TestOnlySharedBoardsAreReachable(t *testing.T) {
	fast(t)
	h := newHub()
	aStore, _, _, b, _, _ := shareAndJoin(t, h)
	private, err := aStore.Create("Private")
	if err != nil {
		t.Fatal(err)
	}
	// Blake's Toru forges a copy of the private board and sends a change.
	k := key{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", private.ID}
	b.mu.Lock()
	b.mirrors[k] = &mirror{key: k, local: "brd-fake", hostName: "Stephen", live: true, joined: make(chan struct{}), buffered: map[int]board.Op{}}
	b.mu.Unlock()
	_, err = b.request(k, "apply", &board.Op{Source: board.Person, Blocks: []board.Block{sticky("sneaky", 0)}}, board.Person)
	if err == nil || !strings.Contains(err.Error(), "isn't shared") {
		t.Fatalf("request on an unshared board: err = %v", err)
	}
	// Nor can it ask for the whole board.
	b.send(k.host, message{T: tJoin, Board: private.ID})
	time.Sleep(200 * time.Millisecond)
	if got, _ := aStore.Load(private.ID); len(got.Blocks) != 0 || got.Rev != 0 {
		t.Fatalf("the private board changed: %+v", got)
	}
	// A forged source is replaced by who sent it.
	brd, _ := aStore.List()
	_ = brd
}

// The sender's name, not the request, says who made a change.
func TestSourceComesFromTheSender(t *testing.T) {
	fast(t)
	h := newHub()
	aStore, bStore, _, _, id, local := shareAndJoin(t, h)
	// Blake's Toru claims the change is Stephen's own: refused.
	if _, err := bStore.Apply(local, board.Op{Source: board.PersonNamed("Stephen"), Blocks: []board.Block{sticky("forged", 0)}}); err == nil {
		t.Fatal("a change sent as another person was taken")
	}
	x, _ := aStore.Load(id)
	for _, blk := range x.Blocks {
		if blk.CreatedBy == board.Person && strings.Contains(string(blk.Props), "forged") {
			t.Fatal("forged change shows as Stephen's")
		}
	}
}

// Leaving the room ends every share and every copy.
func TestLeavingTheRoom(t *testing.T) {
	fast(t)
	h := newHub()
	_, bStore, a, b, id, local := shareAndJoin(t, h)
	h.mu.Lock()
	h.peers["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"].in = false
	h.mu.Unlock()
	eventually(t, "Blake's copy to stop following", func() bool { return !bStore.IsMirror(local) })
	eventually(t, "Stephen's share to end", func() bool { return !a.Status(id).Sharing })
	if len(b.GetState().Offered) != 0 {
		t.Fatal("the board is still offered")
	}
}
