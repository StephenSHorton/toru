package main

import (
	"errors"
	"net/netip"
	"sync"

	"github.com/StephenSHorton/toru/internal/lan"
	"github.com/StephenSHorton/toru/internal/lan/adapter"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/StephenSHorton/toru/internal/lan/pipe"
	"golang.zx2c4.com/wireguard/tun"
)

// host_windows.go is the service's pipe.Backend: it turns the GUI's Up and
// Down into a key, an adapter and a lan.Node, and the node's signaling and
// peer changes back into pipe messages for the GUI.

// node is the part of *lan.Node the host uses; tests substitute their own.
type node interface {
	Hello() api.SignalBlob
	HandleSignal(peer string, blob api.SignalBlob)
	Drop(peer string)
	SetICE(servers []api.ICEServer)
	SetPolicy(p api.Policy)
	Peers() []api.PeerState
	OnChange(fn func())
	Close() error
}

// netAdapter is the part of *adapter.Adapter the host uses.
type netAdapter interface {
	Device() tun.Device
	Close() error
}

// outbox is the pipe server as the host sees it.
type outbox interface {
	SendOwner(m api.Message)
	Released()
}

type host struct {
	keys    *pipe.KeyStore
	version string
	logf    func(format string, args ...any)
	out     outbox

	newAdapter     func(addr netip.Addr) (netAdapter, error)
	newNode        func(dev tun.Device, cfg lan.Config) (node, error)
	ensureFirewall func()

	mu    sync.Mutex
	gen   uint64 // bumps on every Up and Down, so late callbacks from an old node are dropped
	state api.Status
	ad    netAdapter
	n     node
	up    api.Up
	owner pipe.Owner
	peers map[string]api.PeerState
}

func newHost(keys *pipe.KeyStore, logf func(string, ...any)) *host {
	return &host{
		keys:    keys,
		version: version,
		logf:    logf,
		newAdapter: func(addr netip.Addr) (netAdapter, error) {
			a, err := adapter.Up(adapter.Config{Addr: addr, Logf: logf})
			if err != nil {
				return nil, err
			}
			return a, nil
		},
		newNode: func(dev tun.Device, cfg lan.Config) (node, error) {
			n, err := lan.NewNode(dev, cfg)
			if err != nil {
				return nil, err
			}
			return n, nil
		},
		ensureFirewall: func() {
			// Install created the rules; put them back if someone removed
			// them. The adapter exists now, which rule creation needs.
			if ok, err := adapter.RulesPresent(); err == nil && ok {
				return
			}
			if err := adapter.EnsureFirewallRules(); err != nil {
				logf("firewall rules: %v", err)
			} else {
				logf("firewall rules were missing; recreated")
			}
		},
		state: api.Status{State: api.StateOff},
		peers: map[string]api.PeerState{},
	}
}

func (h *host) identity(sid string) (pipe.Key, [32]byte, netip.Addr, error) {
	k, err := h.keys.Load(sid)
	if err != nil {
		return pipe.Key{}, [32]byte{}, netip.Addr{}, err
	}
	pub := lan.PublicKey(k.Private)
	return k, pub, lan.AddrFromKey(pub, k.Salt), nil
}

func (h *host) Identity(sid string) ([32]byte, netip.Addr, error) {
	_, pub, addr, err := h.identity(sid)
	return pub, addr, err
}

func (h *host) Up(owner pipe.Owner, up api.Up) error {
	h.mu.Lock()
	h.owner, h.up = owner, up
	h.mu.Unlock()
	return h.start()
}

// start brings adapter and node up for h.owner and h.up.
func (h *host) start() error {
	h.mu.Lock()
	owner, up := h.owner, h.up
	h.gen++
	gen := h.gen
	h.state = api.Status{State: api.StateStarting, Room: up.Room, Version: h.version}
	h.mu.Unlock()
	h.pushStatus()

	key, pub, addr, err := h.identity(owner.SID)
	if err != nil {
		return h.fail(&pipe.CodeError{Code: api.ErrAdapter, Detail: "key: " + err.Error()})
	}
	ad, err := h.newAdapter(addr)
	if err != nil {
		var ru *adapter.RangeInUseError
		if errors.As(err, &ru) {
			return h.fail(&pipe.CodeError{Code: api.ErrRangeInUse, Detail: ru.Alias})
		}
		return h.fail(&pipe.CodeError{Code: api.ErrAdapter, Detail: err.Error()})
	}
	if h.ensureFirewall != nil {
		h.ensureFirewall()
	}
	n, err := h.newNode(ad.Device(), lan.Config{
		PrivateKey: key.Private,
		Addr:       addr,
		ICEServers: up.ICEServers,
		Policy:     up.Policy,
		Signal:     func(peer string, blob api.SignalBlob) { h.signal(gen, peer, blob) },
		Logf:       h.logf,
	})
	if err != nil {
		ad.Close()
		return h.fail(&pipe.CodeError{Code: api.ErrAdapter, Detail: "node: " + err.Error()})
	}
	n.OnChange(func() { h.peersChanged(gen) })

	h.mu.Lock()
	h.ad, h.n = ad, n
	h.peers = map[string]api.PeerState{}
	h.state = api.Status{State: api.StateOn, IP: addr.String(), PubKey: encodeKey(pub), Room: up.Room, Version: h.version}
	h.mu.Unlock()
	h.logf("LAN up: room %s, %s, user %s, session %d", up.Room, addr, owner.SID, owner.Session)
	h.pushStatus()
	// Peer "" asks the GUI to send the blob to every room member: the
	// service doesn't know who is in the room.
	h.signal(gen, "", n.Hello())
	return nil
}

