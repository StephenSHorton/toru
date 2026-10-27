package lan

import (
	"net"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/pion/ice/v4"
	"github.com/pion/stun/v4"
)

// agent.go runs one peer's ICE: a pion/ice v4 Agent per attempt, trickled
// through the room as Signal blobs, and the make-before-break upgrade from a
// relayed path to a direct one.
//
// Each peer has an actor goroutine (peer.run) that owns its sessions; agent
// callbacks and timers only post events to it, so no lock is held across a
// pion call. The side with the smaller WireGuard public key is controlling:
// it starts every session (it alone allocates Gen), restarts after failures
// and tries the upgrades. The controlled side answers whatever newer Gen it
// is offered. Agent.Restart is never used: in v4.4.8 it drops the selected
// pair and goes back to Checking, which stalls a working link.

// ICE timings, the same as broadcast.go.
const (
	iceDisconnected = 5 * time.Second
	iceFailed       = 15 * time.Second
	iceKeepalive    = 2 * time.Second
	// connectTimeout gives up on a session that never connects (no candidate
	// pair ever worked, so ICE may never report Failed).
	connectTimeout = 30 * time.Second

	backoffMin        = time.Second
	backoffMax        = 30 * time.Second
	upgradeBackoffMax = 5 * time.Minute

	earlyMax = 64 // signal blobs kept for a Gen that hasn't started yet

	// kickDelay is how long the controlled side waits before turning its
	// WireGuard keepalive on, leaving the first handshake to the controller.
	kickDelay = 2 * time.Second
)

var (
	// upgradeBackoffMin is the first wait before trying relay → direct; a
	// var so the upgrade test doesn't sit through it.
	upgradeBackoffMin = 30 * time.Second

	// testAgentOptions, if set, adds to every agent's options; tests use it
	// to keep ICE on loopback.
	testAgentOptions func(direct bool) []ice.AgentOption
)

// session is one ICE agent and its connection.
type session struct {
	gen       uint32
	direct    bool // the relay-less upgrade agent
	agent     *ice.Agent
	conn      *ice.Conn
	started   bool // remote credentials applied, checks running
	connected bool
	path      string
	dead      bool // closed; late events are ignored (actor-owned)
	deadline  *time.Timer
}

// Events the actor handles.
type (
	evSignal struct{ blob api.SignalBlob }
	evHello  struct{ boot uint32 }
	evState  struct {
		s  *session
		st ice.ConnectionState
	}
	evPair struct {
		s    *session
		path string
	}
	evTimeout struct{ s *session }
	evRestart struct{}
	evUpgrade struct{}
	evKick    struct{}
)

func (p *peer) post(ev any) {
	select {
	case p.events <- ev:
	case <-p.done:
	}
}

// run is the peer's actor. It returns after stop, with every agent closed.
func (p *peer) run() {
	defer close(p.stopped)
	p.backoff, p.upBackoff = backoffMin, upgradeBackoffMin
	if p.controlling {
		p.startMain()
	}
	for {
		select {
		case ev := <-p.events:
			p.handle(ev)
		case <-p.done:
			p.closeSession(p.main)
			p.closeSession(p.up)
			p.main, p.up = nil, nil
			stopTimer(p.restartT)
			stopTimer(p.upgradeT)
			return
		}
	}
}

func (p *peer) handle(ev any) {
	switch ev := ev.(type) {
	case evSignal:
		p.onSignal(ev.blob)
	case evHello:
		p.onHello(ev.boot)
	case evState:
		p.onState(ev.s, ev.st)
	case evPair:
		p.onPair(ev.s, ev.path)
	case evTimeout:
		if !ev.s.dead && !ev.s.connected {
			p.n.logf("lan: %s: gen %d never connected", p.id, ev.s.gen)
			p.fail(ev.s)
		}
	case evRestart:
		p.restartT = nil
		if p.controlling && p.main == nil {
			p.startMain()
		}
	case evKick:
		if p.main != nil && p.main.connected {
			p.n.kick(p.pub, false)
		}
	case evUpgrade:
		p.upgradeT = nil
		if p.controlling && p.up == nil && p.main != nil && p.main.connected && p.main.path == api.PathRelay {
			if p.up = p.newSession(p.n.nextGen(), true); p.up == nil {
				p.scheduleUpgrade()
			}
		}
	}
}

