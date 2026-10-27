//go:build lanstub

package lan

// stub_winhost.go stands in for package lan (the data plane: shim, ICE bind,
// wireguard-go device) while it is written in parallel, so the service host
// (cmd/toru-lan) compiles and tests against exactly the shared API. Build
// with -tags lanstub. Delete this file once the real package lands; nothing
// else changes.
//
// The fake Node records what it is told, drains the device so a real Wintun
// ring never fills, and reports no peers until a test calls SetPeers.

import (
	"crypto/sha256"
	"encoding/binary"
	"net/netip"
	"sync"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/tun"
)

type Config struct {
	PrivateKey [32]byte
	Addr       netip.Addr
	ICEServers []api.ICEServer
	Policy     api.Policy
	Signal     func(peer string, blob api.SignalBlob)
	Logf       func(format string, args ...any)
}

type Node struct {
	dev tun.Device
	cfg Config

	mu       sync.Mutex
	peers    []api.PeerState
	onChange func()
	closed   bool
	// Recorded calls, for tests.
	Signals []string // "peer kind"
	Dropped []string
	ICE     [][]api.ICEServer
	Policy  []api.Policy
}

func NewNode(dev tun.Device, cfg Config) (*Node, error) {
	n := &Node{dev: dev, cfg: cfg}
	go n.drain()
	return n, nil
}

func (n *Node) drain() {
	bufs := [][]byte{make([]byte, 65535)}
	sizes := []int{0}
	for {
		if _, err := n.dev.Read(bufs, sizes, 0); err != nil {
			return // device closed
		}
	}
}

func (n *Node) Hello() api.SignalBlob {
	return api.SignalBlob{Kind: api.KindHello, IP: n.cfg.Addr.String()}
}

func (n *Node) HandleSignal(peer string, blob api.SignalBlob) {
	n.mu.Lock()
	n.Signals = append(n.Signals, peer+" "+blob.Kind)
	n.mu.Unlock()
}

func (n *Node) Drop(peer string) {
	n.mu.Lock()
	n.Dropped = append(n.Dropped, peer)
	n.mu.Unlock()
}

func (n *Node) SetICE(servers []api.ICEServer) {
	n.mu.Lock()
	n.ICE = append(n.ICE, servers)
	n.mu.Unlock()
}

func (n *Node) SetPolicy(p api.Policy) {
	n.mu.Lock()
	n.Policy = append(n.Policy, p)
	n.mu.Unlock()
}

func (n *Node) Peers() []api.PeerState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.PeerState(nil), n.peers...)
}

func (n *Node) OnChange(fn func()) {
	n.mu.Lock()
	n.onChange = fn
	n.mu.Unlock()
}

// SetPeers is stub-only: it replaces the peer list and fires OnChange.
func (n *Node) SetPeers(p []api.PeerState) {
	n.mu.Lock()
	n.peers = p
	fn := n.onChange
	n.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

// EmitSignal is stub-only: it makes the node send a blob to a peer.
func (n *Node) EmitSignal(peer string, blob api.SignalBlob) { n.cfg.Signal(peer, blob) }

func (n *Node) Close() error {
	n.mu.Lock()
	n.closed = true
	n.mu.Unlock()
	return nil
}

func PublicKey(priv [32]byte) [32]byte {
	var pub [32]byte
	curve25519.ScalarBaseMult(&pub, &priv)
	return pub
}

func AddrFromKey(pub [32]byte, salt uint32) netip.Addr {
	var s [4]byte
	binary.BigEndian.PutUint32(s[:], salt)
	h := sha256.Sum256(append(pub[:], s[:]...))
	host := uint32(binary.BigEndian.Uint16(h[:2]))%65533 + 2 // skips .0.0, .0.1 and .255.255
	return netip.AddrFrom4([4]byte{100, 96, byte(host >> 8), byte(host)})
}
