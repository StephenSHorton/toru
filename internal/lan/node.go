// Package lan is the LAN data plane: wireguard-go over pion ICE, with the
// broadcast shim in front of the adapter. The service gives NewNode a tun
// device (Wintun; netstack in tests) and carries Signal blobs to and from the
// GUI; everything below that is here. Design: docs/lan.md.
package lan

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// node.go ties the pieces together: the shim around the adapter, the
// wireguard-go device on the ICE bind, and one peer (agent.go) per room member
// that said Hello.

// Config is what the service knows when it brings LAN up.
type Config struct {
	PrivateKey [32]byte   // WireGuard static private key
	Addr       netip.Addr // this node's overlay address
	ICEServers []api.ICEServer
	Policy     api.Policy
	// Signal sends a blob to a room peer (via the GUI). It is called from
	// several goroutines and should not block for long.
	Signal func(peer string, blob api.SignalBlob)
	Logf   func(format string, args ...any)
}

// Node is one LAN member's data plane.
type Node struct {
	cfg  Config
	pub  [32]byte
	boot uint32 // sent in Hello.Gen; a new value tells peers we restarted
	shim *shim
	bind *iceBind
	dev  *device.Device

	helloMu sync.Mutex // one Hello at a time: adding a peer is several steps

	mu       sync.Mutex
	ice      []api.ICEServer
	peers    map[string]*peer
	early    map[string][]api.SignalBlob // creds/cands from peers we have no Hello from yet
	gen      uint32
	closed   bool
	onChange func()

	changed chan struct{}
	done    chan struct{}
}

// peer is one room member on the LAN. The fields under "actor" belong to its
// goroutine (agent.go); state, path and agent are published under Node.mu.
type peer struct {
	n           *Node
	id          string
	pub         [32]byte
	ip          netip.Addr
	ep          *iceEndpoint
	controlling bool

	events  chan any
	done    chan struct{}
	stopped chan struct{}

	// actor
	boot          uint32
	main, up      *session
	lastRemoteGen uint32
	early         []api.SignalBlob
	backoff       time.Duration
	upBackoff     time.Duration
	restartT      *time.Timer
	upgradeT      *time.Timer

	// published
	state, path string
	agentRef    *session
}

