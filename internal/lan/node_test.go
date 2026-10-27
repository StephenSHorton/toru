package lan

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/pion/ice/v4"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

// The acceptance test: two Nodes in one process, each on a gVisor netstack
// (no adapter, no admin), ICE over loopback host candidates only. Nothing in
// it listens on a real interface: netstack is userspace, ICE binds 127.0.0.1
// only, mDNS is off and there are no STUN or TURN servers.

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// loopbackICE keeps every agent on 127.0.0.1 (see the no-firewall-prompt rule
// in the package tests: nothing may listen on a LAN interface).
// mainTypes are the candidate types of the main (non-upgrade) agents; the
// relay test makes them relay-only so the first path is relayed.
func loopbackICE(t *testing.T, mainTypes ...ice.CandidateType) {
	loop := func(ip net.IP) bool { return ip.IsLoopback() && ip.To4() != nil }
	if len(mainTypes) == 0 {
		mainTypes = []ice.CandidateType{ice.CandidateTypeHost}
	}
	testAgentOptions = func(direct bool) []ice.AgentOption {
		types := mainTypes
		if direct {
			types = []ice.CandidateType{ice.CandidateTypeHost}
		}
		return []ice.AgentOption{
			ice.WithIncludeLoopback(),
			ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}),
			ice.WithCandidateTypes(types),
			ice.WithIPFilter(loop),
			ice.WithRemoteIPFilter(loop),
			ice.WithInterfaceFilter(func(string) bool { return true }),
		}
	}
	t.Cleanup(func() { testAgentOptions = nil })
}

// injectTUN wraps a netstack device so a test can also hand the shim packets
// the netstack would refuse to send (255.255.255.255 needs SO_BROADCAST,
// which gonet doesn't expose; spoofed sources).
type injectTUN struct {
	tun.Device
	inject chan []byte
	pkts   chan []byte
	once   sync.Once
}

func newInjectTUN(dev tun.Device) *injectTUN {
	return &injectTUN{Device: dev, inject: make(chan []byte, 16), pkts: make(chan []byte, 64)}
}

func (d *injectTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	d.once.Do(func() {
		go func() {
			defer close(d.pkts)
			buf := [][]byte{make([]byte, 65535)}
			sz := []int{0}
			for {
				n, err := d.Device.Read(buf, sz, 0)
				if err != nil {
					return
				}
				if n == 1 {
					d.pkts <- append([]byte(nil), buf[0][:sz[0]]...)
				}
			}
		}()
	})
	var p []byte
	select {
	case p = <-d.inject:
	case q, ok := <-d.pkts:
		if !ok {
			return 0, os.ErrClosed
		}
		p = q
	}
	sizes[0] = copy(bufs[0][offset:], p)
	return 1, nil
}

func (d *injectTUN) BatchSize() int { return 1 }

type testNode struct {
	*Node
	name string
	addr netip.Addr
	net  *netstack.Net
	inj  *injectTUN
	priv [32]byte
	sig  func(string, api.SignalBlob) // delivers to the other node
	mu   *sync.Mutex                  // guards Node against the delivery goroutines
}

// pair builds two connected nodes, a and b, whose Signal funcs deliver to each
// other asynchronously (like the room does), and waits until ICE is up.
func pair(t *testing.T) (a, b *testNode) {
	t.Helper()
	loopbackICE(t)
	return pairWith(t, nil)
}

