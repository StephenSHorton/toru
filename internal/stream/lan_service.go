package stream

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/StephenSHorton/toru/internal/lan/client"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// lan_service.go is LanService, the Call page's LAN card: is the LAN
// service installed and current, the switch, and each member's tunnel. The
// service itself (toru-lan.exe, LocalSystem) owns the adapter and WireGuard;
// this side starts it (no UAC: its security descriptor lets interactive
// users start it), talks to it over the pipe (internal/lan/client) and
// carries its signaling through the room (lan.go). Installing, updating and
// removing it is "toru-lan.exe install|uninstall", run elevated: one UAC
// prompt. Design: docs/lan.md.

// EventLan carries LanState to the page whenever it changes.
const EventLan = "lan:state"

// LAN card states.
const (
	LanUnsupported = "unsupported" // not Windows
	LanSetup       = "setup"       // not installed: "LAN needs a one-time administrator setup"
	LanUpdate      = "update"      // the service is older than this Toru: "Update LAN (needs admin)"
	LanOff         = "off"
	LanStarting    = "starting"
	LanOn          = "on"
	LanFailed      = "error" // Error says why (api.Err* codes, or LanErr*)
)

// Error codes LanService adds to api's.
const (
	LanErrService = "service" // the service couldn't be started
	LanErrTimeout = "timeout" // the service started but LAN never came up
)

// lanStartWait is how long LAN may take to come up before the card says so.
var lanStartWait = 20 * time.Second

var (
	// lanHold is how long a member that timed out of presence keeps a
	// working tunnel before it is checked again (docs/lan.md: a broker
	// outage does not tear down tunnels).
	lanHold = 60 * time.Second
	// helloResendGap spaces the Hellos resent to one member that has no
	// working tunnel to us.
	helloResendGap = 20 * time.Second
)

// LanPeer is one member's tunnel as our LAN service sees it.
type LanPeer struct {
	IP    string `json:"ip"`
	State string `json:"state"`          // connecting | connected | failed
	Path  string `json:"path,omitempty"` // direct | relay
	RTTMs int    `json:"rttMs"`
}

// LanState is everything the LAN card and the member chips show.
type LanState struct {
	State          string             `json:"state"`  // LanSetup, LanOff, LanOn...
	Wanted         bool               `json:"wanted"` // the switch
	InRoom         bool               `json:"inRoom"`
	IP             string             `json:"ip"` // our overlay address, once the service has one
	PubKey         string             `json:"pubKey"`
	Error          string             `json:"error"` // with State LanFailed
	Detail         string             `json:"detail"`
	ServiceVersion string             `json:"serviceVersion"`
	AppVersion     string             `json:"appVersion"`
	Busy           string             `json:"busy"` // "install" or "uninstall" while the elevated helper runs
	Peers          map[string]LanPeer `json:"peers"`
	Relay          string             `json:"relay"` // RelayNone = "Direct only (no relay set up)"
	AllowServices  bool               `json:"allowServices"`
	MDNS           bool               `json:"mdns"`
}

// lanPipe is the pipe client as LanService uses it (*client.Client; a fake
// in tests).
type lanPipe interface {
	Start()
	Close()
	Up(api.Up) error
	Down() error
	SetICE([]api.ICEServer) error
	Signal(peer string, blob json.RawMessage) error
	Drop(peer string) error
	Readdress(salt uint32) error
	SetPolicy(api.Policy) error
	RequestStatus() error
}

// lanSvcInfo is the Windows service as the service manager reports it.
type lanSvcInfo struct {
	installed bool
	running   bool
	denied    bool // installed, but its security descriptor won't let us start it
}

// lanOS is the machine side: the service manager and the elevated helper.
type lanOS interface {
	supported() bool
	query() lanSvcInfo
	start() error
	runInstaller(verb string) error // "install" | "uninstall", elevated; waits
}

// errUACCancelled is the user saying no to the UAC prompt.
var errUACCancelled = errors.New("cancelled")