// NewNode starts the WireGuard device on dev behind the shim. The Node owns
// dev from here on: Close closes it.
func NewNode(dev tun.Device, cfg Config) (*Node, error) {
	if !overlay.Contains(cfg.Addr) || !validHost(cfg.Addr) {
		return nil, fmt.Errorf("lan: %v is not an overlay host address", cfg.Addr)
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	var boot [4]byte
	_, _ = rand.Read(boot[:])
	n := &Node{
		cfg:     cfg,
		pub:     PublicKey(cfg.PrivateKey),
		boot:    binary.BigEndian.Uint32(boot[:]) | 1,
		ice:     slices.Clone(cfg.ICEServers),
		peers:   map[string]*peer{},
		early:   map[string][]api.SignalBlob{},
		changed: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	n.shim = newShim(dev, cfg.Addr, cfg.Policy)
	n.bind = newICEBind()
	logger := &device.Logger{
		Verbosef: device.DiscardLogf,
		Errorf:   func(f string, a ...any) { cfg.Logf("lan: wireguard: "+f, a...) },
	}
	if os.Getenv("TORU_LAN_DEBUG") != "" {
		logger.Verbosef = func(f string, a ...any) { cfg.Logf("lan: wireguard: "+f, a...) }
	}
	n.dev = device.NewDevice(n.shim, n.bind, logger)
	if err := n.dev.IpcSet("private_key=" + hex.EncodeToString(cfg.PrivateKey[:]) + "\n"); err != nil {
		n.dev.Close()
		return nil, fmt.Errorf("lan: set key: %w", err)
	}
	if err := n.dev.Up(); err != nil {
		n.dev.Close()
		return nil, fmt.Errorf("lan: device up: %w", err)
	}
	go n.notifyLoop()
	return n, nil
}

func (n *Node) logf(format string, args ...any) { n.cfg.Logf(format, args...) }

func (n *Node) signal(peer string, b api.SignalBlob) {
	if n.cfg.Signal != nil {
		n.cfg.Signal(peer, b)
	}
}

// Hello is the KindHello blob to announce this node to a new peer. Gen
// carries the node's boot value, so a peer can tell a restart from a repeat.
func (n *Node) Hello() api.SignalBlob {
	return api.SignalBlob{
		Kind:  api.KindHello,
		Gen:   n.boot,
		WGPub: base64.StdEncoding.EncodeToString(n.pub[:]),
		IP:    n.cfg.Addr.String(),
	}
}

// HandleSignal takes one blob a room peer sent us.
func (n *Node) HandleSignal(id string, b api.SignalBlob) {
	switch b.Kind {
	case api.KindHello:
		n.hello(id, b)
	case api.KindBye:
		n.Drop(id)
	case api.KindCreds, api.KindCand:
		n.mu.Lock()
		if n.closed {
			n.mu.Unlock()
			return
		}
		p := n.peers[id]
		if p == nil {
			// Signaling can overtake the Hello; keep a little for when it comes.
			q := n.early[id]
			if len(q) >= earlyMax {
				q = q[1:]
			}
			n.early[id] = append(q, b)
		}
		n.mu.Unlock()
		if p != nil {
			p.post(evSignal{b})
		}
	default:
		n.logf("lan: %s: unknown signal %q", id, b.Kind)
	}
}

func (n *Node) hello(id string, b api.SignalBlob) {
	key, err := base64.StdEncoding.DecodeString(b.WGPub)
	if err != nil || len(key) != 32 {
		n.logf("lan: %s: bad hello key", id)
		return
	}
	pub := [32]byte(key)
	ip, err := netip.ParseAddr(b.IP)
	if err != nil || !overlay.Contains(ip) || !validHost(ip) || ip == n.cfg.Addr || pub == n.pub {
		n.logf("lan: %s: bad hello address %q", id, b.IP)
		return
	}

	n.helloMu.Lock()
	defer n.helloMu.Unlock()
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return
	}
	if p := n.peers[id]; p != nil && p.pub == pub && p.ip == ip {
		n.mu.Unlock()
		p.post(evHello{b.Gen})
		return
	}
	// A changed key or address under this id, or this key or address under
	// another id: the old entry goes.
	var stale []string
	for oid, op := range n.peers {
		if oid == id || op.pub == pub || op.ip == ip {
			stale = append(stale, oid)
		}
	}
	n.mu.Unlock()
	for _, oid := range stale {
		n.removePeer(oid, false)
	}

	p := &peer{
		n: n, id: id, pub: pub, ip: ip,
		controlling: bytes.Compare(n.pub[:], pub[:]) < 0,
		boot:        b.Gen,
		events:      make(chan any, 64),
		done:        make(chan struct{}),
		stopped:     make(chan struct{}),
		state:       api.PeerConnecting,
	}
	p.ep = n.bind.newEndpoint()
	// No persistent keepalive yet: it would start a handshake with no path,
	// and WireGuard then waits out its 5 s retry. kick turns it on once ICE
	// is up.
	cfg := fmt.Sprintf("public_key=%s\nendpoint=%s\nreplace_allowed_ips=true\nallowed_ip=%s/32\n",
		hex.EncodeToString(pub[:]), p.ep.addr, ip)
	if err := n.dev.IpcSet(cfg); err != nil {
		n.logf("lan: %s: add WireGuard peer: %v", id, err)
		n.bind.removeEndpoint(p.ep)
		return
	}
	n.shim.addPeer(ip)

	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		n.unconfigure(p)
		return
	}
	n.peers[id] = p
	early := n.early[id]
	delete(n.early, id)
	n.mu.Unlock()

	n.logf("lan: peer %s at %v (controlling=%v)", id, ip, p.controlling)
	go p.run()
	for _, eb := range early {
		p.post(evSignal{eb})
	}
	n.notify()
	// Answer with our own Hello, in case the GUI's announce crossed theirs.
	n.signal(id, n.Hello())
}

// Drop removes a peer (left the room, timed out, or said bye).
func (n *Node) Drop(id string) { n.removePeer(id, true) }

