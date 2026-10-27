package lan

import (
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.zx2c4.com/wireguard/conn"
)

// chanConn is a packetConn over two channels; close ends Read.
type chanConn struct {
	rx, tx chan []byte
	closed chan struct{}
}

func chanPair() (*chanConn, *chanConn) {
	a2b, b2a := make(chan []byte, 16), make(chan []byte, 16)
	return &chanConn{rx: b2a, tx: a2b, closed: make(chan struct{})}, &chanConn{rx: a2b, tx: b2a, closed: make(chan struct{})}
}

func (c *chanConn) Read(p []byte) (int, error) {
	select {
	case b := <-c.rx:
		return copy(p, b), nil
	case <-c.closed:
		return 0, io.EOF
	}
}

func (c *chanConn) Write(p []byte) (int, error) {
	c.tx <- append([]byte(nil), p...)
	return len(p), nil
}

func (c *chanConn) close() { close(c.closed) }

func recvOne(t *testing.T, fn conn.ReceiveFunc) ([]byte, conn.Endpoint) {
	t.Helper()
	type res struct {
		b  []byte
		ep conn.Endpoint
	}
	ch := make(chan res, 1)
	go func() {
		bufs := [][]byte{make([]byte, 2048)}
		sizes := []int{0}
		eps := []conn.Endpoint{nil}
		if n, err := fn(bufs, sizes, eps); err == nil && n == 1 {
			ch <- res{bufs[0][:sizes[0]], eps[0]}
		}
	}()
	select {
	case r := <-ch:
		return r.b, r.ep
	case <-time.After(2 * time.Second):
		t.Fatal("nothing received")
		return nil, nil
	}
}

func TestBindSendReceiveSwap(t *testing.T) {
	b := newICEBind()
	defer b.shutdown()
	fns, _, err := b.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Open(0); !errors.Is(err, conn.ErrBindAlreadyOpen) {
		t.Fatalf("second Open: %v", err)
	}
	e := b.newEndpoint()
	ep, err := b.ParseEndpoint(e.DstToString())
	if err != nil || ep != e {
		t.Fatalf("ParseEndpoint(%q) = %v, %v", e.DstToString(), ep, err)
	}
	if !netip.MustParsePrefix("127.77.0.0/16").Contains(e.DstIP()) {
		t.Fatalf("endpoint %v outside 127.77/16", e.DstIP())
	}

	if err := b.Send([][]byte{[]byte("x")}, e); !errors.Is(err, errNoPath) {
		t.Fatalf("send with no path: %v", err)
	}

	relay, relayFar := chanPair()
	b.attach(e, relay)
	b.setActive(e, relay)
	if err := b.Send([][]byte{[]byte("via relay")}, e); err != nil {
		t.Fatal(err)
	}
	if got := <-relayFar.rx; string(got) != "via relay" {
		t.Fatalf("relay got %q", got)
	}
	relayFar.Write([]byte("from relay"))
	if got, from := recvOne(t, fns[0]); string(got) != "from relay" || from != e {
		t.Fatalf("got %q from %v", got, from)
	}

	// Make-before-break: attach direct, swap, then close the relay.
	direct, directFar := chanPair()
	b.attach(e, direct)
	b.setActive(e, direct)
	relay.close()
	if err := b.Send([][]byte{[]byte("via direct")}, e); err != nil {
		t.Fatal(err)
	}
	if got := <-directFar.rx; string(got) != "via direct" {
		t.Fatalf("direct got %q", got)
	}
	directFar.Write([]byte("from direct"))
	if got, from := recvOne(t, fns[0]); string(got) != "from direct" || from != e {
		t.Fatalf("got %q from %v", got, from)
	}

	// Close unblocks the receive func; Open works again (device Down/Up).
	done := make(chan error, 1)
	go func() {
		_, err := fns[0]([][]byte{make([]byte, 10)}, []int{0}, []conn.Endpoint{nil})
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	b.Close()
	if err := <-done; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("receive after Close: %v", err)
	}
	fns, _, err = b.Open(0)
	if err != nil {
		t.Fatal(err)
	}
	directFar.Write([]byte("after reopen"))
	if got, _ := recvOne(t, fns[0]); string(got) != "after reopen" {
		t.Fatalf("got %q", got)
	}

	b.removeEndpoint(e)
	if _, err := b.ParseEndpoint(e.DstToString()); err == nil {
		t.Fatal("removed endpoint still parses")
	}
}

func TestBindEndpointsDistinct(t *testing.T) {
	b := newICEBind()
	seen := map[string]bool{}
	for range 300 {
		s := b.newEndpoint().DstToString()
		if seen[s] {
			t.Fatalf("endpoint %s handed out twice", s)
		}
		seen[s] = true
	}
}

func TestAddrFromKey(t *testing.T) {
	var pub [32]byte
	rand.Read(pub[:])
	a := AddrFromKey(pub, 0)
	if a != AddrFromKey(pub, 0) {
		t.Fatal("not deterministic")
	}
	if a == AddrFromKey(pub, 1) && AddrFromKey(pub, 1) == AddrFromKey(pub, 2) {
		t.Fatal("salt doesn't change the address")
	}
	for i := range 200000 {
		rand.Read(pub[:])
		a := AddrFromKey(pub, uint32(i))
		if !overlay.Contains(a) || !validHost(a) {
			t.Fatalf("AddrFromKey gave %v", a)
		}
	}
	for _, s := range []string{"100.96.0.0", "100.96.0.1", "100.96.255.255", "100.97.0.5", "10.0.0.1"} {
		a := netip.MustParseAddr(s)
		if overlay.Contains(a) && validHost(a) {
			t.Fatalf("%v counted as a valid host", a)
		}
	}
}

func TestPublicKeyMatchesWireGuard(t *testing.T) {
	// RFC 7748 §6.1 test vector (Alice).
	var priv [32]byte
	copy(priv[:], mustHex("77076d0a7318a57d3c16c17251b26645df4c2f87ebc0992ab177fba51db92c2a"))
	want := mustHex("8520f0098930a754748b7ddcb43ef75a0dbf3a0d26381af4eba4a98eaa9b4e6a")
	if got := PublicKey(priv); string(got[:]) != string(want) {
		t.Fatalf("PublicKey = %x", got)
	}
}