// LanService is the Wails-bound API behind the LAN card.
//
// JS binding name: LanService.*
type LanService struct {
	s          *StreamService
	appVersion string
	os         lanOS
	newPipe    func(client.Handlers) lanPipe

	mu          sync.Mutex
	app         *application.App
	pipe        lanPipe
	gen         int // bumps per pipe, so a closed pipe's late callbacks are ignored
	connected   bool
	hello       api.Hello
	status      api.Status
	peers       map[string]LanPeer
	info        lanSvcInfo
	needsUpdate bool
	wanted      bool
	room        string // current room hash ("" = none)
	upRoom      string // room the last Up was for ("" = down)
	errCode     string
	errDetail   string
	busy        string
	watchdog    *time.Timer

	// lastHello is the service's last room-wide Hello ("" peer), resent to
	// members that have no tunnel to us: a Hello lost to a broker outage,
	// or one a member dropped us before, is otherwise never sent again.
	lastHello    json.RawMessage
	helloPending bool                   // the last room-wide send failed (no broker)
	helloSent    map[string]time.Time   // last resend per member
	holds        map[string]*time.Timer // timed-out members whose tunnel still works
}

// NewLanService returns the LAN card's service for s; appVersion is this
// Toru's version, shown on the card (updates go by protocol, not version).
func NewLanService(s *StreamService, appVersion string) *LanService {
	l := newLanService(s, appVersion, defaultLanOS(), func(h client.Handlers) lanPipe { return client.New(h) })
	return l
}

func newLanService(s *StreamService, appVersion string, os lanOS, newPipe func(client.Handlers) lanPipe) *LanService {
	s.mu.Lock()
	wanted := s.settings.LanOn
	s.mu.Unlock()
	l := &LanService{
		s: s, appVersion: appVersion, os: os, newPipe: newPipe,
		peers: map[string]LanPeer{}, wanted: wanted, status: api.Status{State: api.StateOff},
		helloSent: map[string]time.Time{}, holds: map[string]*time.Timer{},
	}
	s.setLan(l)
	return l
}

// SetApp injects the running app (event emits).
//
//wails:ignore
func (l *LanService) SetApp(app *application.App) {
	l.mu.Lock()
	l.app = app
	l.mu.Unlock()
}

// Init checks the service and, if LAN was left on in a room, turns it back
// on. Call it after StreamService.Init (which rejoins the room).
//
//wails:ignore
func (l *LanService) Init() {
	l.refresh()
	room := l.s.roomTopicHash()
	l.mu.Lock()
	l.room = room
	l.mu.Unlock()
	l.bringUp()
	l.emit()
}

// Shutdown drops the pipe. Tunnels stay up in the service until it sees no
// GUI for a while (docs/lan.md), so a quick restart of Toru doesn't cut a game.
//
//wails:ignore
func (l *LanService) Shutdown() {
	l.mu.Lock()
	p := l.pipe
	l.pipe, l.connected = nil, false
	l.gen++
	l.stopWatchdogLocked()
	l.mu.Unlock()
	if p != nil {
		p.Close()
	}
}

// --- bound API ----------------------------------------------------------------

// GetState returns the LAN card's state, checking the service first.
func (l *LanService) GetState() LanState {
	l.refresh()
	return l.snapshot()
}

// Enable turns LAN on for the current room: starts the service (no UAC) and
// brings the adapter up. It returns at once; progress arrives on lan:state.
func (l *LanService) Enable() (LanState, error) {
	l.refresh()
	l.mu.Lock()
	room, installed, update := l.room, l.info.installed, l.updateLocked()
	l.mu.Unlock()
	switch {
	case !l.os.supported():
		return l.snapshot(), errors.New("LAN needs Windows")
	case room == "":
		return l.snapshot(), errors.New("join a room first: LAN connects the people in it")
	case !installed:
		return l.snapshot(), errors.New("LAN isn't set up yet")
	case update:
		return l.snapshot(), errors.New("LAN needs an update first")
	}
	l.setWanted(true)
	go l.bringUp()
	l.emit()
	return l.snapshot(), nil
}

// Disable turns LAN off: the adapter goes down and LAN games stop seeing
// the room. The service stays installed.
func (l *LanService) Disable() LanState {
	l.setWanted(false)
	l.takeDown(true)
	l.emit()
	return l.snapshot()
}

// SetPolicy sets the Advanced toggles: Windows file sharing and remote
// desktop between members, and mDNS/SSDP discovery.
func (l *LanService) SetPolicy(allowServices, mdns bool) LanState {
	l.s.mu.Lock()
	l.s.settings.LanAllowServices, l.s.settings.LanMDNS = allowServices, mdns
	settings := l.s.settings
	l.s.mu.Unlock()
	if err := saveSettings(settings); err != nil {
		logf("save settings: %v", err)
	}
	l.mu.Lock()
	p, ok := l.pipe, l.connected
	l.mu.Unlock()
	if ok {
		_ = p.SetPolicy(api.Policy{AllowServices: allowServices, MDNS: mdns})
	}
	l.emit()
	return l.snapshot()
}

