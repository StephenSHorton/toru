package main

import (
	"errors"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pion/turn/v5"
)

// limit.go wraps pion's relay sockets: bandwidth caps per user and per
// client IP, allocation caps per user, per client IP and in total, the
// own-address port check, and a refusal of TCP (RFC 6062) allocations, which
// Toru never uses.
//
// The user is the peer id in the credential's username, which the caller
// picks (the credential endpoint is open), so per-user caps alone let one
// host mint ids until the relay is full. authHandler appends the client's
// source IP to the user id pion passes here, and every allocation counts
// against both.

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
	errIPQuota        = errors.New("too many allocations from this address")
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
	ipRate, ipBurst     float64 // bytes/s per client IP
	maxPerUser, maxAll  int
	maxPerIP            int

	mu    sync.Mutex
	users map[string]*userState
	ips   map[string]*userState
	total int
	now   func() time.Time
}

// userSep joins the peer id and the client IP in the user id authHandler
// returns. A peer id may contain anything; the IP never contains this.
const userSep = "|"

// authHandler is pion's TURN REST check with the client's source IP added
// to the user id, so allocations and bandwidth are also counted per IP.
func authHandler(secret string) turn.AuthHandler {
	inner := turn.LongTermTURNRESTAuthHandler(secret, nil)
	return func(ra *turn.RequestAttributes) (string, []byte, bool) {
		user, key, ok := inner(ra)
		if !ok {
			return user, key, ok
		}
		return user + userSep + srcIP(ra.SrcAddr), key, ok
	}
}

func srcIP(a net.Addr) string {
	switch a := a.(type) {
	case *net.UDPAddr:
		return a.IP.String()
	case *net.TCPAddr:
		return a.IP.String()
	}
	if a == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}

// splitUser undoes authHandler: the peer id and the client IP ("" if the id
// carries none).
func splitUser(id string) (user, ip string) {
	if i := strings.LastIndex(id, userSep); i >= 0 {
		return id[:i], id[i+len(userSep):]
	}
	return id, ""
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
	u, ipst, err := g.acquire(conf.UserID)
	if err != nil {
		return nil, nil, err
	}
	conn, addr, err := g.inner.AllocatePacketConn(conf)
	if err != nil {
		g.release(conf.UserID)
		return nil, nil, err
	}
	lc := &limitedConn{PacketConn: conn, gen: g, user: conf.UserID, bw: u.bw}
	if ipst != nil {
		lc.ipBW = ipst.bw
	}
	return lc, addr, nil
}

func (g *relayGen) AllocateListener(turn.AllocateListenerConfig) (net.Listener, net.Addr, error) {
	return nil, nil, errTCPRelay
}

func (g *relayGen) AllocateConn(turn.AllocateConnConfig) (net.Conn, error) {
	return nil, errTCPRelay
}

// acquire counts one allocation for id (authHandler's peer|ip). The IP
// state is nil when the id carries no IP (tests, or a handler without it).
func (g *relayGen) acquire(id string) (*userState, *userState, error) {
	user, ip := splitUser(id)
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.maxAll > 0 && g.total >= g.maxAll {
		return nil, nil, errServerQuota
	}
	var is *userState
	if ip != "" {
		if g.ips == nil {
			g.ips = map[string]*userState{}
		}
		if is = g.ips[ip]; is == nil {
			is = &userState{bw: newBucket(g.ipRate, g.ipBurst, g.now)}
			g.ips[ip] = is
		}
		if g.maxPerIP > 0 && is.allocs >= g.maxPerIP {
			return nil, nil, errIPQuota
		}
	}
	u := g.users[user]
	if u == nil {
		u = &userState{bw: newBucket(g.userRate, g.userBurst, g.now)}
		g.users[user] = u
	}
	if g.maxPerUser > 0 && u.allocs >= g.maxPerUser {
		return nil, nil, errUserQuota
	}
	u.allocs++
	if is != nil {
		is.allocs++
	}
	g.total++
	return u, is, nil
}

func (g *relayGen) release(id string) {
	user, ip := splitUser(id)
	g.mu.Lock()
	defer g.mu.Unlock()
	u := g.users[user]
	if u == nil {
		return
	}
	u.allocs--
	g.total--
	// Keep a bucket while it still owes bytes, so closing and reopening an
	// allocation doesn't reset the cap. sweep forgets it once it has refilled.
	if u.allocs <= 0 && u.bw.full() {
		delete(g.users, user)
	}
	if is := g.ips[ip]; is != nil {
		is.allocs--
		if is.allocs <= 0 && is.bw.full() {
			delete(g.ips, ip)
		}
	}
}

// sweep drops idle users and IPs whose buckets have refilled.
func (g *relayGen) sweep() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range []map[string]*userState{g.users, g.ips} {
		for k, u := range m {
			if u.allocs <= 0 && u.bw.full() {
				delete(m, k)
			}
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
	ipBW *bucket // the client IP's budget, shared by its users (nil = none)
	once sync.Once
}

// take spends n bytes from the IP's budget and then the user's.
func (c *limitedConn) take(n int) bool {
	if c.ipBW != nil && !c.ipBW.take(n) {
		return false
	}
	return c.bw.take(n)
}

func (c *limitedConn) WriteTo(p []byte, addr net.Addr) (int, error) {
	if !c.gen.own.allowSend(addr) || !c.take(len(p)) {
		return len(p), nil
	}
	return c.PacketConn.WriteTo(p, addr)
}

func (c *limitedConn) ReadFrom(p []byte) (int, net.Addr, error) {
	for {
		n, addr, err := c.PacketConn.ReadFrom(p)
		if err != nil || c.take(n) {
			return n, addr, err
		}
	}
}

func (c *limitedConn) Close() error {
	c.once.Do(func() { c.gen.release(c.user) })
	return c.PacketConn.Close()
}