func (n *Node) removePeer(id string, notify bool) {
	n.mu.Lock()
	p := n.peers[id]
	delete(n.peers, id)
	delete(n.early, id)
	n.mu.Unlock()
	if p == nil {
		return
	}
	close(p.done)
	<-p.stopped
	n.unconfigure(p)
	n.logf("lan: peer %s removed", id)
	if notify {
		n.notify()
	}
}

// unconfigure takes a peer out of WireGuard, the shim and the bind.
func (n *Node) unconfigure(p *peer) {
	if err := n.dev.IpcSet("public_key=" + hex.EncodeToString(p.pub[:]) + "\nremove=true\n"); err != nil {
		n.logf("lan: %s: remove WireGuard peer: %v", p.id, err)
	}
	n.shim.removePeer(p.ip)
	n.bind.removeEndpoint(p.ep)
}

// SetICE replaces the ICE servers (fresh TURN credentials) for new agents.
func (n *Node) SetICE(servers []api.ICEServer) {
	n.mu.Lock()
	n.ice = slices.Clone(servers)
	n.mu.Unlock()
}

// SetPolicy changes the room policy; it applies to the next packet.
func (n *Node) SetPolicy(p api.Policy) { n.shim.setPolicy(p) }

// OnChange sets fn to be called (on its own goroutine, coalesced) whenever a
// peer's state, path, RTT or handshake changes.
func (n *Node) OnChange(fn func()) {
	n.mu.Lock()
	n.onChange = fn
	n.mu.Unlock()
}

// Peers reports every peer's tunnel.
func (n *Node) Peers() []api.PeerState {
	type row struct {
		st  api.PeerState
		pub [32]byte
		s   *session
	}
	n.mu.Lock()
	rows := make([]row, 0, len(n.peers))
	for id, p := range n.peers {
		rows = append(rows, row{api.PeerState{Peer: id, IP: p.ip.String(), State: p.state, Path: p.path}, p.pub, p.agentRef})
	}
	n.mu.Unlock()

	wg := n.wgStats()
	out := make([]api.PeerState, 0, len(rows))
	for _, r := range rows {
		if w, ok := wg[r.pub]; ok {
			r.st.LastHandshake, r.st.RxBytes, r.st.TxBytes = w.handshake, w.rx, w.tx
		}
		if r.s != nil && r.st.State == api.PeerConnected {
			if cp, ok := r.s.agent.GetSelectedCandidatePairStats(); ok && cp.CurrentRoundTripTime > 0 {
				r.st.RTTMs = max(1, int(cp.CurrentRoundTripTime*1000+0.5))
			}
		}
		out = append(out, r.st)
	}
	slices.SortFunc(out, func(a, b api.PeerState) int { return strings.Compare(a.Peer, b.Peer) })
	return out
}

type wgPeerStats struct {
	handshake int64
	rx, tx    uint64
}

// wgStats reads per-peer counters from the device's UAPI dump.
func (n *Node) wgStats() map[[32]byte]wgPeerStats {
	out := map[[32]byte]wgPeerStats{}
	dump, err := n.dev.IpcGet()
	if err != nil {
		return out
	}
	var cur *[32]byte
	var st wgPeerStats
	flush := func() {
		if cur != nil {
			out[*cur] = st
		}
	}
	for line := range strings.SplitSeq(dump, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "public_key":
			flush()
			b, err := hex.DecodeString(v)
			if err != nil || len(b) != 32 {
				cur = nil
				continue
			}
			key := [32]byte(b)
			cur, st = &key, wgPeerStats{}
		case "last_handshake_time_sec":
			st.handshake, _ = strconv.ParseInt(v, 10, 64)
		case "rx_bytes":
			st.rx, _ = strconv.ParseUint(v, 10, 64)
		case "tx_bytes":
			st.tx, _ = strconv.ParseUint(v, 10, 64)
		}
	}
	flush()
	return out
}

// Close tears down every peer, the device and the adapter it was given.
func (n *Node) Close() error {
	n.mu.Lock()
	if n.closed {
		n.mu.Unlock()
		return nil
	}
	n.closed = true
	ids := make([]string, 0, len(n.peers))
	for id := range n.peers {
		ids = append(ids, id)
	}
	n.mu.Unlock()
	for _, id := range ids {
		n.removePeer(id, false)
	}
	close(n.done)
	n.dev.Close()
	n.bind.shutdown()
	return nil
}