// Setup installs the LAN service, or updates it: one UAC prompt for
// "toru-lan.exe install". A first setup turns LAN on when in a room.
func (l *LanService) Setup() (LanState, error) {
	l.refresh()
	l.mu.Lock()
	first := !l.info.installed
	l.mu.Unlock()
	if err := l.runInstaller("install"); err != nil {
		return l.snapshot(), err
	}
	l.mu.Lock()
	l.needsUpdate = false // the new service's Hello checks again
	l.errCode, l.errDetail = "", ""
	inRoom := l.room != ""
	l.mu.Unlock()
	l.refresh()
	if first && inRoom {
		return l.Enable()
	}
	go l.bringUp() // an update: the pipe redials the new service by itself
	l.emit()
	return l.snapshot(), nil
}

// Uninstall turns LAN off and removes the service, the adapter and the
// firewall rules (one UAC prompt for "toru-lan.exe uninstall").
func (l *LanService) Uninstall() (LanState, error) {
	l.setWanted(false)
	l.takeDown(true)
	err := l.runInstaller("uninstall")
	l.refresh()
	l.emit()
	return l.snapshot(), err
}

// --- lanLink (from the room) --------------------------------------------------

func (l *LanService) signal(peer string, blob json.RawMessage) {
	l.mu.Lock()
	p, ok := l.pipe, l.connected && l.upRoom != ""
	l.mu.Unlock()
	if ok {
		_ = p.Signal(peer, blob)
	}
}

func (l *LanService) drop(peer string) {
	l.mu.Lock()
	p, ok := l.pipe, l.connected
	_, had := l.peers[peer]
	delete(l.peers, peer)
	delete(l.helloSent, peer)
	if h := l.holds[peer]; h != nil {
		h.Stop()
		delete(l.holds, peer)
	}
	l.mu.Unlock()
	if ok {
		_ = p.Drop(peer)
	}
	if had {
		l.emit()
	}
}

// expired: peer timed out of presence. A working tunnel stays (the game
// keeps running through a broker outage); it is dropped once the member has
// been gone lanHold and its tunnel is down, or when it leaves.
func (l *LanService) expired(peer string) {
	l.mu.Lock()
	ps, have := l.peers[peer]
	if !have || ps.State != api.PeerConnected || !l.connected {
		l.mu.Unlock()
		l.drop(peer)
		return
	}
	if l.holds[peer] == nil {
		l.holds[peer] = time.AfterFunc(lanHold, func() {
			l.mu.Lock()
			delete(l.holds, peer)
			l.mu.Unlock()
			if !l.s.isMember(peer) {
				l.expired(peer) // still gone: drop, or hold again if it still works
			}
		})
	}
	l.mu.Unlock()
}

// memberSeen: presence from peer. A member with LAN on and no working
// tunnel to us gets our Hello again (fresh: at once; otherwise at most every
// helloResendGap): it may have dropped us while we were away, or our Hello
// may have been lost to a broker outage.
func (l *LanService) memberSeen(peer string, lanOn, fresh bool) {
	l.mu.Lock()
	if h := l.holds[peer]; h != nil {
		h.Stop()
		delete(l.holds, peer)
	}
	blob := l.lastHello
	ps, have := l.peers[peer]
	send := len(blob) > 0 && lanOn && l.connected && l.upRoom != "" && (!have || ps.State != api.PeerConnected)
	if send && !fresh {
		if at, ok := l.helloSent[peer]; ok && time.Since(at) < helloResendGap {
			send = false
		}
	}
	if send {
		l.helloSent[peer] = time.Now()
	}
	l.mu.Unlock()
	if send {
		if err := l.s.lanSend(peer, blob); err != nil {
			logf("LAN hello to %s: %v", peer, err)
		}
	}
}

// roomConnected: a broker (re)connected. A room-wide Hello that found no
// broker goes out now.
func (l *LanService) roomConnected() {
	l.mu.Lock()
	blob, pending := l.lastHello, l.helloPending
	ok := pending && len(blob) > 0 && l.connected && l.upRoom != ""
	l.mu.Unlock()
	if !ok {
		return
	}
	if err := l.s.lanSend("", blob); err == nil {
		l.mu.Lock()
		if string(l.lastHello) == string(blob) {
			l.helloPending = false
		}
		l.mu.Unlock()
	}
}