// onHello is a repeat Hello from a peer we know (same key). A new boot means
// the peer restarted and its old sessions are gone: start over now instead of
// waiting for ICE to time out.
func (p *peer) onHello(boot uint32) {
	if boot == p.boot {
		return
	}
	p.boot = boot
	p.n.logf("lan: %s restarted; new ICE session", p.id)
	p.closeSession(p.main)
	p.closeSession(p.up)
	p.main, p.up = nil, nil
	p.lastRemoteGen = 0
	p.n.bind.setActive(p.ep, nil)
	p.publish(api.PeerConnecting, "")
	p.backoff = backoffMin
	if p.controlling {
		stopTimer(p.restartT)
		p.restartT = nil
		p.startMain()
	}
}

func (p *peer) startMain() {
	p.main = p.newSession(p.n.nextGen(), false)
	if p.main == nil {
		p.scheduleRestart()
		return
	}
	p.publish(api.PeerConnecting, "")
}

// newSession builds an agent for gen, announces its credentials and starts
// trickling candidates. The controller calls it first; the controlled side
// calls it on the controller's credentials and then start()s straight away.
func (p *peer) newSession(gen uint32, direct bool) *session {
	a, err := ice.NewAgentWithOptions(p.n.agentOptions(direct)...)
	if err != nil {
		p.n.logf("lan: %s: new ICE agent: %v", p.id, err)
		return nil
	}
	s := &session{gen: gen, direct: direct, agent: a}
	_ = a.OnConnectionStateChange(func(st ice.ConnectionState) { p.post(evState{s, st}) })
	_ = a.OnSelectedCandidatePairChange(func(local, remote ice.Candidate) {
		path := api.PathDirect
		if local.Type() == ice.CandidateTypeRelay || remote.Type() == ice.CandidateTypeRelay {
			path = api.PathRelay
		}
		p.post(evPair{s, path})
	})
	// Candidates go straight out from pion's handler goroutine, in order,
	// after the creds blob below (sent before gathering starts).
	_ = a.OnCandidate(func(c ice.Candidate) {
		b := api.SignalBlob{Kind: api.KindCand, Gen: gen, Direct: direct}
		if c != nil {
			b.Cand = c.Marshal()
		}
		p.n.signal(p.id, b)
	})
	ufrag, pwd, err := a.GetLocalUserCredentials()
	if err != nil {
		p.n.logf("lan: %s: ICE credentials: %v", p.id, err)
		go func() { _ = a.Close() }()
		return nil
	}
	p.n.signal(p.id, api.SignalBlob{Kind: api.KindCreds, Gen: gen, Ufrag: ufrag, Pwd: pwd, Direct: direct})
	if err := a.GatherCandidates(); err != nil {
		p.n.logf("lan: %s: gather: %v", p.id, err)
		go func() { _ = a.Close() }()
		return nil
	}
	s.deadline = time.AfterFunc(connectTimeout, func() { p.post(evTimeout{s}) })
	// Apply what arrived for this Gen before we had an agent for it.
	early := p.early[:0]
	for _, b := range p.early {
		switch {
		case b.Gen == gen && b.Direct == direct:
			p.apply(s, b)
		case b.Gen > gen:
			early = append(early, b)
		}
	}
	p.early = early
	return s
}

// start applies the remote credentials and begins connectivity checks.
func (p *peer) start(s *session, ufrag, pwd string) {
	if s.started || ufrag == "" || pwd == "" {
		return
	}
	var c *ice.Conn
	var err error
	if p.controlling {
		c, err = s.agent.StartDial(ufrag, pwd)
	} else {
		c, err = s.agent.StartAccept(ufrag, pwd)
	}
	if err != nil {
		p.n.logf("lan: %s: start ICE gen %d: %v", p.id, s.gen, err)
		p.fail(s)
		return
	}
	s.started, s.conn = true, c
	p.n.bind.attach(p.ep, c)
	if s == p.main {
		// Usable before this side reports Connected (ice.Conn writes on the
		// best valid pair until one is selected), so a handshake that comes
		// in early gets its response instead of a 5 s retry.
		p.n.bind.setActive(p.ep, c)
	}
}

