package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
)

// These tests run the client against a fake service on net.Pipe: no
// sockets, no listeners.

func init() {
	backoffMin, backoffMax = 5*time.Millisecond, 20*time.Millisecond
	helloTimeout = time.Second
}

// fakeService is the far end of one pipe connection.
type fakeService struct {
	conn net.Conn
	r    *bufio.Reader
}

func (f *fakeService) read(t *testing.T) api.Message {
	t.Helper()
	_ = f.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := f.r.ReadBytes('\n')
	if err != nil {
		t.Fatalf("fake service read: %v", err)
	}
	var m api.Message
	if err := json.Unmarshal(line, &m); err != nil {
		t.Fatalf("fake service got %q: %v", line, err)
	}
	return m
}

func (f *fakeService) send(t *testing.T, m api.Message) {
	t.Helper()
	b, _ := json.Marshal(m)
	_ = f.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := f.conn.Write(append(b, '\n')); err != nil {
		t.Fatalf("fake service write: %v", err)
	}
}

// pipeDialer hands each dial's far end to the test.
func pipeDialer() (Dialer, chan *fakeService) {
	ch := make(chan *fakeService, 8)
	return func(ctx context.Context) (net.Conn, error) {
		a, b := net.Pipe()
		select {
		case ch <- &fakeService{conn: b, r: bufio.NewReader(b)}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return a, nil
	}, ch
}

func next(t *testing.T, ch chan *fakeService) *fakeService {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("client never dialled")
		return nil
	}
}

func TestHandshakeAndMessages(t *testing.T) {
	dial, svc := pipeDialer()
	connected := make(chan api.Hello, 1)
	signals := make(chan api.Signal, 1)
	statuses := make(chan api.Status, 1)
	peers := make(chan api.PeerState, 1)
	c := NewWithDialer(dial, Handlers{
		Connected: func(h api.Hello) { connected <- h },
		Signal:    func(s api.Signal) { signals <- s },
		Status:    func(s api.Status) { statuses <- s },
		PeerState: func(p api.PeerState) { peers <- p },
	})
	if err := c.Down(); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Send before connect = %v, want ErrNotConnected", err)
	}
	c.Start()
	defer c.Close()

	f := next(t, svc)
	hi := f.read(t)
	if hi.Type != api.TypeHello || hi.Hello == nil || hi.Hello.Major != api.ProtocolMajor || hi.Hello.Minor != api.ProtocolMinor {
		t.Fatalf("first message %+v, want our Hello", hi)
	}
	f.send(t, api.Message{Type: api.TypeHello, ID: hi.ID, Hello: &api.Hello{Major: api.ProtocolMajor, Minor: 3, Version: "0.13.0", PubKey: "pk", IP: "100.96.1.2"}})
	select {
	case h := <-connected:
		if h.IP != "100.96.1.2" || h.Version != "0.13.0" {
			t.Fatalf("hello %+v", h)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Connected")
	}
	if h, ok := c.Hello(); !ok || h.PubKey != "pk" {
		t.Fatalf("Hello() = %+v, %v", h, ok)
	}

	// GUI → service.
	go func() { _ = c.Up(api.Up{Room: "r", Self: "me", Policy: api.Policy{MDNS: true}}) }()
	up := f.read(t)
	if up.Type != api.TypeUp || up.Up == nil || up.Up.Room != "r" || up.Up.Self != "me" || !up.Up.Policy.MDNS {
		t.Fatalf("Up arrived as %+v", up)
	}
	go func() { _ = c.Signal("peer1", json.RawMessage(`{"kind":"hello"}`)) }()
	sig := f.read(t)
	if sig.Type != api.TypeSignal || sig.Signal.Peer != "peer1" || string(sig.Signal.Blob) != `{"kind":"hello"}` {
		t.Fatalf("Signal arrived as %+v", sig)
	}

	// Service → GUI, including lines the client must skip.
	_, _ = f.conn.Write([]byte("not json\n"))
	f.send(t, api.Message{Type: "fromTheFuture"})
	f.send(t, api.Message{Type: api.TypeSignal, Signal: &api.Signal{Peer: "peer2", Blob: json.RawMessage(`{"kind":"cand"}`)}})
	f.send(t, api.Message{Type: api.TypePeerState, PeerState: &api.PeerState{Peer: "peer2", State: api.PeerConnected, Path: api.PathRelay, RTTMs: 31}})
	f.send(t, api.Message{Type: api.TypeStatus, Status: &api.Status{State: api.StateOn, IP: "100.96.1.2"}})
	if s := <-signals; s.Peer != "peer2" || string(s.Blob) != `{"kind":"cand"}` {
		t.Fatalf("signal %+v", s)
	}
	if p := <-peers; p.Path != api.PathRelay || p.RTTMs != 31 {
		t.Fatalf("peer state %+v", p)
	}
	if s := <-statuses; s.State != api.StateOn {
		t.Fatalf("status %+v", s)
	}
}

func TestReconnects(t *testing.T) {
	dial, svc := pipeDialer()
	connected := make(chan struct{}, 4)
	disconnected := make(chan error, 4)
	c := NewWithDialer(dial, Handlers{
		Connected:    func(api.Hello) { connected <- struct{}{} },
		Disconnected: func(err error) { disconnected <- err },
	})
	c.Start()
	defer c.Close()

	for round := 0; round < 2; round++ {
		f := next(t, svc)
		hi := f.read(t)
		f.send(t, api.Message{Type: api.TypeHello, ID: hi.ID, Hello: &api.Hello{Major: api.ProtocolMajor}})
		select {
		case <-connected:
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: no Connected", round)
		}
		if !c.Connected() {
			t.Fatalf("round %d: Connected() false", round)
		}
		_ = f.conn.Close() // the service stops
		select {
		case <-disconnected:
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: no Disconnected", round)
		}
	}
}

func TestIncompatibleService(t *testing.T) {
	dial, svc := pipeDialer()
	disconnected := make(chan error, 4)
	c := NewWithDialer(dial, Handlers{
		Connected:    func(api.Hello) { t.Error("Connected to an incompatible service") },
		Disconnected: func(err error) { disconnected <- err },
	})
	c.Start()
	defer c.Close()
	f := next(t, svc)
	hi := f.read(t)
	f.send(t, api.Message{Type: api.TypeHello, ID: hi.ID, Hello: &api.Hello{Major: api.ProtocolMajor + 1, Version: "9.0.0"}})
	select {
	case err := <-disconnected:
		var inc *IncompatibleError
		if !errors.As(err, &inc) || inc.Hello.Version != "9.0.0" {
			t.Fatalf("Disconnected(%v), want IncompatibleError", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Disconnected")
	}
	if c.Connected() {
		t.Fatal("Connected() true after an incompatible Hello")
	}
	next(t, svc) // and it keeps trying (the service may get updated)
}

func TestCloseWhileDialling(t *testing.T) {
	c := NewWithDialer(func(ctx context.Context) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}, Handlers{})
	c.Start()
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		c.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close hung")
	}
	NewWithDialer(nil, Handlers{}).Close() // never started: returns at once
}