func pairWith(t *testing.T, servers []api.ICEServer) (a, b *testNode) {
	t.Helper()
	mk := func(name string, extra ...netip.Addr) (*testNode, [32]byte) {
		var priv [32]byte
		rand.Read(priv[:])
		addr := AddrFromKey(PublicKey(priv), 0)
		dev, tnet, err := netstack.CreateNetTUN(append([]netip.Addr{addr}, extra...), nil, api.MTU)
		if err != nil {
			t.Fatal(err)
		}
		return &testNode{name: name, addr: addr, net: tnet, inj: newInjectTUN(dev), priv: priv}, priv
	}
	// A also owns 100.96.255.255 as a local address: CreateNetTUN gives /32
	// addresses, so without it gVisor wouldn't accept a subnet broadcast. A
	// real Wintun adapter is a /16 and takes it natively.
	a, privA := mk("a", subnetBcast)
	b, privB := mk("b")

	var mu sync.Mutex
	closed := false
	deliver := func(to **testNode, from string) func(string, api.SignalBlob) {
		return func(_ string, blob api.SignalBlob) {
			go func() {
				mu.Lock()
				dead := closed
				n := (*to).Node
				mu.Unlock()
				if !dead && n != nil {
					n.HandleSignal(from, blob)
				}
			}()
		}
	}
	a.sig, b.sig = deliver(&b, "a"), deliver(&a, "b")
	a.mu, b.mu = &mu, &mu
	for _, x := range []struct {
		n    *testNode
		priv [32]byte
	}{{a, privA}, {b, privB}} {
		n, err := NewNode(x.n.inj, Config{PrivateKey: x.priv, Addr: x.n.addr, ICEServers: servers, Signal: x.n.sig, Logf: logTo(t, x.n.name)})
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		x.n.Node = n
		mu.Unlock()
		t.Cleanup(func() {
			mu.Lock()
			closed = true
			cur := x.n.Node
			mu.Unlock()
			_ = n.Close()
			_ = cur.Close() // a restarted node (TestNodeRestartOneSide)
		})
	}
	go a.HandleSignal("b", b.Hello())
	go b.HandleSignal("a", a.Hello())
	waitFor(t, 20*time.Second, "both peers connected", func() bool {
		return connected(a.Peers(), "b") && connected(b.Peers(), "a")
	})
	return a, b
}

func logTo(t *testing.T, name string) func(string, ...any) {
	return func(f string, a ...any) { t.Logf(name+": "+f, a...) }
}

func connected(ps []api.PeerState, id string) bool {
	for _, p := range ps {
		if p.Peer == id {
			return p.State == api.PeerConnected
		}
	}
	return false
}

