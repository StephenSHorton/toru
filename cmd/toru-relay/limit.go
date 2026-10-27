package main

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/pion/turn/v5"
)

// limit.go wraps pion's relay sockets: a per-user bandwidth cap, per-user and
// total allocation caps, the own-address port check, and a refusal of TCP
// (RFC 6062) allocations, which Toru never uses.

// bucket is a token bucket counting bytes. rate is bytes per second; burst is
// the most it can hold. A zero rate means unlimited.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

func newBucket(rate, burst float64, now func() time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now(), now: now}
}

// take spends n tokens if there are enough and reports whether it did.
func (b *bucket) take(n int) bool {
	if b.rate <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	t := b.now()
	b.tokens += t.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = t
	if float64(n) > b.tokens {
		return false
	}
	b.tokens -= float64(n)
	return true
}

// full reports whether the bucket has refilled completely (nothing owed), so
// an idle entry can be forgotten.
func (b *bucket) full() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.tokens+b.now().Sub(b.last).Seconds()*b.rate >= b.burst
}

var (
	errTCPRelay       = errors.New("tcp relay allocations are not supported")
	errUserQuota      = errors.New("too many allocations for this user")
	errServerQuota    = errors.New("relay is full")
	errPortOutOfRange = errors.New("requested port outside the relay range")
	errNotIPv4        = errors.New("only IPv4 relay allocations are supported")
)

// relayGen is the turn.RelayAddressGenerator every listener uses. It hands
// allocation to pion's port-range generator and wraps each relay socket.
type relayGen struct {
	inner *turn.RelayAddressGeneratorPortRange
	own   *ownAddrs

	userRate, userBurst float64 // bytes/s per user, both directions together
	maxPerUser, maxAll  int

	mu    sync.Mutex
	users map[string]*userState
	total int
	now   func() time.Time
}

type userState struct {
	allocs int
	bw     *bucket
}

func (g *relayGen) Validate() error { return g.inner.Validate() }

func (g *relayGen) AllocatePacketConn(conf turn.AllocateListenerConfig) (net.PacketConn, net.Addr, error) {
	if conf.Network != "udp4" {
		return nil, nil, errNotIPv4
	}
	if conf.RequestedPort != 0 && (conf.RequestedPort < int(g.inner.MinPort) || conf.RequestedPort > int(g.inner.MaxPort)) {
		return nil, nil, errPortOutOfRange
	}
	u, err := g.acquire(conf.UserID)
	if err != nil {
		return nil, nil, err
	}
	conn, addr, err := g.inner.AllocatePacketConn(conf)
	if err != nil {
		g.release(conf.UserID)
		return nil, nil, err
	}
	return &limitedConn{PacketConn: conn, gen: g, user: conf.UserID, bw: u.bw}, addr, nil
}

func (g *relayGen) AllocateListener(turn.AllocateListenerConfig) (net.Listener, net.Addr, error) {
	return nil, nil, errTCPRelay
}

func (g *relayGen) AllocateConn(turn.AllocateConnConfig) (net.Conn, error) {
	return nil, errTCPRelay
}

func (g *relayGen) acquire(user string) (*userState, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maxAll > 0 && g.total >= g.maxAll {
		return nil, errServerQuota
	}
	u := g.users[user]
	if u == nil {
		u = &userState{bw: newBucket(g.userRate, g.userBurst, g.now)}
		g.users[user] = u
	}
	if g.maxPerUser > 0 && u.allocs >= g.maxPerUser {
		return nil, errUserQuota
	}
	u.allocs++
	g.total++
	return u, nil
}

func (g *relayGen) release(user string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	u := g.users[user]
	if u == nil {
		return
	}
	u.allocs--
	g.total--
	// Keep the bucket while it still owes bytes, so closing and reopening an
	// allocation doesn't reset the cap. sweep forgets it once it has refilled.
	if u.allocs <= 0 && u.bw.full() {
		delete(g.users, user)
	}
}

// sweep drops idle users whose buckets have refilled.
func (g *relayGen) sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, u := range g.users {
		if u.allocs <= 0 && u.bw.full() {
			delete(g.users, k)
		}
	}
}

// limitedConn is one allocation's relay socket. Over-budget packets are
// dropped silently in both directions, the same as a congested link; the
// peers' own congestion control backs off.
type limitedConn struct {
	net.PacketConn
	gen  *relayGen
	user string
	bw   *bucket
	once sync.Once
}

func (c *limitedConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if !c.gen.own.allowSend(addr) || !c.bw.take(len(p)) {
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, addr)
}

func (c *limitedConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil || c.bw.take(n) {
			return n, addr, err
		}
	}
}

func (c *limitedConn) Close() error {
	c.once.Do(func() { c.gen.release(c.user) })
	return c.PacketConn.Close()
}
