package pipe

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/sys/windows"
)

// Named pipes never touch the network stack or the firewall. The real pipe
// name lives under ProtectedPrefix\Administrators, which a non-elevated test
// cannot create, so each test uses its own name with the production SDDL
// plus FILE_CREATE_PIPE_INSTANCE for the test user: the real server runs as
// SYSTEM, which PipeSDDL lets make the instances after the first, and a
// non-elevated test process is only an interactive user (TestPipeSDDL).

type fakeBackend struct {
	mu      sync.Mutex
	calls   []string
	upErr   error
	state   string
	srv     *Server
	downs   atomic.Int32
	signals []string
}

func (f *fakeBackend) log(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeBackend) Identity(sid string) ([32]byte, netip.Addr, error) {
	var k [32]byte
	copy(k[:], sid)
	return k, netip.MustParseAddr("100.96.1.2"), nil
}

func (f *fakeBackend) Up(o Owner, up api.Up) error {
	f.log(fmt.Sprintf("up %d %s", o.Session, up.Room))
	if f.upErr != nil {
		return f.upErr
	}
	f.mu.Lock()
	f.state = api.StateOn
	f.mu.Unlock()
	f.srv.SendOwner(api.Message{Type: api.TypeStatus, Status: &api.Status{State: api.StateOn, Room: up.Room}})
	return nil
}

func (f *fakeBackend) Down() error {
	f.log("down")
	f.downs.Add(1)
	f.mu.Lock()
	f.state = api.StateOff
	f.mu.Unlock()
	return nil
}

func (f *fakeBackend) Signal(peer string, blob api.SignalBlob) error {
	f.mu.Lock()
	f.signals = append(f.signals, peer+" "+blob.Kind)
	f.mu.Unlock()
	return nil
}
func (f *fakeBackend) Drop(peer string) error         { f.log("drop " + peer); return nil }
func (f *fakeBackend) SetICE(s []api.ICEServer) error { f.log("ice"); return nil }
func (f *fakeBackend) SetPolicy(p api.Policy) error   { f.log("policy"); return nil }
func (f *fakeBackend) Readdress(sid string, salt uint32, running bool) error {
	f.log(fmt.Sprintf("readdress %d %v", salt, running))
	return nil
}
func (f *fakeBackend) Status() api.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	st := f.state
	if st == "" {
		st = api.StateOff
	}
	return api.Status{State: st, Room: "secret-room", IP: "100.96.1.2", Version: "test"}
}

var pipeSeq atomic.Int32

