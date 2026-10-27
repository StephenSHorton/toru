// Package client is Toru's side of the LAN service pipe (docs/lan.md): it
// dials \\.\pipe\ProtectedPrefix\Administrators\Toru\lan, swaps Hello with a
// protocol check, keeps the connection up (redialling while the service
// starts or restarts) and turns incoming JSON lines into typed callbacks.
// The contract is package api.
package client

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
)

// Handlers are the client's callbacks. All run on the client's own reader
// goroutine, one at a time; any may be nil. A handler must not block on a
// call that waits for the next incoming message.
type Handlers struct {
	// Connected: the pipe is up and the service's Hello is compatible.
	Connected func(api.Hello)
	// Disconnected: the connection ended (service stopped, crashed or was
	// incompatible: err is then an *IncompatibleError). The client redials.
	Disconnected func(err error)
	Signal       func(api.Signal)
	PeerState    func(api.PeerState)
	Status       func(api.Status)
	Error        func(api.Error)
}

// IncompatibleError is a service speaking another major protocol version
// (the GUI shows "Update LAN").
type IncompatibleError struct {
	Hello api.Hello
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("LAN service speaks protocol %d.%d, Toru speaks %d.%d",
		e.Hello.Major, e.Hello.Minor, api.ProtocolMajor, api.ProtocolMinor)
}

// ErrNotConnected is Send while the pipe is down.
var ErrNotConnected = errors.New("the LAN service isn't connected")

// Timing. Variables so tests can run fast.
var (
	dialTimeout  = 2 * time.Second
	helloTimeout = 5 * time.Second
	writeTimeout = 5 * time.Second
	backoffMin   = 250 * time.Millisecond
	backoffMax   = 5 * time.Second
)

// maxLine bounds one incoming JSON line (a Status with many peers).
const maxLine = 1 << 20

// Dialer opens one connection to the service.
type Dialer func(ctx context.Context) (net.Conn, error)

// Client is one pipe connection to the LAN service, redialled until Close.
type Client struct {
	dial Dialer
	h    Handlers

	mu     sync.Mutex
	conn   net.Conn
	hello  *api.Hello
	closed bool
	quit   chan struct{}
	done   chan struct{}
	start  sync.Once

	wmu sync.Mutex // one writer at a time
}

// New returns a client for the real service pipe. Nothing happens until Start.
func New(h Handlers) *Client {
	return NewWithDialer(dialPipe, h)
}

// NewWithDialer returns a client that connects with dial (tests use net.Pipe).
func NewWithDialer(dial Dialer, h Handlers) *Client {
	return &Client{dial: dial, h: h, quit: make(chan struct{}), done: make(chan struct{})}
}

// Start begins dialling in the background. Calling it again does nothing.
func (c *Client) Start() {
	c.start.Do(func() { go c.run() })
}

// Close drops the connection and stops redialling. It waits for the reader
// goroutine, so it must not be called from a handler.
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.quit)
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	started := true
	c.start.Do(func() { started = false })
	if started {
		<-c.done
	}
}

// Connected reports whether the pipe is up (Hello done).
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

// Hello is the service's Hello on the current connection (ok = connected).
func (c *Client) Hello() (api.Hello, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil || c.hello == nil {
		return api.Hello{}, false
	}
	return *c.hello, true
}

// Send writes one message.
func (c *Client) Send(m api.Message) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrNotConnected
	}
	return c.write(conn, m)
}

// Up brings the LAN up for one room.
func (c *Client) Up(up api.Up) error {
	return c.Send(api.Message{Type: api.TypeUp, Up: &up})
}

// Down takes the LAN down.
func (c *Client) Down() error { return c.Send(api.Message{Type: api.TypeDown}) }

// SetICE hands the service a fresh ICE list.
func (c *Client) SetICE(servers []api.ICEServer) error {
	return c.Send(api.Message{Type: api.TypeSetICE, SetICE: &api.SetICE{ICEServers: servers}})
}

// Signal hands the service one room signaling blob from peer.
func (c *Client) Signal(peer string, blob json.RawMessage) error {
	return c.Send(api.Message{Type: api.TypeSignal, Signal: &api.Signal{Peer: peer, Blob: blob}})
}

// Drop removes a peer that left the room.
func (c *Client) Drop(peer string) error {
	return c.Send(api.Message{Type: api.TypeDrop, Drop: &api.Drop{Peer: peer}})
}

// Readdress asks the service to move off a contested address.
func (c *Client) Readdress(salt uint32) error {
	return c.Send(api.Message{Type: api.TypeReaddress, Readdress: &api.Readdress{Salt: salt}})
}

// SetPolicy changes the room's LAN policy.
func (c *Client) SetPolicy(p api.Policy) error {
	return c.Send(api.Message{Type: api.TypeSetPolicy, SetPolicy: &p})
}

// RequestStatus asks for a Status; it arrives on Handlers.Status.
func (c *Client) RequestStatus() error { return c.Send(api.Message{Type: api.TypeStatus}) }

