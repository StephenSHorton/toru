package main

import (
	"encoding/json"
	"errors"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan"
	"github.com/StephenSHorton/toru/internal/lan/adapter"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/StephenSHorton/toru/internal/lan/pipe"
	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/tuntest"
)

// These tests run the host against an in-memory tun device and a fake node
// (plus one smoke test with the real lan.Node); nothing here creates an
// adapter or opens a socket.

// fakeNode records what the host tells it and reports no peers until a test
// calls SetPeers.
type fakeNode struct {
	cfg lan.Config

	mu       sync.Mutex
	peers    []api.PeerState
	onChange func()
	Signals  []string // "peer kind"
	Dropped  []string
	ICE      [][]api.ICEServer
	Policy   []api.Policy
}

func (n *fakeNode) Hello() api.SignalBlob {
	return api.SignalBlob{Kind: api.KindHello, IP: n.cfg.Addr.String()}
}

func (n *fakeNode) HandleSignal(peer string, blob api.SignalBlob) {
	n.mu.Lock()
	n.Signals = append(n.Signals, peer+" "+blob.Kind)
	n.mu.Unlock()
}

func (n *fakeNode) Drop(peer string) {
	n.mu.Lock()
	n.Dropped = append(n.Dropped, peer)
	n.mu.Unlock()
}

func (n *fakeNode) SetICE(servers []api.ICEServer) {
	n.mu.Lock()
	n.ICE = append(n.ICE, servers)
	n.mu.Unlock()
}

func (n *fakeNode) SetPolicy(p api.Policy) {
	n.mu.Lock()
	n.Policy = append(n.Policy, p)
	n.mu.Unlock()
}

func (n *fakeNode) Peers() []api.PeerState {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]api.PeerState(nil), n.peers...)
}

func (n *fakeNode) OnChange(fn func()) {
	n.mu.Lock()
	n.onChange = fn
	n.mu.Unlock()
}

func (n *fakeNode) Close() error { return nil }

// SetPeers replaces the peer list and fires OnChange.
func (n *fakeNode) SetPeers(p []api.PeerState) {
	n.mu.Lock()
	n.peers = p
	fn := n.onChange
	n.mu.Unlock()
	if fn != nil {
		go fn()
	}
}

// EmitSignal makes the node send a blob to a peer.
func (n *fakeNode) EmitSignal(peer string, blob api.SignalBlob) { n.cfg.Signal(peer, blob) }

type sent struct {
	mu   sync.Mutex
	msgs []api.Message
	rel  int
}

func (s *sent) SendOwner(m api.Message) {
	s.mu.Lock()
	s.msgs = append(s.msgs, m)
	s.mu.Unlock()
}

func (s *sent) Released() {
	s.mu.Lock()
	s.rel++
	s.mu.Unlock()
}

func (s *sent) take() []api.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.msgs
	s.msgs = nil
	return m
}

type fakeAdapter struct {
	dev    *onceCloseTUN
	addr   netip.Addr
	closed bool
}

func newFakeAdapter(addr netip.Addr) *fakeAdapter {
	return &fakeAdapter{dev: &onceCloseTUN{Device: tuntest.NewChannelTUN().TUN()}, addr: addr}
}

func (f *fakeAdapter) Device() tun.Device { return f.dev }
func (f *fakeAdapter) Close() error       { f.closed = true; return f.dev.Close() }

// onceCloseTUN makes Close idempotent, as Wintun's is: the real lan.Node
// closes the device it was given and the adapter closes it again.
type onceCloseTUN struct {
	tun.Device
	once sync.Once
}

func (d *onceCloseTUN) Close() error {
	d.once.Do(func() { _ = d.Device.Close() })
	return nil
}

func testStore(t *testing.T) *pipe.KeyStore {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return &pipe.KeyStore{Dir: filepath.Join(t.TempDir(), "keys"), SDDL: "D:P(A;OICI;FA;;;" + u.User.Sid.String() + ")"}
}

type harness struct {
	h        *host
	out      *sent
	adapters []*fakeAdapter
	nodes    []*fakeNode
	adErr    error
	cfgs     []lan.Config
}

