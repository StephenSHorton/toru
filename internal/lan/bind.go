package lan

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"golang.zx2c4.com/wireguard/conn"
)

// bind.go is iceBind, the conn.Bind wireguard-go sends and receives through.
// There are no sockets: each peer has a fake endpoint (127.77.x.y:1, never on
// the wire) that stands for whatever ICE connection currently carries that
// peer. Send writes to the connection behind the endpoint; one goroutine per
// connection reads it and feeds a shared channel that the single ReceiveFunc
// drains. Swapping the connection behind an endpoint (relay → direct) is one
// atomic store, so WireGuard just sees the same endpoint keep working. The
// shape follows NetBird's client/iface/bind/ice_bind.go, written fresh.

// packetConn is the slice of *ice.Conn the bind uses; tests use pipes.
type packetConn interface {
	Read(p []byte) (int, error)
	Write(p []byte) (int, error)
}

var errNoPath = errors.New("lan: no ICE path to peer yet")

// iceEndpoint is a peer's fake endpoint. The address is stable for the peer's
// lifetime, which is what WireGuard's cookie (DstToBytes) needs.
type iceEndpoint struct {
	addr netip.AddrPort
	conn atomic.Pointer[packetConnBox]
}

// packetConnBox lets an interface sit behind atomic.Pointer.
type packetConnBox struct{ c packetConn }

func (e *iceEndpoint) ClearSrc()           {}
func (e *iceEndpoint) SrcToString() string { return "" }
func (e *iceEndpoint) DstToString() string { return e.addr.String() }
func (e *iceEndpoint) DstToBytes() []byte  { b, _ := e.addr.MarshalBinary(); return b }
func (e *iceEndpoint) DstIP() netip.Addr   { return e.addr.Addr() }
func (e *iceEndpoint) SrcIP() netip.Addr   { return netip.Addr{} }

type rxPacket struct {
	b  []byte
	ep *iceEndpoint
}

type iceBind struct {
	rx chan rxPacket

	mu     sync.Mutex
	eps    map[netip.AddrPort]*iceEndpoint
	next   uint16
	closed chan struct{} // nil while closed; closed to unblock ReceiveFunc
	done   chan struct{} // closed by shutdown; stops the readers
}

var _ conn.Bind = (*iceBind)(nil)

func newICEBind() *iceBind {
	return &iceBind{rx: make(chan rxPacket, 256), eps: map[netip.AddrPort]*iceEndpoint{}, done: make(chan struct{})}
}

// Open starts receiving. The port is meaningless (there is no socket), so it
// reports whatever it was given. Peers and their connections survive
// Close/Open, which wireguard-go does on every Up.
func (b *iceBind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	closed := make(chan struct{})
	b.closed = closed
	recv := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		select {
		case p := <-b.rx:
			sizes[0] = copy(packets[0], p.b)
			eps[0] = p.ep
			return 1, nil
		case <-closed:
			return 0, net.ErrClosed
		}
	}
	return []conn.ReceiveFunc{recv}, port, nil
}

func (b *iceBind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed != nil {
		close(b.closed)
		b.closed = nil
	}
	return nil
}

// shutdown stops every reader for good (Node.Close).
func (b *iceBind) shutdown() {
	b.mu.Lock()
	defer b.mu.Unlock()
	select {
	case <-b.done:
	default:
		close(b.done)
	}
}

func (b *iceBind) SetMark(uint32) error { return nil }
func (b *iceBind) BatchSize() int       { return 1 }

func (b *iceBind) Send(bufs [][]byte, ep conn.Endpoint) error {
	e, ok := ep.(*iceEndpoint)
	if !ok {
		return conn.ErrWrongEndpointType
	}
	box := e.conn.Load()
	if box == nil {
		return errNoPath
	}
	for _, buf := range bufs {
		if _, err := box.c.Write(buf); err != nil {
			return err
		}
	}
	return nil
}

// ParseEndpoint resolves the endpoint string node.go put in IpcSet back to the
// peer's endpoint.
func (b *iceBind) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.eps[ap]; e != nil {
		return e, nil
	}
	return nil, errors.New("lan: unknown endpoint " + s)
}

// newEndpoint allocates the next free 127.77.x.y:1.
func (b *iceBind) newEndpoint() *iceEndpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		b.next++
		if b.next == 0 {
			continue
		}
		ap := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 77, byte(b.next >> 8), byte(b.next)}), 1)
		if b.eps[ap] == nil {
			e := &iceEndpoint{addr: ap}
			b.eps[ap] = e
			return e
		}
	}
}

func (b *iceBind) removeEndpoint(e *iceEndpoint) {
	b.mu.Lock()
	delete(b.eps, e.addr)
	b.mu.Unlock()
	e.conn.Store(nil)
}

// setActive points ep's sends at c (nil: no path). One atomic store, so a
// relay → direct switch never drops a send in between.
func (b *iceBind) setActive(e *iceEndpoint, c packetConn) {
	if c == nil {
		e.conn.Store(nil)
		return
	}
	e.conn.Store(&packetConnBox{c})
}

// attach starts reading c on behalf of ep. Packets from it count as ep's
// whether or not c is the active one, so the make-before-break overlap
// delivers from both. The reader stops when c's Read fails (its agent closed)
// or on shutdown.
func (b *iceBind) attach(e *iceEndpoint, c packetConn) {
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := c.Read(buf)
			if errors.Is(err, io.ErrShortBuffer) {
				continue
			}
			if err != nil {
				return
			}
			select {
			case b.rx <- rxPacket{b: append([]byte(nil), buf[:n]...), ep: e}:
			case <-b.done:
				return
			}
		}
	}()
}