// publish records the peer's state for Peers and fires OnChange if it moved.
// Called by the peer's actor.
func (p *peer) publish(state, path string) {
	p.n.mu.Lock()
	changed := p.state != state || p.path != path || p.agentRef != p.main
	p.state, p.path, p.agentRef = state, path, p.main
	p.n.mu.Unlock()
	if changed {
		p.n.notify()
	}
}

// kick turns the persistent keepalive (20 s) on now that a path exists; the
// first one goes out at once and, with no session yet, handshakes. initiate
// (the controller) also starts a handshake explicitly, which covers a
// reconnect when the keepalive was already on. WireGuard skips either if it
// tried less than 5 s ago (the OS sent a packet before ICE was up); its own
// retry timer covers that case.
func (n *Node) kick(pub [32]byte, initiate bool) {
	key := hex.EncodeToString(pub[:])
	if err := n.dev.IpcSet("public_key=" + key + "\nupdate_only=true\npersistent_keepalive_interval=20\n"); err != nil {
		n.logf("lan: keepalive for %s: %v", key[:8], err)
	}
	if !initiate {
		return
	}
	if wp := n.dev.LookupPeer(device.NoisePublicKey(pub)); wp != nil {
		_ = wp.SendHandshakeInitiation(false)
	}
}

// nextGen allocates an agent generation. It starts from the clock, so a
// restarted node's generations are newer than the ones its peers remember.
func (n *Node) nextGen() uint32 {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.gen = max(n.gen+1, uint32(time.Now().Unix()))
	return n.gen
}

func (n *Node) notify() {
	select {
	case n.changed <- struct{}{}:
	default:
	}
}

// notifyLoop delivers OnChange: at once for state changes, and every few
// seconds if RTT or the last handshake moved.
func (n *Node) notifyLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var last []api.PeerState
	fire := func() {
		n.mu.Lock()
		fn := n.onChange
		n.mu.Unlock()
		if fn != nil {
			fn()
		}
	}
	for {
		select {
		case <-n.done:
			return
		case <-n.changed:
			last = statsKey(n.Peers())
			fire()
		case <-t.C:
			cur := statsKey(n.Peers())
			if !slices.Equal(cur, last) {
				last = cur
				fire()
			}
		}
	}
}

// statsKey drops the byte counters, which change on every packet.
func statsKey(ps []api.PeerState) []api.PeerState {
	for i := range ps {
		ps[i].RxBytes, ps[i].TxBytes = 0, 0
	}
	return ps
}

// PublicKey is the WireGuard (Curve25519) public key for priv.
func PublicKey(priv [32]byte) [32]byte {
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return [32]byte{} // only for a low-order result, which a clamped key can't give
	}
	return [32]byte(pub)
}

// AddrFromKey derives a node's overlay address: the first 8 bytes of
// SHA-256(pub || salt as 4 big-endian bytes), mod 65533, plus 2, as the host
// part of 100.96.0.0/16. That covers 100.96.0.2 to 100.96.255.254 and never
// gives .0.0 (network), .0.1 or .255.255 (broadcast). Salt is 0 until a
// collision makes the GUI ask for another (Readdress).
func AddrFromKey(pub [32]byte, salt uint32) netip.Addr {
	var in [36]byte
	copy(in[:], pub[:])
	binary.BigEndian.PutUint32(in[32:], salt)
	h := sha256.Sum256(in[:])
	host := uint16(binary.BigEndian.Uint64(h[:8])%65533 + 2)
	base := overlay.Addr().As4()
	return netip.AddrFrom4([4]byte{base[0], base[1], byte(host >> 8), byte(host)})
}

// validHost reports whether a is usable as a node address in the overlay.
func validHost(a netip.Addr) bool {
	if !a.Is4() {
		return false
	}
	b := a.As4()
	host := uint16(b[2])<<8 | uint16(b[3])
	return host >= 2 && host != 0xffff
}

func netipFromIP(ip net.IP) (netip.Addr, bool) {
	a, ok := netip.AddrFromSlice(ip)
	return a.Unmap(), ok
}