func (h *host) fail(err *pipe.CodeError) error {
	h.mu.Lock()
	h.state = api.Status{State: api.StateError, Error: err.Code, Detail: err.Detail, Room: h.up.Room, Version: h.version}
	h.mu.Unlock()
	h.logf("LAN up failed: %v", err)
	h.pushStatus()
	return err
}

func (h *host) Down() error {
	err := h.stop()
	h.pushStatus()
	return err
}

// stop closes node and adapter (the adapter puts the metric back and
// disappears).
func (h *host) stop() error {
	h.mu.Lock()
	h.gen++
	n, ad := h.n, h.ad
	h.n, h.ad = nil, nil
	h.peers = map[string]api.PeerState{}
	h.state = api.Status{State: api.StateOff, Version: h.version}
	h.mu.Unlock()
	var errs []error
	if n != nil {
		errs = append(errs, n.Close())
	}
	if ad != nil {
		errs = append(errs, ad.Close())
		h.logf("LAN down")
	}
	return errors.Join(errs...)
}

func (h *host) running() node {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

func (h *host) Signal(peer string, blob api.SignalBlob) error {
	n := h.running()
	if n == nil {
		return &pipe.CodeError{Code: api.ErrNotUp}
	}
	n.HandleSignal(peer, blob)
	return nil
}

func (h *host) Drop(peer string) error {
	n := h.running()
	if n == nil {
		return &pipe.CodeError{Code: api.ErrNotUp}
	}
	n.Drop(peer)
	h.mu.Lock()
	delete(h.peers, peer)
	h.mu.Unlock()
	return nil
}

func (h *host) SetICE(servers []api.ICEServer) error {
	n := h.running()
	if n == nil {
		return &pipe.CodeError{Code: api.ErrNotUp}
	}
	h.mu.Lock()
	h.up.ICEServers = servers
	h.mu.Unlock()
	n.SetICE(servers)
	return nil
}

func (h *host) SetPolicy(p api.Policy) error {
	n := h.running()
	if n == nil {
		return &pipe.CodeError{Code: api.ErrNotUp}
	}
	h.mu.Lock()
	h.up.Policy = p
	h.mu.Unlock()
	n.SetPolicy(p)
	return nil
}

// Readdress stores the salt and, when this user's LAN is running, restarts
// it on the new address: lan.Node has no way to change its address, so the
// node (and adapter) are rebuilt, and peers reconnect from the new Hello.
func (h *host) Readdress(sid string, salt uint32, running bool) error {
	if _, err := h.keys.SetSalt(sid, salt); err != nil {
		return &pipe.CodeError{Code: api.ErrAdapter, Detail: "store salt: " + err.Error()}
	}
	if !running {
		return nil
	}
	h.logf("readdress: salt %d, restarting LAN", salt)
	if err := h.stop(); err != nil {
		h.logf("readdress: stop: %v", err)
	}
	if err := h.start(); err != nil {
		h.out.Released()
		return err
	}
	return nil
}

func (h *host) Status() api.Status {
	h.mu.Lock()
	st := h.state
	n := h.n
	h.mu.Unlock()
	st.Version = h.version
	if n != nil {
		st.Peers = n.Peers()
	}
	return st
}

func (h *host) pushStatus() {
	if h.out == nil {
		return
	}
	st := h.Status()
	h.out.SendOwner(api.Message{Type: api.TypeStatus, Status: &st})
}

func (h *host) signal(gen uint64, peer string, blob api.SignalBlob) {
	h.mu.Lock()
	stale := gen != h.gen
	h.mu.Unlock()
	if stale || h.out == nil {
		return
	}
	m, err := pipe.MarshalBlob(peer, blob)
	if err != nil {
		h.logf("signal: %v", err)
		return
	}
	h.out.SendOwner(m)
}

// peersChanged sends a PeerState for every peer that is new or changed,
// then the full Status (which also shows peers that are gone).
func (h *host) peersChanged(gen uint64) {
	h.mu.Lock()
	n := h.n
	if gen != h.gen || n == nil {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	now := n.Peers()

	h.mu.Lock()
	if gen != h.gen {
		h.mu.Unlock()
		return
	}
	var changed []api.PeerState
	seen := map[string]bool{}
	for _, p := range now {
		seen[p.Peer] = true
		if old, ok := h.peers[p.Peer]; !ok || old != p {
			changed = append(changed, p)
		}
		h.peers[p.Peer] = p
	}
	for id := range h.peers {
		if !seen[id] {
			delete(h.peers, id)
		}
	}
	h.mu.Unlock()
	if h.out == nil {
		return
	}
	for i := range changed {
		h.out.SendOwner(api.Message{Type: api.TypePeerState, PeerState: &changed[i]})
	}
	h.pushStatus()
}