func newHarness(t *testing.T) *harness {
	hs := &harness{out: &sent{}}
	h := newHost(testStore(t), t.Logf)
	h.version = "test"
	h.out = hs.out
	h.ensureFirewall = nil
	h.newAdapter = func(addr netip.Addr) (netAdapter, error) {
		if hs.adErr != nil {
			return nil, hs.adErr
		}
		a := newFakeAdapter(addr)
		hs.adapters = append(hs.adapters, a)
		return a, nil
	}
	h.newNode = func(dev tun.Device, cfg lan.Config) (node, error) {
		n := &fakeNode{cfg: cfg}
		hs.cfgs = append(hs.cfgs, cfg)
		hs.nodes = append(hs.nodes, n)
		return n, nil
	}
	hs.h = h
	return hs
}

const sid = "S-1-5-21-10-20-30-1001"

func TestHostUpDown(t *testing.T) {
	hs := newHarness(t)
	pub, addr, err := hs.h.Identity(sid)
	if err != nil {
		t.Fatal(err)
	}
	if !adapter.ValidAddr(addr) || addr != lan.AddrFromKey(pub, 0) {
		t.Fatalf("identity address %s", addr)
	}
	up := api.Up{Room: "room1", Self: "me", ICEServers: []api.ICEServer{{URLs: []string{"stun:x"}}}, Policy: api.Policy{MDNS: true}}
	if err := hs.h.Up(pipe.Owner{Session: 1, SID: sid}, up); err != nil {
		t.Fatal(err)
	}
	if len(hs.adapters) != 1 || hs.adapters[0].addr != addr {
		t.Fatalf("adapter not brought up on %s", addr)
	}
	cfg := hs.cfgs[0]
	key, _ := hs.h.keys.Load(sid)
	if cfg.PrivateKey != key.Private || cfg.Addr != addr || !cfg.Policy.MDNS || len(cfg.ICEServers) != 1 {
		t.Fatalf("node config %+v", cfg)
	}
	msgs := hs.out.take()
	var states []string
	var hello *api.Signal
	for _, m := range msgs {
		switch m.Type {
		case api.TypeStatus:
			states = append(states, m.Status.State)
		case api.TypeSignal:
			hello = m.Signal
		}
	}
	if strings.Join(states, ",") != "starting,on" {
		t.Fatalf("status pushes %v", states)
	}
	if hello == nil || hello.Peer != "" {
		t.Fatalf("no hello announcement: %+v", msgs)
	}
	var blob api.SignalBlob
	_ = json.Unmarshal(hello.Blob, &blob)
	if blob.Kind != api.KindHello {
		t.Fatalf("announced %+v", blob)
	}
	st := hs.h.Status()
	if st.State != api.StateOn || st.IP != addr.String() || st.PubKey != encodeKey(pub) || st.Room != "room1" || st.Version != "test" {
		t.Fatalf("status %+v", st)
	}

	// Signals in, signals out, drop, ICE, policy reach the node.
	if err := hs.h.Signal("peerA", api.SignalBlob{Kind: api.KindCreds}); err != nil {
		t.Fatal(err)
	}
	hs.nodes[0].EmitSignal("peerA", api.SignalBlob{Kind: api.KindCand, Cand: "c"})
	_ = hs.h.Drop("peerA")
	_ = hs.h.SetICE(nil)
	_ = hs.h.SetPolicy(api.Policy{AllowServices: true})
	n := hs.nodes[0]
	if len(n.Signals) != 1 || n.Signals[0] != "peerA creds" || len(n.Dropped) != 1 || len(n.ICE) != 1 || len(n.Policy) != 1 {
		t.Fatalf("node saw %+v", n)
	}
	if m := hs.out.take(); len(m) != 1 || m[0].Type != api.TypeSignal || m[0].Signal.Peer != "peerA" {
		t.Fatalf("outgoing signal %+v", m)
	}

	// Peer changes become PeerState pushes plus a Status.
	n.SetPeers([]api.PeerState{{Peer: "peerB", State: api.PeerConnected, Path: api.PathDirect}})
	waitFor(t, func() bool {
		for _, m := range hs.out.take() {
			if m.Type == api.TypePeerState && m.PeerState.Peer == "peerB" {
				return true
			}
		}
		return false
	})

	if err := hs.h.Down(); err != nil {
		t.Fatal(err)
	}
	if !hs.adapters[0].closed {
		t.Fatal("adapter left up")
	}
	if st := hs.h.Status(); st.State != api.StateOff || st.IP != "" {
		t.Fatalf("status after down %+v", st)
	}
	// A late callback from the closed node must not reach the GUI.
	hs.out.take()
	n.EmitSignal("peerA", api.SignalBlob{Kind: api.KindCand})
	if m := hs.out.take(); len(m) != 0 {
		t.Fatalf("stale node still talks: %+v", m)
	}
	if err := hs.h.Signal("p", api.SignalBlob{Kind: api.KindCand}); err == nil {
		t.Fatal("signal accepted while down")
	}
}