func (c *Client) write(conn net.Conn, m api.Message) error {
	line, err := json.Marshal(m)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = conn.Write(line)
	_ = conn.SetWriteDeadline(time.Time{})
	return err
}

func (c *Client) stopping() bool {
	select {
	case <-c.quit:
		return true
	default:
		return false
	}
}

// run dials, handshakes and reads until Close, backing off between tries.
func (c *Client) run() {
	defer close(c.done)
	backoff := backoffMin
	for !c.stopping() {
		err := c.session()
		if c.stopping() {
			return
		}
		if err == nil {
			backoff = backoffMin // a session that got going redials fast
		}
		select {
		case <-c.quit:
			return
		case <-time.After(backoff):
		}
		if err != nil {
			backoff = min(backoff*2, backoffMax)
		}
	}
}

// session is one connection: dial, Hello both ways, then read until it
// drops. It returns nil if the session got past Hello.
func (c *Client) session() error {
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	go func() {
		select {
		case <-c.quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	conn, err := c.dial(ctx)
	cancel()
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = conn.Close()
		return nil
	}
	c.mu.Unlock()
	// Close the conn on Close even while the handshake is reading.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-c.quit:
			_ = conn.Close()
		case <-stop:
		}
	}()

	r := bufio.NewReaderSize(conn, 64<<10)
	hello, err := c.handshake(conn, r)
	if err != nil {
		_ = conn.Close()
		var inc *IncompatibleError
		if errors.As(err, &inc) && c.h.Disconnected != nil {
			c.h.Disconnected(err)
		}
		return err
	}
	c.mu.Lock()
	c.conn, c.hello = conn, &hello
	c.mu.Unlock()
	if c.h.Connected != nil {
		c.h.Connected(hello)
	}
	err = c.read(r)
	c.mu.Lock()
	c.conn, c.hello = nil, nil
	c.mu.Unlock()
	_ = conn.Close()
	if c.h.Disconnected != nil && !c.stopping() {
		c.h.Disconnected(err)
	}
	return nil
}

// handshake sends our Hello and reads the service's, which must come first.
func (c *Client) handshake(conn net.Conn, r *bufio.Reader) (api.Hello, error) {
	err := c.write(conn, api.Message{Type: api.TypeHello, ID: 1, Hello: &api.Hello{Major: api.ProtocolMajor, Minor: api.ProtocolMinor}})
	if err != nil {
		return api.Hello{}, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	m, err := readMessage(r)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil {
		return api.Hello{}, err
	}
	if m.Type == api.TypeError && m.Error != nil {
		return api.Hello{}, fmt.Errorf("LAN service: %s %s", m.Error.Code, m.Error.Detail)
	}
	if m.Type != api.TypeHello || m.Hello == nil {
		return api.Hello{}, fmt.Errorf("LAN service: expected hello, got %q", m.Type)
	}
	if m.Hello.Major != api.ProtocolMajor {
		return *m.Hello, &IncompatibleError{Hello: *m.Hello}
	}
	return *m.Hello, nil
}

// read dispatches messages until the connection fails.
func (c *Client) read(r *bufio.Reader) error {
	for {
		m, err := readMessage(r)
		if err != nil {
			var bad *badLineError
			if errors.As(err, &bad) {
				continue // a line we can't parse: skip it, keep the session
			}
			return err
		}
		c.dispatch(m)
	}
}

func (c *Client) dispatch(m api.Message) {
	switch m.Type {
	case api.TypeSignal:
		if m.Signal != nil && c.h.Signal != nil {
			c.h.Signal(*m.Signal)
		}
	case api.TypePeerState:
		if m.PeerState != nil && c.h.PeerState != nil {
			c.h.PeerState(*m.PeerState)
		}
	case api.TypeStatus:
		if m.Status != nil && c.h.Status != nil {
			c.h.Status(*m.Status)
		}
	case api.TypeError:
		if m.Error != nil && c.h.Error != nil {
			c.h.Error(*m.Error)
		}
	case api.TypeHello:
		// A repeat Hello (address changed after Readdress): keep the latest.
		if m.Hello != nil && m.Hello.Major == api.ProtocolMajor {
			h := *m.Hello
			c.mu.Lock()
			c.hello = &h
			c.mu.Unlock()
			if c.h.Connected != nil {
				c.h.Connected(h)
			}
		}
	}
	// Unknown types are from a newer minor version: ignored.
}

type badLineError struct{ err error }

func (e *badLineError) Error() string { return "bad line from LAN service: " + e.err.Error() }

// readMessage reads one JSON line.
func readMessage(r *bufio.Reader) (api.Message, error) {
	var m api.Message
	var line []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return m, err
		}
		line = append(line, chunk...)
		if len(line) > maxLine {
			return m, errors.New("line from LAN service too long")
		}
		if !isPrefix {
			break
		}
	}
	if len(line) == 0 {
		return m, &badLineError{errors.New("empty line")}
	}
	if err := json.Unmarshal(line, &m); err != nil {
		return m, &badLineError{err}
	}
	return m, nil
}