// apply feeds one creds or cand blob to the session it belongs to.
func (p *peer) apply(s *session, b api.SignalBlob) {
	switch b.Kind {
	case api.KindCreds:
		p.start(s, b.Ufrag, b.Pwd)
	case api.KindCand:
		if b.Cand == "" {
			return // end of candidates; ICE needs no notice
		}
		c, err := ice.UnmarshalCandidate(b.Cand)
		if err != nil {
			p.n.logf("lan: %s: bad candidate: %v", p.id, err)
			return
		}
		if err := s.agent.AddRemoteCandidate(c); err != nil {
			p.n.logf("lan: %s: add candidate: %v", p.id, err)
		}
	}
}

func (p *peer) sessionFor(gen uint32, direct bool) *session {
	for _, s := range []*session{p.main, p.up} {
		if s != nil && s.gen == gen && s.direct == direct {
			return s
		}
	}
	return nil
}

func (p *peer) onSignal(b api.SignalBlob) {
	if s := p.sessionFor(b.Gen, b.Direct); s != nil {
		p.apply(s, b)
		return
	}
	if b.Kind == api.KindCreds && !p.controlling && b.Gen > p.lastRemoteGen {
		// The controller started a new session: answer it.
		p.lastRemoteGen = b.Gen
		if b.Direct {
			p.closeSession(p.up)
			p.up = p.newSession(b.Gen, true)
			if p.up != nil {
				p.start(p.up, b.Ufrag, b.Pwd)
			}
			return
		}
		p.closeSession(p.main)
		p.closeSession(p.up)
		p.main, p.up = nil, nil
		p.n.bind.setActive(p.ep, nil)
		p.publish(api.PeerConnecting, "")
		p.main = p.newSession(b.Gen, false)
		if p.main != nil {
			p.start(p.main, b.Ufrag, b.Pwd)
		}
		return
	}
	// A blob for a Gen we haven't started yet (trickle can overtake creds):
	// keep it. Anything older is stale.
	if b.Gen > p.highestGen() {
		if len(p.early) >= earlyMax {
			p.early = p.early[1:]
		}
		p.early = append(p.early, b)
	}
}

func (p *peer) highestGen() uint32 {
	g := p.lastRemoteGen
	for _, s := range []*session{p.main, p.up} {
		if s != nil && s.gen > g {
			g = s.gen
		}
	}
	return g
}

func (p *peer) onState(s *session, st ice.ConnectionState) {
	if s.dead {
		return
	}
	switch st {
	case ice.ConnectionStateConnected, ice.ConnectionStateCompleted:
		if s.connected {
			// Back from Disconnected on the same pair: pion fires no pair
			// change, so this is the only place the peer turns connected
			// again (and broadcast fan-out to it resumes). publish
			// coalesces a repeat.
			if s == p.main {
				p.publish(api.PeerConnected, s.path)
			}
			return
		}
		s.connected = true
		stopTimer(s.deadline)
		switch s {
		case p.main:
			p.n.bind.setActive(p.ep, s.conn)
			p.backoff = backoffMin
			p.publish(api.PeerConnected, s.path)
			if p.controlling {
				p.n.kick(p.pub, true)
			} else {
				// If both sides initiate at once, each initiation wipes the
				// other's handshake state and both wait out a 5 s retry. The
				// controller goes first; we only turn the keepalive on, late.
				time.AfterFunc(kickDelay, func() { p.post(evKick{}) })
			}
			if p.controlling && s.path == api.PathRelay {
				p.scheduleUpgrade()
			}
		case p.up:
			p.promote()
		}
	case ice.ConnectionStateDisconnected:
		if s == p.main {
			p.publish(api.PeerConnecting, s.path)
		}
	case ice.ConnectionStateFailed:
		p.fail(s)
	}
}

func (p *peer) onPair(s *session, path string) {
	if s.dead {
		return
	}
	s.path = path
	if s != p.main {
		return
	}
	if s.connected {
		p.publish(api.PeerConnected, path)
	}
	if !p.controlling || !s.connected {
		return
	}
	if path == api.PathRelay {
		p.scheduleUpgrade()
	} else {
		stopTimer(p.upgradeT)
		p.upgradeT = nil
		p.closeSession(p.up)
		p.up = nil
	}
}