// forgetHelloLocked clears the resend state when the service's node goes.
func (l *LanService) forgetHelloLocked() {
	l.lastHello, l.helloPending = nil, false
	l.helloSent = map[string]time.Time{}
	for id, h := range l.holds {
		h.Stop()
		delete(l.holds, id)
	}
}

func (l *LanService) readdress(salt uint32) {
	l.mu.Lock()
	p, ok := l.pipe, l.connected
	l.mu.Unlock()
	if ok {
		_ = p.Readdress(salt)
	}
}

func (l *LanService) roomChanged(room string) {
	l.mu.Lock()
	l.room = room
	l.mu.Unlock()
	if room == "" {
		l.takeDown(false) // keep the pipe and the switch: the next room brings it back
	} else {
		go l.bringUp()
	}
	l.emit()
}

func (l *LanService) iceChanged() {
	l.mu.Lock()
	p, ok := l.pipe, l.connected && l.upRoom != ""
	l.mu.Unlock()
	if ok {
		_ = p.SetICE(pipeICE(l.s.ICEServers()))
	}
	l.emit() // the relay note
}

// --- internals ----------------------------------------------------------------

func (l *LanService) setWanted(on bool) {
	l.mu.Lock()
	l.wanted = on
	if on {
		l.errCode, l.errDetail = "", ""
	}
	l.mu.Unlock()
	l.s.mu.Lock()
	l.s.settings.LanOn = on
	settings := l.s.settings
	l.s.mu.Unlock()
	if err := saveSettings(settings); err != nil {
		logf("save settings: %v", err)
	}
}

func (l *LanService) refresh() {
	if !l.os.supported() {
		return
	}
	info := l.os.query()
	l.mu.Lock()
	l.info = info
	l.mu.Unlock()
}

// updateLocked: the installed service must be replaced first.
func (l *LanService) updateLocked() bool {
	return l.info.installed && (l.needsUpdate || l.info.denied)
}

// bringUp starts the service and the pipe when LAN is wanted in a room;
// with the pipe already up it re-sends Up (a new room). Idempotent.
func (l *LanService) bringUp() {
	l.mu.Lock()
	if !l.wanted || l.room == "" || !l.info.installed || l.updateLocked() || l.busy != "" {
		l.mu.Unlock()
		return
	}
	p, connected := l.pipe, l.connected
	fresh := p == nil
	if fresh {
		l.gen++
		p = l.newPipe(l.handlers(l.gen))
		l.pipe = p
	}
	if !connected {
		l.armWatchdogLocked()
	}
	l.mu.Unlock()
	if fresh {
		if err := l.os.start(); err != nil {
			l.mu.Lock()
			if l.pipe == p {
				l.pipe = nil // the next try builds a fresh one
				l.gen++
			}
			l.mu.Unlock()
			l.fail(LanErrService, err.Error())
			return
		}
		p.Start()
	}
	if connected {
		l.sendUp()
	}
	l.emit()
}

// sendUp sends Up for the current room, if connected and wanted.
func (l *LanService) sendUp() {
	self := l.s.self
	ice := pipeICE(l.s.ICEServers())
	l.s.mu.Lock()
	policy := api.Policy{AllowServices: l.s.settings.LanAllowServices, MDNS: l.s.settings.LanMDNS}
	l.s.mu.Unlock()
	l.mu.Lock()
	if !l.connected || !l.wanted || l.room == "" || l.updateLocked() || l.upRoom == l.room {
		l.mu.Unlock()
		return
	}
	p, room := l.pipe, l.room
	l.upRoom = room
	l.mu.Unlock()
	if err := p.Up(api.Up{Room: room, Self: self, ICEServers: ice, Policy: policy}); err != nil {
		l.mu.Lock()
		l.upRoom = ""
		l.mu.Unlock()
	}
}

// takeDown sends Down if LAN is up; closePipe also drops the pipe.
func (l *LanService) takeDown(closePipe bool) {
	l.mu.Lock()
	p, wasUp := l.pipe, l.connected && (l.upRoom != "" || serviceUp(l.status))
	l.upRoom = ""
	l.status = api.Status{State: api.StateOff}
	l.peers = map[string]LanPeer{}
	l.errCode, l.errDetail = "", ""
	l.forgetHelloLocked()
	l.stopWatchdogLocked()
	if closePipe {
		l.pipe, l.connected = nil, false
		l.gen++
	}
	ip, pub := l.hello.IP, l.hello.PubKey
	l.mu.Unlock()
	if p != nil && wasUp {
		_ = p.Down()
	}
	if p != nil && closePipe {
		p.Close()
	}
	l.s.setLanInfo(lanInfo(ip, pub, false))
}