// TestPipeSDDL checks what api.PipeSDDL gives an interactive non-admin (this
// test must run non-elevated to mean anything): connecting works, but
// creating a second server instance of the pipe, which would let it catch the
// GUI's next connection, is refused.
func TestPipeSDDL(t *testing.T) {
	if windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("elevated: Administrators get everything, nothing to check")
	}
	sd, err := windows.SecurityDescriptorFromString(api.PipeSDDL)
	if err != nil {
		t.Fatal(err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	seq := 0
	// listen makes a fresh pipe (a first instance needs no instance right).
	listen := func() *uint16 {
		seq++
		n, _ := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\pipe\toru-lan-sddl-%d-%d`, os.Getpid(), seq))
		h, err := windows.CreateNamedPipe(n, windows.PIPE_ACCESS_DUPLEX|windows.FILE_FLAG_FIRST_PIPE_INSTANCE,
			windows.PIPE_TYPE_BYTE, windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, sa)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { windows.CloseHandle(h) })
		return n
	}

	n := listen()
	second, err := windows.CreateNamedPipe(n, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE, windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	if err == nil {
		windows.CloseHandle(second)
		t.Fatal("a non-admin created a second server instance")
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("second instance: %v, want access denied", err)
	}

	// The GUI's access, and winio's default GENERIC_READ|GENERIC_WRITE
	// (which on a pipe does not include the instance right), both connect.
	for _, access := range []uint32{api.PipeClientAccess, windows.GENERIC_READ | windows.GENERIC_WRITE} {
		h, err := windows.CreateFile(listen(), access, 0, nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			t.Fatalf("open with %#x: %v", access, err)
		}
		windows.CloseHandle(h)
	}
}

// newServer starts a server; ids, when given, are handed out to
// connections in accept order (tests dial one at a time).
func newServer(t *testing.T, b *fakeBackend, idle time.Duration, onIdle func(), ids ...Identity) (*Server, string) {
	t.Helper()
	name := fmt.Sprintf(`\\.\pipe\toru-lan-test-%d-%d`, os.Getpid(), pipeSeq.Add(1))
	cfg := Config{Name: name, SDDL: api.PipeSDDL + "(A;;0x4;;;" + currentSID(t) + ")", Version: "test", Backend: b, IdleTimeout: idle, OnIdle: onIdle, Logf: t.Logf}
	if len(ids) > 0 {
		var mu sync.Mutex
		next := 0
		cfg.Identify = func(net.Conn) (Identity, error) {
			mu.Lock()
			defer mu.Unlock()
			if next >= len(ids) {
				return Identity{}, fmt.Errorf("no identity left")
			}
			next++
			return ids[next-1], nil
		}
	}
	s, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	b.srv = s
	t.Cleanup(func() { s.Close() })
	return s, name
}

type client struct {
	t    *testing.T
	conn net.Conn
	sc   *bufio.Scanner
}

func dial(t *testing.T, name string) *client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := winio.DialPipeAccess(ctx, name, api.PipeClientAccess)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 4096), MaxLine*2)
	return &client{t: t, conn: conn, sc: sc}
}

func (c *client) send(m api.Message) {
	c.t.Helper()
	b, _ := Encode(m)
	if _, err := c.conn.Write(b); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) sendRaw(s string) {
	c.t.Helper()
	if _, err := c.conn.Write([]byte(s + "\n")); err != nil {
		c.t.Fatal(err)
	}
}

// recv returns the next message, or ok=false on EOF/timeout.
func (c *client) recv() (api.Message, bool) {
	c.t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if !c.sc.Scan() {
		return api.Message{}, false
	}
	var m api.Message
	if err := json.Unmarshal(c.sc.Bytes(), &m); err != nil {
		c.t.Fatalf("bad line %q: %v", c.sc.Text(), err)
	}
	return m, true
}

func (c *client) expect(typ string) api.Message {
	c.t.Helper()
	m, ok := c.recv()
	if !ok {
		c.t.Fatalf("connection ended waiting for %s", typ)
	}
	if m.Type != typ {
		c.t.Fatalf("got %s %+v, want %s", m.Type, m, typ)
	}
	return m
}

func (c *client) expectError(code string) {
	c.t.Helper()
	m := c.expect(api.TypeError)
	if m.Error == nil || m.Error.Code != code {
		c.t.Fatalf("error %+v, want %s", m.Error, code)
	}
}

func (c *client) hello() api.Hello {
	c.t.Helper()
	c.send(api.Message{Type: api.TypeHello, ID: 1, Hello: &api.Hello{Major: api.ProtocolMajor, Minor: api.ProtocolMinor}})
	m := c.expect(api.TypeHello)
	if m.ID != 1 {
		c.t.Fatalf("hello reply id %d", m.ID)
	}
	return *m.Hello
}

func upMsg(room string) api.Message {
	return api.Message{Type: api.TypeUp, Up: &api.Up{Room: room, Self: "self1"}}
}

func sigMsg(peer string) api.Message {
	raw, _ := json.Marshal(api.SignalBlob{Kind: api.KindHello})
	return api.Message{Type: api.TypeSignal, Signal: &api.Signal{Peer: peer, Blob: raw}}
}

// TestRealIdentity uses the default pipe-handle path: the client is this
// process, so pid, session and SID are known.
func TestRealIdentity(t *testing.T) {
	b := &fakeBackend{}
	var got Identity
	s, name := newServer(t, b, time.Minute, nil)
	s.cfg.Identify = func(c net.Conn) (Identity, error) {
		id, err := identify(c)
		got = id
		return id, err
	}
	c := dial(t, name)
	h := c.hello()
	if got.PID != uint32(os.Getpid()) {
		t.Errorf("client pid %d, want %d", got.PID, os.Getpid())
	}
	var sess uint32
	windows.ProcessIdToSessionId(uint32(os.Getpid()), &sess)
	if got.Session != sess {
		t.Errorf("client session %d, want %d", got.Session, sess)
	}
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if got.SID != u.User.Sid.String() {
		t.Errorf("client SID %s, want %s", got.SID, u.User.Sid)
	}
	if h.Major != api.ProtocolMajor || h.Version != "test" || h.IP != "100.96.1.2" {
		t.Errorf("hello %+v", h)
	}
	if k, err := base64.StdEncoding.DecodeString(h.PubKey); err != nil || len(k) != 32 {
		t.Errorf("pubKey %q", h.PubKey)
	}
}

func TestHelloFirst(t *testing.T) {
	b := &fakeBackend{}
	_, name := newServer(t, b, time.Minute, nil, Identity{Session: 1, SID: "S-1-5-21-1"})
	c := dial(t, name)
	c.send(api.Message{Type: api.TypeStatus, ID: 5})
	c.expectError(api.ErrProtocol)
	if _, ok := c.recv(); ok {
		t.Fatal("connection still open after a message before hello")
	}
}

func TestProtocolMismatch(t *testing.T) {
	b := &fakeBackend{}
	_, name := newServer(t, b, time.Minute, nil, Identity{Session: 1, SID: "S-1-5-21-1"})
	c := dial(t, name)
	c.send(api.Message{Type: api.TypeHello, ID: 1, Hello: &api.Hello{Major: api.ProtocolMajor + 1}})
	h := c.expect(api.TypeHello)
	if h.Hello.Major != api.ProtocolMajor || h.Hello.Version != "test" {
		t.Fatalf("hello %+v", h.Hello)
	}
	c.expectError(api.ErrProtocol)
	if _, ok := c.recv(); ok {
		t.Fatal("connection still open after a protocol mismatch")
	}
}

func TestBadRequestKeepsConnection(t *testing.T) {
	b := &fakeBackend{}
	_, name := newServer(t, b, time.Minute, nil, Identity{Session: 1, SID: "S-1-5-21-1"})
	c := dial(t, name)
	c.hello()
	c.sendRaw(`{"type":"route","id":9}`)
	c.expectError(api.ErrBadRequest)
	c.send(api.Message{Type: api.TypeStatus, ID: 10})
	if m := c.expect(api.TypeStatus); m.ID != 10 {
		t.Fatalf("status id %d", m.ID)
	}
}

func TestNotUp(t *testing.T) {
	b := &fakeBackend{}
	_, name := newServer(t, b, time.Minute, nil, Identity{Session: 1, SID: "S-1-5-21-1"})
	c := dial(t, name)
	c.hello()
	c.send(sigMsg("p"))
	c.expectError(api.ErrNotUp)
	c.send(api.Message{Type: api.TypeDrop, Drop: &api.Drop{Peer: "p"}})
	c.expectError(api.ErrNotUp)
	c.send(api.Message{Type: api.TypeDown}) // idempotent, no reply
	c.send(api.Message{Type: api.TypeStatus, ID: 2})
	c.expect(api.TypeStatus)
}

// TestWrongSession is the pipe test from the build order: a client from
// another Windows session can't touch, or read, a LAN it does not own.
func TestWrongSession(t *testing.T) {
	b := &fakeBackend{}
	a := Identity{Session: 1, PID: 10, SID: "S-1-5-21-1-1001"}
	other := Identity{Session: 2, PID: 20, SID: "S-1-5-21-1-1002"}
	sameUserOtherSession := Identity{Session: 3, PID: 30, SID: a.SID}
	_, name := newServer(t, b, time.Minute, nil, a, other, sameUserOtherSession)

	ca := dial(t, name)
	ca.hello()
	ca.send(upMsg("room-a"))
	if st := ca.expect(api.TypeStatus); st.Status.State != api.StateOn {
		t.Fatalf("status %+v", st.Status)
	}

	cb := dial(t, name)
	cb.hello()
	cb.send(upMsg("room-b"))
	cb.expectError(api.ErrWrongSession)
	cb.send(sigMsg("p"))
	cb.expectError(api.ErrWrongSession)
	cb.send(api.Message{Type: api.TypeDrop, Drop: &api.Drop{Peer: "p"}})
	cb.expectError(api.ErrWrongSession)
	cb.send(api.Message{Type: api.TypeSetICE, SetICE: &api.SetICE{}})
	cb.expectError(api.ErrWrongSession)
	cb.send(api.Message{Type: api.TypeSetPolicy, SetPolicy: &api.Policy{}})
	cb.expectError(api.ErrWrongSession)
	cb.send(api.Message{Type: api.TypeDown})
	cb.expectError(api.ErrWrongSession)
	cb.send(api.Message{Type: api.TypeStatus, ID: 3})
	st := cb.expect(api.TypeStatus).Status
	if st.Room != "" || st.IP != "" || st.Error != api.ErrWrongSession || st.State != api.StateOn {
		t.Fatalf("non-owner status leaks or is wrong: %+v", st)
	}

	// The same user in another session is still another session.
	cc := dial(t, name)
	cc.hello()
	cc.send(sigMsg("p"))
	cc.expectError(api.ErrWrongSession)

	// The owner is unaffected and gets the full status.
	ca.send(sigMsg("peer1"))
	ca.send(api.Message{Type: api.TypeStatus, ID: 4})
	if st := ca.expect(api.TypeStatus).Status; st.Room != "secret-room" {
		t.Fatalf("owner status %+v", st)
	}
	b.mu.Lock()
	sig := strings.Join(b.signals, ",")
	b.mu.Unlock()
	if sig != "peer1 hello" {
		t.Fatalf("signals %q", sig)
	}

	// After Down, the other session may bring the LAN up.
	ca.send(api.Message{Type: api.TypeDown})
	ca.send(api.Message{Type: api.TypeStatus, ID: 5})
	ca.expect(api.TypeStatus)
	cb.send(upMsg("room-b"))
	if st := cb.expect(api.TypeStatus); st.Status.Room != "room-b" {
		t.Fatalf("status %+v", st.Status)
	}
}

func TestUpFailureReleases(t *testing.T) {
	b := &fakeBackend{upErr: &CodeError{Code: api.ErrRangeInUse, Detail: "Tailscale"}}
	a := Identity{Session: 1, SID: "S-1-5-21-1"}
	o := Identity{Session: 2, SID: "S-1-5-21-2"}
	s, name := newServer(t, b, time.Minute, nil, a, o)
	c := dial(t, name)
	c.hello()
	c.send(upMsg("r"))
	m := c.expect(api.TypeError)
	if m.Error.Code != api.ErrRangeInUse || m.Error.Detail != "Tailscale" {
		t.Fatalf("error %+v", m.Error)
	}
	if _, ok := s.Owner(); ok {
		t.Fatal("still owned after a failed up")
	}
	b.upErr = nil
	c2 := dial(t, name)
	c2.hello()
	c2.send(upMsg("r2"))
	c2.expect(api.TypeStatus)
}

func TestPeerBound(t *testing.T) {
	b := &fakeBackend{}
	_, name := newServer(t, b, time.Minute, nil, Identity{Session: 1, SID: "S-1-5-21-1"})
	c := dial(t, name)
	c.hello()
	c.send(upMsg("r"))
	c.expect(api.TypeStatus)
	for i := 0; i < MaxPeers; i++ {
		c.send(sigMsg(fmt.Sprintf("p%d", i)))
	}
	c.send(sigMsg("p0")) // a known peer is fine
	c.send(sigMsg("one-too-many"))
	c.expectError(api.ErrBadRequest)
	c.send(api.Message{Type: api.TypeDrop, Drop: &api.Drop{Peer: "p1"}})
	c.send(sigMsg("now-fits"))
	c.send(api.Message{Type: api.TypeStatus, ID: 9})
	if m := c.expect(api.TypeStatus); m.ID != 9 {
		t.Fatalf("unexpected %+v", m)
	}
}

func TestReaddressScope(t *testing.T) {
	b := &fakeBackend{}
	a := Identity{Session: 1, SID: "S-1-5-21-1"}
	o := Identity{Session: 2, SID: "S-1-5-21-2"}
	_, name := newServer(t, b, time.Minute, nil, a, o)
	ca := dial(t, name)
	ca.hello()
	ca.send(upMsg("r"))
	ca.expect(api.TypeStatus)
	ca.send(api.Message{Type: api.TypeReaddress, Readdress: &api.Readdress{Salt: 1}})
	cb := dial(t, name)
	cb.hello()
	cb.send(api.Message{Type: api.TypeReaddress, Readdress: &api.Readdress{Salt: 2}})
	cb.send(api.Message{Type: api.TypeStatus, ID: 1})
	cb.expect(api.TypeStatus)
	b.mu.Lock()
	calls := strings.Join(b.calls, ",")
	b.mu.Unlock()
	if !strings.Contains(calls, "readdress 1 true") || !strings.Contains(calls, "readdress 2 false") {
		t.Fatalf("calls %q", calls)
	}
}

// TestOwnerGone: the owner's GUI disconnects and does not come back; the
// LAN comes down and, with nobody connected, the service is told to stop.
func TestOwnerGone(t *testing.T) {
	b := &fakeBackend{}
	idle := make(chan struct{}, 1)
	a := Identity{Session: 1, SID: "S-1-5-21-1"}
	s, name := newServer(t, b, 300*time.Millisecond, func() { idle <- struct{}{} }, a)
	c := dial(t, name)
	c.hello()
	c.send(upMsg("r"))
	c.expect(api.TypeStatus)
	c.conn.Close()
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("OnIdle never ran")
	}
	if b.downs.Load() == 0 {
		t.Fatal("LAN was not brought down")
	}
	if _, ok := s.Owner(); ok {
		t.Fatal("still owned")
	}
}

// TestOwnerReconnects: a GUI restart within the timeout keeps the LAN up.
func TestOwnerReconnects(t *testing.T) {
	b := &fakeBackend{}
	idle := make(chan struct{}, 1)
	a := Identity{Session: 1, SID: "S-1-5-21-1"}
	_, name := newServer(t, b, 500*time.Millisecond, func() { idle <- struct{}{} }, a, a)
	c := dial(t, name)
	c.hello()
	c.send(upMsg("r"))
	c.expect(api.TypeStatus)
	c.conn.Close()
	time.Sleep(100 * time.Millisecond)
	c2 := dial(t, name)
	c2.hello()
	select {
	case <-idle:
		t.Fatal("stopped although the owner reconnected")
	case <-time.After(time.Second):
	}
	if b.downs.Load() != 0 {
		t.Fatal("LAN brought down although the owner reconnected")
	}
	// Signals for the owner reach the new connection.
	b.srv.SendOwner(api.Message{Type: api.TypePeerState, PeerState: &api.PeerState{Peer: "p", State: api.PeerConnected}})
	c2.expect(api.TypePeerState)
}

// TestIdleWithoutClients: started by the GUI but never connected to.
func TestIdleWithoutClients(t *testing.T) {
	b := &fakeBackend{}
	idle := make(chan struct{}, 1)
	newServer(t, b, 200*time.Millisecond, func() { idle <- struct{}{} })
	select {
	case <-idle:
	case <-time.After(5 * time.Second):
		t.Fatal("OnIdle never ran")
	}
	if b.downs.Load() != 0 {
		t.Fatal("down called with no LAN up")
	}
}

func TestTooLongLine(t *testing.T) {
	b := &fakeBackend{}
	_, name := newServer(t, b, time.Minute, nil, Identity{Session: 1, SID: "S-1-5-21-1"})
	c := dial(t, name)
	c.hello()
	// The server may hang up before reading it all, so the write can fail.
	go c.conn.Write([]byte(`{"type":"status","x":"` + strings.Repeat("x", MaxLine+10) + "\"}\n"))
	c.expectError(api.ErrBadRequest)
	if _, ok := c.recv(); ok {
		t.Fatal("connection still open after an oversized line")
	}
}