// promote makes the upgrade session the main one: point the endpoint at the
// direct conn first, then close the relayed agent (make-before-break).
func (p *peer) promote() {
	old := p.main
	p.main, p.up = p.up, nil
	p.n.bind.setActive(p.ep, p.main.conn)
	p.closeSession(old)
	if p.main.path == "" {
		p.main.path = api.PathDirect
	}
	p.upBackoff = upgradeBackoffMin
	p.n.logf("lan: %s: upgraded to a direct path (gen %d)", p.id, p.main.gen)
	p.publish(api.PeerConnected, p.main.path)
}

// fail ends s. A failed main session takes the link down; the controller
// retries with backoff. A failed upgrade just waits for the next try.
func (p *peer) fail(s *session) {
	p.closeSession(s)
	switch s {
	case p.main:
		p.closeSession(p.up)
		p.main, p.up = nil, nil
		p.n.bind.setActive(p.ep, nil)
		p.publish(api.PeerFailed, "")
		if p.controlling {
			p.scheduleRestart()
		}
	case p.up:
		p.up = nil
		if p.controlling {
			p.scheduleUpgrade()
		}
	}
}

func (p *peer) scheduleRestart() {
	if p.restartT != nil {
		return
	}
	d := p.backoff
	p.backoff = min(p.backoff*2, backoffMax)
	p.restartT = time.AfterFunc(d, func() { p.post(evRestart{}) })
}

func (p *peer) scheduleUpgrade() {
	if p.upgradeT != nil || p.up != nil {
		return
	}
	d := p.upBackoff
	p.upBackoff = min(p.upBackoff*2, upgradeBackoffMax)
	p.upgradeT = time.AfterFunc(d, func() { p.post(evUpgrade{}) })
}

// closeSession marks s dead and closes its agent off the actor: Close waits
// for pion's handler goroutines, which may be blocked posting to us.
func (p *peer) closeSession(s *session) {
	if s == nil || s.dead {
		return
	}
	s.dead = true
	stopTimer(s.deadline)
	go func() { _ = s.agent.Close() }()
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

// agentOptions builds one agent's configuration from the current ICE list.
// The overlay itself is never a candidate (that would tunnel the tunnel
// through itself), and mDNS is off: there is no browser here, and its
// multicast socket would be one more listener on a real interface.
func (n *Node) agentOptions(direct bool) []ice.AgentOption {
	n.mu.Lock()
	servers := n.ice
	n.mu.Unlock()

	notOverlay := func(ip net.IP) bool {
		a, ok := netipFromIP(ip)
		return !ok || !overlay.Contains(a)
	}
	opts := []ice.AgentOption{
		ice.WithUrls(iceURLs(servers, direct, n.logf)),
		ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6}),
		ice.WithMulticastDNSMode(ice.MulticastDNSModeDisabled),
		ice.WithDisconnectedTimeout(iceDisconnected),
		ice.WithFailedTimeout(iceFailed),
		ice.WithKeepaliveInterval(iceKeepalive),
		ice.WithInterfaceFilter(func(name string) bool { return name != api.AdapterName }),
		ice.WithIPFilter(notOverlay),
		ice.WithRemoteIPFilter(notOverlay),
	}
	if direct {
		opts = append(opts, ice.WithCandidateTypes([]ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive}))
	}
	if testAgentOptions != nil {
		opts = append(opts, testAgentOptions(direct)...)
	}
	return opts
}

// iceURLs converts the GUI's server list to pion URIs, with the TURN REST
// credentials on the TURN ones. direct leaves TURN out.
func iceURLs(servers []api.ICEServer, direct bool, logf func(string, ...any)) []*stun.URI {
	var out []*stun.URI
	for _, s := range servers {
		for _, raw := range s.URLs {
			u, err := stun.ParseURI(raw)
			if err != nil {
				logf("lan: ignoring ICE server %q: %v", raw, err)
				continue
			}
			if u.Scheme == stun.SchemeTypeTURN || u.Scheme == stun.SchemeTypeTURNS {
				if direct {
					continue
				}
				u.Username, u.Password = s.Username, s.Credential
			}
			out = append(out, u)
		}
	}
	return out
}