func (l *LanService) fail(code, detail string) {
	l.mu.Lock()
	l.errCode, l.errDetail = code, detail
	l.stopWatchdogLocked()
	l.mu.Unlock()
	logf("LAN: %s: %s", code, detail)
	l.emit()
}

func (l *LanService) armWatchdogLocked() {
	l.stopWatchdogLocked()
	gen := l.gen
	l.watchdog = time.AfterFunc(lanStartWait, func() {
		l.mu.Lock()
		stuck := l.gen == gen && l.wanted && l.status.State != api.StateOn && l.errCode == ""
		l.mu.Unlock()
		if stuck {
			l.fail(LanErrTimeout, "the LAN service didn't come up")
		}
	})
}

func (l *LanService) stopWatchdogLocked() {
	if l.watchdog != nil {
		l.watchdog.Stop()
		l.watchdog = nil
	}
}

func (l *LanService) runInstaller(verb string) error {
	l.mu.Lock()
	if l.busy != "" {
		l.mu.Unlock()
		return errors.New("LAN setup is already running")
	}
	l.busy = verb
	l.mu.Unlock()
	l.emit()
	err := l.os.runInstaller(verb)
	l.mu.Lock()
	l.busy = ""
	l.mu.Unlock()
	l.emit()
	switch {
	case errors.Is(err, errUACCancelled) && verb == "install":
		return errors.New("setup was cancelled: LAN needs administrator permission once")
	case errors.Is(err, errUACCancelled):
		return errors.New("removal was cancelled, so LAN is still installed")
	}
	return err
}

// handlers are the pipe callbacks for pipe generation gen.
func (l *LanService) handlers(gen int) client.Handlers {
	current := func() bool { return l.gen == gen } // l.mu held
	return client.Handlers{
		Connected: func(h api.Hello) {
			l.mu.Lock()
			if !current() {
				l.mu.Unlock()
				return
			}
			l.connected, l.hello = true, h
			l.needsUpdate = l.olderService(h)
			on := l.status.State == api.StateOn
			p := l.pipe
			l.mu.Unlock()
			l.s.setLanInfo(lanInfo(h.IP, h.PubKey, on))
			l.sendUp()
			_ = p.RequestStatus()
			l.emit()
		},
		Disconnected: func(err error) {
			var inc *client.IncompatibleError
			l.mu.Lock()
			if !current() {
				l.mu.Unlock()
				return
			}
			l.connected, l.upRoom = false, ""
			l.status = api.Status{State: api.StateOff}
			l.peers = map[string]LanPeer{}
			l.forgetHelloLocked()
			if errors.As(err, &inc) {
				l.hello, l.needsUpdate = inc.Hello, true
			}
			// The service stopped under a live pipe (stopped from
			// services.msc, an idle stop racing Enable, a crash): the pipe
			// redials, but nothing would start the service again.
			restart := l.wanted && l.room != "" && l.info.installed && !l.updateLocked() && l.busy == ""
			if restart {
				l.armWatchdogLocked()
			}
			ip, pub := l.hello.IP, l.hello.PubKey
			l.mu.Unlock()
			l.s.setLanInfo(lanInfo(ip, pub, false))
			if restart {
				if err := l.os.start(); err != nil {
					l.mu.Lock()
					ok := current()
					l.mu.Unlock()
					if ok {
						l.fail(LanErrService, err.Error())
					}
				}
			}
			l.emit()
		},
		Status: func(st api.Status) {
			l.mu.Lock()
			if !current() {
				l.mu.Unlock()
				return
			}
			l.status = st
			l.peers = map[string]LanPeer{}
			for _, ps := range st.Peers {
				l.peers[ps.Peer] = lanPeer(ps)
			}
			switch st.State {
			case api.StateError:
				l.errCode, l.errDetail = st.Error, st.Detail
				l.stopWatchdogLocked()
			case api.StateOn:
				l.errCode, l.errDetail = "", ""
				l.stopWatchdogLocked()
			}
			if st.IP != "" {
				l.hello.IP = st.IP
			}
			if st.PubKey != "" {
				l.hello.PubKey = st.PubKey
			}
			// An older service still up (left up by the Toru before an
			// update) keeps the adapter's metric and broadcast routes while
			// the card waits for Update LAN: take it down.
			down := l.updateLocked() && serviceUp(st)
			p := l.pipe
			ip, pub, on := l.hello.IP, l.hello.PubKey, st.State == api.StateOn && l.wanted && !down
			l.mu.Unlock()
			if down && p != nil {
				logf("LAN: the service needs an update; taking LAN down")
				_ = p.Down()
			}
			l.s.setLanInfo(lanInfo(ip, pub, on))
			l.emit()
		},
		PeerState: func(ps api.PeerState) {
			l.mu.Lock()
			if !current() || ps.Peer == "" {
				l.mu.Unlock()
				return
			}
			l.peers[ps.Peer] = lanPeer(ps)
			l.mu.Unlock()
			l.emit()
		},
		Error: func(e api.Error) {
			switch e.Code {
			case api.ErrRangeInUse, api.ErrAdapter, api.ErrWrongSession, api.ErrProtocol:
				l.mu.Lock()
				ok := current()
				l.mu.Unlock()
				if ok {
					l.fail(e.Code, e.Detail)
				}
			default:
				logf("LAN service: %s %s", e.Code, e.Detail)
			}
		},
		Signal: func(sig api.Signal) {
			l.mu.Lock()
			ok := current() && l.wanted
			l.mu.Unlock()
			if !ok {
				return
			}
			err := l.s.lanSend(sig.Peer, sig.Blob)
			if sig.Peer == "" {
				// The service sends only its Hello room-wide (on Up and after
				// a Readdress): keep it for members that miss it.
				l.mu.Lock()
				if current() {
					l.lastHello = append(json.RawMessage(nil), sig.Blob...)
					l.helloPending = err != nil
					l.helloSent = map[string]time.Time{}
				}
				l.mu.Unlock()
			}
			if err != nil {
				logf("LAN signal to %s: %v", sig.Peer, err)
			}
		},
	}
}

