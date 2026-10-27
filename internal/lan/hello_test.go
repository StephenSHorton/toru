package lan

import (
	"crypto/rand"
	"encoding/base64"
	"net/netip"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/pion/ice/v4"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// Hello handling and reconnects. Every agent here is on loopback
// (loopbackICE): nothing listens on a LAN interface.

// TestNodeRestartOneSide: B's node restarts (LAN off and on, or a service
// restart) with the same key and address while A keeps running and never
// drops B. A must answer B's new Hello, or B never adds A back.
func TestNodeRestartOneSide(t *testing.T) {
	a, b := pair(t)
	old := b.Node
	_ = old.Close()
	dev, tnet, err := netstack.CreateNetTUN([]netip.Addr{b.addr}, nil, api.MTU)
	if err != nil {
		t.Fatal(err)
	}
	inj := newInjectTUN(dev)
	nb, err := NewNode(inj, Config{PrivateKey: b.priv, Addr: b.addr, Signal: b.sig, Logf: logTo(t, "b2")})
	if err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.Node, b.net, b.inj = nb, tnet, inj
	b.mu.Unlock()
	// What host.start does: one room-wide Hello. Nothing else is sent.
	b.sig("", nb.Hello())
	waitFor(t, 25*time.Second, "reconnect after B's restart", func() bool {
		return connected(a.Peers(), "b") && connected(nb.Peers(), "a")
	})

	l, err := a.net.ListenUDPAddrPort(netip.AddrPortFrom(a.addr, 9100))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	c, err := tnet.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(a.addr, 9100))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if got, _, ok := recvUDP(t, l, func() { _, _ = c.Write([]byte("after restart")) }, 10*time.Second); !ok || got != "after restart" {
		t.Fatalf("got %q (ok=%v)", got, ok)
	}
}

// TestReconnectRepublishes: Disconnected then Connected again on the same
// pair (pion fires no pair change) puts the peer back to connected, with
// broadcast fan-out on.
func TestReconnectRepublishes(t *testing.T) {
	ip := netip.MustParseAddr("100.96.3.4")
	sh, _ := newTestShim(t, api.Policy{}, ip)
	n := &Node{shim: sh, peers: map[string]*peer{}, changed: make(chan struct{}, 1)}
	s := &session{connected: true, path: api.PathDirect}
	p := &peer{n: n, id: "v", ip: ip, main: s}
	p.publish(api.PeerConnected, s.path)

	up := func() bool { return (*sh.peers.Load())[ip] }
	p.onState(s, ice.ConnectionStateDisconnected)
	if p.state != api.PeerConnecting || up() {
		t.Fatalf("after Disconnected: state %q, fan-out %v", p.state, up())
	}
	p.onState(s, ice.ConnectionStateConnected)
	if p.state != api.PeerConnected || p.path != api.PathDirect || !up() {
		t.Fatalf("after Connected again: state %q path %q, fan-out %v", p.state, p.path, up())
	}
}

// soloNode is one Node with no partner: Hellos are handed to it directly.
func soloNode(t *testing.T) *Node {
	t.Helper()
	loopbackICE(t)
	var priv [32]byte
	rand.Read(priv[:])
	addr := AddrFromKey(PublicKey(priv), 0)
	dev, _, err := netstack.CreateNetTUN([]netip.Addr{addr}, nil, api.MTU)
	if err != nil {
		t.Fatal(err)
	}
	n, err := NewNode(dev, Config{PrivateKey: priv, Addr: addr, Logf: logTo(t, "n")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Close() })
	return n
}

func randKey() [32]byte {
	var priv [32]byte
	rand.Read(priv[:])
	return PublicKey(priv)
}

func helloFor(pub [32]byte, salt uint32) api.SignalBlob {
	return api.SignalBlob{Kind: api.KindHello, Gen: 7, WGPub: base64.StdEncoding.EncodeToString(pub[:]),
		IP: AddrFromKey(pub, salt).String(), Salt: salt}
}

// grind finds a key and salt whose address is target, as an attacker can.
func grind(t *testing.T, target netip.Addr) ([32]byte, uint32) {
	t.Helper()
	for range 50 {
		pub := randKey()
		for salt := uint32(0); salt <= maxSalt; salt++ {
			if AddrFromKey(pub, salt) == target {
				return pub, salt
			}
		}
	}
	t.Fatal("no key found for", target)
	return [32]byte{}, 0
}

func peerPub(n *Node, id string) ([32]byte, netip.Addr, bool) {
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.peers[id]
	if p == nil {
		return [32]byte{}, netip.Addr{}, false
	}
	return p.pub, p.ip, true
}

func TestHelloAddressBoundToKey(t *testing.T) {
	n := soloNode(t)
	v, m := randKey(), randKey()

	forged := helloFor(m, 0)
	forged.IP = AddrFromKey(v, 0).String()
	n.HandleSignal("m", forged)
	if _, _, ok := peerPub(n, "m"); ok {
		t.Fatal("a hello for another key's address was accepted")
	}
	big := helloFor(m, maxSalt+1)
	n.HandleSignal("m", big)
	if _, _, ok := peerPub(n, "m"); ok {
		t.Fatal("a salt over the bound was accepted")
	}
	n.HandleSignal("m", helloFor(m, 3))
	if pub, ip, ok := peerPub(n, "m"); !ok || pub != m || ip != AddrFromKey(m, 3) {
		t.Fatalf("a valid salted hello: ok=%v ip=%v", ok, ip)
	}
}

func TestHelloKeepsLiveHolder(t *testing.T) {
	t.Run("address under another id", func(t *testing.T) {
		n := soloNode(t)
		v := randKey()
		n.HandleSignal("v", helloFor(v, 0))
		m, salt := grind(t, AddrFromKey(v, 0))
		n.HandleSignal("m", helloFor(m, salt))
		if pub, _, ok := peerPub(n, "v"); !ok || pub != v {
			t.Fatal("the live holder of the address was evicted")
		}
		if _, _, ok := peerPub(n, "m"); ok {
			t.Fatal("the colliding claim was added")
		}

		// Once the holder has gone quiet, the held-back claim goes through.
		n.mu.Lock()
		n.peers["v"].added = time.Now().Add(-time.Hour)
		n.mu.Unlock()
		n.retryBlocked()
		if pub, _, ok := peerPub(n, "m"); !ok || pub != m {
			t.Fatal("the held-back hello didn't go through after the holder went quiet")
		}
		if _, _, ok := peerPub(n, "v"); ok {
			t.Fatal("the quiet holder is still there")
		}
	})

	t.Run("new key under the same id", func(t *testing.T) {
		n := soloNode(t)
		v, m := randKey(), randKey()
		n.HandleSignal("v", helloFor(v, 0))
		n.HandleSignal("v", helloFor(m, 0)) // forged From
		if pub, _, ok := peerPub(n, "v"); !ok || pub != v {
			t.Fatal("a live peer's key was replaced")
		}
	})

	t.Run("bye for a live peer", func(t *testing.T) {
		n := soloNode(t)
		v := randKey()
		n.HandleSignal("v", helloFor(v, 0))
		n.HandleSignal("v", api.SignalBlob{Kind: api.KindBye})
		if _, _, ok := peerPub(n, "v"); !ok {
			t.Fatal("a bye removed a live peer")
		}
		n.mu.Lock()
		n.peers["v"].added = time.Now().Add(-time.Hour)
		n.mu.Unlock()
		n.HandleSignal("v", api.SignalBlob{Kind: api.KindBye})
		if _, _, ok := peerPub(n, "v"); ok {
			t.Fatal("a bye didn't remove a quiet peer")
		}
	})
}