// TestHostRealNode runs Up and Down with the real lan.Node on an in-memory
// device: no peers, so no ICE agent and no socket.
func TestHostRealNode(t *testing.T) {
	hs := newHarness(t)
	hs.h.newNode = func(dev tun.Device, cfg lan.Config) (node, error) {
		return lan.NewNode(dev, cfg)
	}
	_, addr, err := hs.h.Identity(sid)
	if err != nil {
		t.Fatal(err)
	}
	if err := hs.h.Up(pipe.Owner{Session: 1, SID: sid}, api.Up{Room: "r", Self: "me"}); err != nil {
		t.Fatal(err)
	}
	var hello api.SignalBlob
	for _, m := range hs.out.take() {
		if m.Type == api.TypeSignal && m.Signal.Peer == "" {
			_ = json.Unmarshal(m.Signal.Blob, &hello)
		}
	}
	if hello.Kind != api.KindHello || hello.IP != addr.String() || hello.WGPub == "" {
		t.Fatalf("real node hello %+v", hello)
	}
	if err := hs.h.Down(); err != nil {
		t.Fatal(err)
	}
	if !hs.adapters[0].closed {
		t.Fatal("adapter left up")
	}
}

func TestHostRangeInUse(t *testing.T) {
	hs := newHarness(t)
	hs.adErr = &adapter.RangeInUseError{Prefix: netip.MustParsePrefix("100.64.0.0/10"), Alias: "Tailscale"}
	err := hs.h.Up(pipe.Owner{Session: 1, SID: sid}, api.Up{Room: "r", Self: "me"})
	var ce *pipe.CodeError
	if !errors.As(err, &ce) || ce.Code != api.ErrRangeInUse || ce.Detail != "Tailscale" {
		t.Fatalf("error %v", err)
	}
	if st := hs.h.Status(); st.State != api.StateError || st.Error != api.ErrRangeInUse {
		t.Fatalf("status %+v", st)
	}
	hs.adErr = errors.New("wintun broke")
	err = hs.h.Up(pipe.Owner{Session: 1, SID: sid}, api.Up{Room: "r", Self: "me"})
	if !errors.As(err, &ce) || ce.Code != api.ErrAdapter {
		t.Fatalf("error %v", err)
	}
}

func TestHostReaddress(t *testing.T) {
	hs := newHarness(t)
	_, before, _ := hs.h.Identity(sid)
	if err := hs.h.Up(pipe.Owner{Session: 1, SID: sid}, api.Up{Room: "r", Self: "me"}); err != nil {
		t.Fatal(err)
	}
	if err := hs.h.Readdress(sid, 42, true); err != nil {
		t.Fatal(err)
	}
	_, after, _ := hs.h.Identity(sid)
	if after == before {
		t.Fatalf("address unchanged after readdress: %s", after)
	}
	if len(hs.adapters) != 2 || !hs.adapters[0].closed || hs.adapters[1].addr != after {
		t.Fatalf("not rebuilt on the new address")
	}
	if st := hs.h.Status(); st.State != api.StateOn || st.IP != after.String() {
		t.Fatalf("status %+v", st)
	}
	// Stored for later without touching a LAN that isn't this user's.
	if err := hs.h.Readdress("S-1-5-21-10-20-30-1002", 7, false); err != nil {
		t.Fatal(err)
	}
	if len(hs.adapters) != 2 {
		t.Fatal("readdress of another user restarted the LAN")
	}
}

func TestHostIdentityPerUser(t *testing.T) {
	hs := newHarness(t)
	a, _, _ := hs.h.Identity(sid)
	b, _, _ := hs.h.Identity("S-1-5-21-10-20-30-1002")
	a2, _, _ := hs.h.Identity(sid)
	if a == b || a != a2 {
		t.Fatal("keys are not one per user")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("timed out")
}