// olderService reports whether the service in h must be updated for this
// Toru: another major protocol or an older minor. The app version is not
// compared: every release builds both binaries with the same version, so
// that would put LAN behind a UAC prompt after every Toru update. The minor
// is bumped whenever the service must be replaced (api.ProtocolMinor).
func (l *LanService) olderService(h api.Hello) bool {
	return h.Major != api.ProtocolMajor || h.Minor < api.ProtocolMinor
}

// serviceUp: the service reports LAN up or coming up.
func serviceUp(st api.Status) bool {
	return st.State == api.StateOn || st.State == api.StateStarting
}

func lanPeer(ps api.PeerState) LanPeer {
	return LanPeer{IP: ps.IP, State: ps.State, Path: ps.Path, RTTMs: ps.RTTMs}
}

func lanInfo(ip, pub string, on bool) *LanInfo {
	if ip == "" && pub == "" {
		return nil
	}
	return &LanInfo{IP: ip, WGPub: pub, On: on}
}

// snapshot builds LanState without asking the service manager.
func (l *LanService) snapshot() LanState {
	relay := l.s.relay.Status().State
	l.s.mu.Lock()
	allow, mdns := l.s.settings.LanAllowServices, l.s.settings.LanMDNS
	l.s.mu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	st := LanState{
		Wanted: l.wanted, InRoom: l.room != "", IP: l.hello.IP, PubKey: l.hello.PubKey,
		ServiceVersion: l.hello.Version, AppVersion: l.appVersion, Busy: l.busy,
		Peers: map[string]LanPeer{}, Relay: relay, AllowServices: allow, MDNS: mdns,
	}
	for id, p := range l.peers {
		st.Peers[id] = p
	}
	switch {
	case !l.os.supported():
		st.State = LanUnsupported
	case !l.info.installed:
		st.State = LanSetup
	case l.updateLocked():
		st.State = LanUpdate
	case !l.wanted || l.room == "":
		st.State = LanOff
	case l.errCode != "":
		st.State, st.Error, st.Detail = LanFailed, l.errCode, l.errDetail
	case l.status.State == api.StateOn:
		st.State = LanOn
	default:
		st.State = LanStarting
	}
	return st
}

func (l *LanService) emit() {
	l.mu.Lock()
	app := l.app
	l.mu.Unlock()
	if app != nil {
		app.Event.Emit(EventLan, l.snapshot())
	}
}