func waitFor(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// recvUDP sends with send every 200 ms until l receives, then returns what
// arrived and from where.
func recvUDP(t *testing.T, l net.PacketConn, send func(), d time.Duration) (string, netip.AddrPort, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	buf := make([]byte, 2048)
	for time.Now().Before(deadline) {
		send()
		_ = l.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		n, from, err := l.ReadFrom(buf)
		if err == nil {
			ap, _ := netip.ParseAddrPort(from.String())
			return string(buf[:n]), ap, true
		}
	}
	return "", netip.AddrPort{}, false
}

func TestNodeAcceptance(t *testing.T) {
	a, b := pair(t)
	ps := a.Peers()
	if len(ps) != 1 || ps[0].IP != b.addr.String() || ps[0].Path != api.PathDirect {
		t.Fatalf("a's peers: %+v", ps)
	}

	t.Run("unicast", func(t *testing.T) {
		l, err := a.net.ListenUDPAddrPort(netip.AddrPortFrom(a.addr, 9000))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		c, err := b.net.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(a.addr, 9000))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		got, from, ok := recvUDP(t, l, func() { _, _ = c.Write([]byte("unicast")) }, 10*time.Second)
		if !ok || got != "unicast" || from.Addr() != b.addr {
			t.Fatalf("got %q from %v (ok=%v)", got, from, ok)
		}
		waitFor(t, 5*time.Second, "a WireGuard handshake", func() bool { return a.Peers()[0].LastHandshake != 0 })
	})

	// The wildcard listener: through wireguard's netstack wrapper, binding
	// 0.0.0.0 is an exact match on that address in gVisor, and only the
	// dual-stack any address (what a nil IP gives) catches every IPv4
	// destination.
	game, err := a.net.ListenUDP(&net.UDPAddr{Port: 6112})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = game.Close() }()

	t.Run("subnet broadcast", func(t *testing.T) {
		c, err := b.net.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(subnetBcast, 6112))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		got, from, ok := recvUDP(t, game, func() { _, _ = c.Write([]byte("wc3 game")) }, 10*time.Second)
		if !ok {
			t.Logf("b shim %s; a shim %s", b.shim.stats.String(), a.shim.stats.String())
		}
		if !ok || got != "wc3 game" || from.Addr() != b.addr {
			t.Fatalf("got %q from %v (ok=%v)", got, from, ok)
		}
		if b.shim.stats.Fanout.Load() == 0 || a.shim.stats.Unwrapped.Load() == 0 {
			t.Fatal("broadcast didn't go through the wrapper")
		}
	})

	t.Run("limited broadcast", func(t *testing.T) {
		// gonet can't send to 255.255.255.255 (no SO_BROADCAST), so the
		// packet B's OS would send is injected into B's shim Read path.
		pkt := udp4(b.addr, limitedBcast, 5555, 6112, []byte("limited"))
		got, from, ok := recvUDP(t, game, func() { b.inj.inject <- pkt }, 10*time.Second)
		if !ok || got != "limited" || from != netip.AddrPortFrom(b.addr, 5555) {
			t.Fatalf("got %q from %v (ok=%v)", got, from, ok)
		}
	})

	t.Run("spoofed broadcast dropped", func(t *testing.T) {
		before := a.shim.stats.Spoofed.Load()
		// A wrapper B builds by hand (B's own shim would never wrap a foreign
		// source): unicast to A, so it passes B's shim and WireGuard.
		spoof := udp4(netip.MustParseAddr("100.96.9.9"), subnetBcast, 5555, 6112, []byte("spoof"))
		w := (&shim{self: b.addr}).wrap(a.addr, spoof)
		b.inj.inject <- w
		waitFor(t, 5*time.Second, "a to drop the spoof", func() bool { return a.shim.stats.Spoofed.Load() > before })
		_ = game.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		if n, from, err := game.ReadFrom(make([]byte, 100)); err == nil {
			t.Fatalf("spoofed broadcast delivered: %d bytes from %v", n, from)
		}
	})

	t.Run("windows services blocked", func(t *testing.T) {
		l, err := a.net.ListenTCPAddrPort(netip.AddrPortFrom(a.addr, 445))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				_, _ = c.Write([]byte("smb"))
				_ = c.Close()
			}
		}()
		dial := func(d time.Duration) error {
			ctx, cancel := context.WithTimeout(context.Background(), d)
			defer cancel()
			c, err := b.net.DialContextTCPAddrPort(ctx, netip.AddrPortFrom(a.addr, 445))
			if err != nil {
				return err
			}
			_ = c.Close()
			return nil
		}
		if err := dial(2 * time.Second); err == nil {
			t.Fatal("TCP 445 connected with AllowServices off")
		}
		if a.shim.stats.Blocked.Load() == 0 {
			t.Fatal("no blocked packets counted")
		}
		a.SetPolicy(api.Policy{AllowServices: true})
		if err := dial(5 * time.Second); err != nil {
			t.Fatalf("TCP 445 with AllowServices on: %v", err)
		}
		a.SetPolicy(api.Policy{})
	})

	t.Run("drop", func(t *testing.T) {
		changed := make(chan struct{}, 4)
		a.OnChange(func() { changed <- struct{}{} })
		a.Drop("b")
		if ps := a.Peers(); len(ps) != 0 {
			t.Fatalf("peers after Drop: %+v", ps)
		}
		dump, _ := a.dev.IpcGet()
		if strings.Contains(dump, "public_key=") {
			t.Fatal("WireGuard still has the peer")
		}
		if a.shim.isPeer(b.addr) {
			t.Fatal("shim still has the peer")
		}
		select {
		case <-changed:
		case <-time.After(2 * time.Second):
			t.Fatal("OnChange not called on Drop")
		}
		l, err := a.net.ListenUDPAddrPort(netip.AddrPortFrom(a.addr, 9001))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = l.Close() }()
		c, err := b.net.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(a.addr, 9001))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = c.Close() }()
		if got, from, ok := recvUDP(t, l, func() { _, _ = c.Write([]byte("after drop")) }, time.Second); ok {
			t.Fatalf("received %q from %v after Drop", got, from)
		}
	})
}

// TestNodeRejoin: a peer that restarts (new boot, new agents) reconnects
// without waiting for ICE timeouts on the other side.
func TestNodeRejoin(t *testing.T) {
	a, b := pair(t)
	a.Drop("b")
	b.Drop("a")
	go a.HandleSignal("b", b.Hello())
	go b.HandleSignal("a", a.Hello())
	waitFor(t, 20*time.Second, "reconnect", func() bool {
		return connected(a.Peers(), "b") && connected(b.Peers(), "a")
	})
}
