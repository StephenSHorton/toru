package stream

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/StephenSHorton/toru/internal/lan/client"
)

// These tests open no listeners except httptest's, which binds 127.0.0.1,
// and no rooms (no MQTT): everything goes through onMessage directly.

// newTestService is a StreamService on a throwaway %AppData%.
func newTestService(t *testing.T) *StreamService {
	t.Helper()
	t.Setenv("APPDATA", t.TempDir())
	return NewService()
}

func readStreamJSON(t *testing.T) map[string]any {
	t.Helper()
	path, err := settingsPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInstallIDMigration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	// A stream.json from before InstallID.
	if err := os.MkdirAll(filepath.Join(dir, "toru"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := `{"name":"Stephen","roomCode":"","fps":120,"noiseMode":"ai"}`
	if err := os.WriteFile(filepath.Join(dir, "toru", "stream.json"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewService()
	if !validInstallID(s.self) {
		t.Fatalf("self %q isn't an InstallID", s.self)
	}
	got := readStreamJSON(t)
	if got["installId"] != s.self || got["name"] != "Stephen" || got["fps"] != float64(120) || got["noiseMode"] != "ai" {
		t.Fatalf("migrated file %v", got)
	}
	if again := NewService(); again.self != s.self {
		t.Fatalf("second start got id %q, first %q", again.self, s.self)
	}

	// A page save carries no InstallID, volumes or relay setup; they stay.
	if _, err := s.SetPeerVolume("friend", 150); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.settings.CredentialURL = "https://relay.example/cred"
	s.mu.Unlock()
	in := s.GetSettings()
	in.InstallID, in.PeerVolumes, in.CredentialURL, in.LanOn = "", nil, "", true
	in.Name = "Steve"
	saved, err := s.SetSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	if saved.InstallID != s.self || saved.PeerVolumes["friend"] != 150 || saved.CredentialURL == "" || saved.LanOn || saved.Name != "Steve" {
		t.Fatalf("page save clobbered file-only fields: %+v", saved)
	}

	// A broken id is replaced, not kept.
	if n := Normalize(Settings{InstallID: "not-hex"}); n.InstallID != "" {
		t.Fatalf("bad InstallID kept: %q", n.InstallID)
	}
}

func TestPeerVolumes(t *testing.T) {
	s := newTestService(t)
	for _, c := range []struct{ in, want int }{{150, 150}, {999, MaxPeerVolume}, {-5, 0}} {
		st, err := s.SetPeerVolume("a", c.in)
		if err != nil {
			t.Fatal(err)
		}
		if st.Call.Volumes["a"] != c.want {
			t.Errorf("SetPeerVolume(%d) = %d, want %d", c.in, st.Call.Volumes["a"], c.want)
		}
	}
	st, _ := s.SetPeerVolume("a", 100)
	if _, ok := st.Call.Volumes["a"]; ok {
		t.Error("100% should not be stored")
	}
	if _, err := s.SetPeerVolume(s.self, 50); err == nil {
		t.Error("set our own volume")
	}
	if _, err := s.SetPeerVolume("", 50); err == nil {
		t.Error("set an empty peer's volume")
	}
	s.SetPeerVolume("b", 0)
	s.Shutdown() // flushes the pending save
	if v := readStreamJSON(t)["peerVolumes"].(map[string]any); v["b"] != float64(0) {
		t.Errorf("saved volumes %v", v)
	}
	n := Normalize(Settings{PeerVolumes: map[string]int{"x": 300, "y": 100, "": 50}})
	if len(n.PeerVolumes) != 1 || n.PeerVolumes["x"] != MaxPeerVolume {
		t.Errorf("Normalize volumes = %v", n.PeerVolumes)
	}
}

func TestICEListWithoutRelay(t *testing.T) {
	r := newRelay(Settings{}, "me", nil)
	r.start() // no CredentialURL: nothing to do
	defer r.stop()
	if got := r.servers(); !slices.EqualFunc(got, stunServers, iceEqual) {
		t.Fatalf("servers = %+v, want STUN only", got)
	}
	if st := r.Status(); st.State != RelayNone {
		t.Fatalf("status %+v", st)
	}
	// TURN URLs alone can't work (TURN needs credentials): still STUN only.
	r2 := newRelay(Normalize(Settings{TurnURLs: []string{"turn:relay.example:3478", "http://nope"}}), "me", nil)
	if got := r2.servers(); len(got) != len(stunServers) || r2.Status().State != RelayNone || r2.Status().Detail == "" {
		t.Fatalf("turn without credentials: %+v %+v", got, r2.Status())
	}
	// The pion and pipe shapes carry the same entries.
	if w := webrtcICE(stunServers); len(w) != len(stunServers) || w[0].URLs[0] != stunServers[0].URLs[0] {
		t.Fatalf("webrtcICE = %+v", w)
	}
	if p := pipeICE([]ICEServer{{URLs: []string{"turn:x"}, Username: "u", Credential: "c"}}); p[0].Username != "u" || p[0].Credential != "c" {
		t.Fatalf("pipeICE = %+v", p)
	}
}

func iceEqual(a, b ICEServer) bool {
	return slices.Equal(a.URLs, b.URLs) && a.Username == b.Username && a.Credential == b.Credential
}

// credServer is a TURN REST credential endpoint on 127.0.0.1.
func credServer(t *testing.T, fail *atomic.Bool) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("peer") != "me" {
			http.Error(w, "no peer", http.StatusBadRequest)
			return
		}
		if fail != nil && fail.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		i := n.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"username": "user" + string(rune('0'+i)),
			"password": "pw", // the TURN REST draft's name
			"ttl":      1,
			"uris":     []string{"turn:127.0.0.1:3478?transport=udp", "turns:relay.example:443?transport=tcp", "junk"},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func withFastRefresh(t *testing.T) {
	oldMin, oldRetry, oldMax := minRefresh, retryMin, retryMax
	minRefresh, retryMin, retryMax = 50*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { minRefresh, retryMin, retryMax = oldMin, oldRetry, oldMax })
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func turnEntry(list []ICEServer) *ICEServer {
	for i := range list {
		if list[i].Username != "" {
			return &list[i]
		}
	}
	return nil
}

func TestRelayCredentialRefresh(t *testing.T) {
	withFastRefresh(t)
	srv, n := credServer(t, nil)
	var changes atomic.Int32
	r := newRelay(Settings{CredentialURL: srv.URL + "/cred?service=turn"}, "me", func() { changes.Add(1) })
	if st := r.Status(); st.State != RelayPending {
		t.Fatalf("before the first fetch: %+v", st)
	}
	r.start()
	defer r.stop()
	waitFor(t, "first credentials", func() bool { return turnEntry(r.servers()) != nil })
	turn := turnEntry(r.servers())
	if turn.Username != "user1" || turn.Credential != "pw" {
		t.Fatalf("turn entry %+v", turn)
	}
	if !slices.Equal(turn.URLs, []string{"turn:127.0.0.1:3478?transport=udp", "turns:relay.example:443?transport=tcp"}) {
		t.Fatalf("turn urls %v", turn.URLs)
	}
	if st := r.Status(); st.State != RelayOK || st.Expires == 0 {
		t.Fatalf("status %+v", st)
	}
	// ttl 1 s: refreshed at 80%, before it expires.
	waitFor(t, "a refresh", func() bool { return n.Load() >= 2 })
	waitFor(t, "the new username", func() bool {
		e := turnEntry(r.servers())
		return e != nil && e.Username != "user1"
	})
	if changes.Load() < 2 {
		t.Fatalf("onChange ran %d times", changes.Load())
	}

	// TurnURLs in stream.json replace the endpoint's uris.
	r2 := newRelay(Settings{CredentialURL: srv.URL, TurnURLs: []string{"turns:mine.example:443"}}, "me", nil)
	r2.start()
	defer r2.stop()
	waitFor(t, "credentials", func() bool { return turnEntry(r2.servers()) != nil })
	if u := turnEntry(r2.servers()).URLs; !slices.Equal(u, []string{"turns:mine.example:443"}) {
		t.Fatalf("turnUrls override: %v", u)
	}
}

func TestRelayFailureKeepsThenDrops(t *testing.T) {
	withFastRefresh(t)
	var fail atomic.Bool
	srv, _ := credServer(t, &fail)
	var offset atomic.Int64
	r := newRelay(Settings{CredentialURL: srv.URL}, "me", nil)
	r.now = func() time.Time { return time.Now().Add(time.Duration(offset.Load())) }
	r.start()
	defer r.stop()
	waitFor(t, "credentials", func() bool { return turnEntry(r.servers()) != nil })
	fail.Store(true)
	waitFor(t, "the failure", func() bool { return r.Status().State == RelayError })
	if !strings.Contains(r.Status().Detail, "503") {
		t.Fatalf("detail %q", r.Status().Detail)
	}
	// Past expiry, credentials that can't be refreshed leave the list.
	offset.Store(int64(time.Hour))
	if turnEntry(r.servers()) != nil {
		t.Fatal("expired credentials still offered")
	}
	if got := r.servers(); len(got) != len(stunServers) {
		t.Fatalf("servers %+v", got)
	}
	// It recovers by itself.
	offset.Store(0)
	fail.Store(false)
	waitFor(t, "recovery", func() bool { return r.Status().State == RelayOK })
}

func TestRelayRejectsPlainHTTP(t *testing.T) {
	r := newRelay(Settings{CredentialURL: "http://relay.example/cred"}, "me", nil)
	if _, err := r.fetch(); err == nil || !strings.Contains(err.Error(), "https") {
		t.Fatalf("fetch over plain http to a remote host: %v", err)
	}
}

// fakeLink records what the room hands the LAN side.
type fakeLink struct {
	mu        sync.Mutex
	signals   []api.Signal
	drops     []string
	salts     []uint32
	rooms     []string
	iceEvents int
}

func (f *fakeLink) signal(peer string, blob json.RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signals = append(f.signals, api.Signal{Peer: peer, Blob: blob})
}
func (f *fakeLink) drop(peer string) { f.mu.Lock(); f.drops = append(f.drops, peer); f.mu.Unlock() }
func (f *fakeLink) readdress(salt uint32) {
	f.mu.Lock()
	f.salts = append(f.salts, salt)
	f.mu.Unlock()
}
func (f *fakeLink) roomChanged(room string) {
	f.mu.Lock()
	f.rooms = append(f.rooms, room)
	f.mu.Unlock()
}
func (f *fakeLink) iceChanged() { f.mu.Lock(); f.iceEvents++; f.mu.Unlock() }

func presence(t *testing.T, from string, p presenceBody) Envelope {
	t.Helper()
	raw, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{V: 1, From: from, Type: msgPresence, At: time.Now().UnixMilli(), Body: raw}
}

func TestLanRelay(t *testing.T) {
	s := newTestService(t)
	f := &fakeLink{}
	s.setLan(f)

	// msgLan from a member goes to the service as is; junk doesn't.
	s.onMessage(Envelope{From: "peerA", Type: msgLan, Body: json.RawMessage(`{"kind":"hello","wgPub":"k"}`)})
	s.onMessage(Envelope{From: "peerA", Type: msgLan, Body: json.RawMessage(`{"kind":`)})
	s.onMessage(Envelope{From: "peerA", Type: msgLan})
	s.onMessage(Envelope{From: "peerA", Type: msgLan, Body: json.RawMessage(`"` + strings.Repeat("x", maxLanBlob) + `"`)})
	if len(f.signals) != 1 || f.signals[0].Peer != "peerA" || string(f.signals[0].Blob) != `{"kind":"hello","wgPub":"k"}` {
		t.Fatalf("signals %+v", f.signals)
	}

	// Members that leave or time out become Drop.
	s.onMessage(presence(t, "peerA", presenceBody{Name: "A"}))
	s.onMessage(presence(t, "peerB", presenceBody{Name: "B"}))
	s.onMessage(Envelope{From: "peerA", Type: msgLeave})
	s.mu.Lock()
	s.members["peerB"].seen = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	s.expireMembers(time.Now())
	if !slices.Equal(f.drops, []string{"peerA", "peerB"}) {
		t.Fatalf("drops %v", f.drops)
	}

	// The service's blobs go back out through the room.
	if err := s.lanSend("peerA", json.RawMessage(`{"kind":"cand"}`)); !errors.Is(err, errNotInRoom) {
		t.Fatalf("lanSend outside a room = %v", err)
	}
	if err := s.lanSend("peerA", json.RawMessage(`{`)); !errors.Is(err, errBadLanBlob) {
		t.Fatalf("lanSend of bad JSON = %v", err)
	}
	if err := s.lanSend(s.self, json.RawMessage(`{}`)); err != nil {
		t.Fatalf("lanSend to ourselves = %v, want a silent no-op", err)
	}

	// Presence carries a member's LanInfo, cleaned.
	s.onMessage(presence(t, "peerC", presenceBody{Name: "C", Lan: &LanInfo{IP: "100.96.7.8", WGPub: "k", On: true}}))
	s.onMessage(presence(t, "peerD", presenceBody{Name: "D", Lan: &LanInfo{IP: "8.8.8.8", WGPub: "k", On: true}}))
	st := s.GetState()
	_ = st
	s.mu.Lock()
	c, d := s.members["peerC"].Lan, s.members["peerD"].Lan
	s.mu.Unlock()
	if c == nil || c.IP != "100.96.7.8" || !c.On {
		t.Fatalf("peerC lan %+v", c)
	}
	if d != nil {
		t.Fatalf("an address outside the overlay was kept: %+v", d)
	}

	// Our own LanInfo rides in presence.
	s.setLanInfo(&LanInfo{IP: "100.96.1.1", WGPub: "me", On: true})
	s.mu.Lock()
	ours := s.lanSelf
	s.mu.Unlock()
	if ours == nil || ours.IP != "100.96.1.1" {
		t.Fatalf("lanSelf %+v", ours)
	}

	if h := roomHash("toru/v1/abc"); len(h) != 16 || h == roomHash("toru/v1/abd") || strings.Contains(h, "abc") {
		t.Fatalf("roomHash %q", h)
	}
}

func key(b byte) string {
	k := make([]byte, 32)
	k[0] = b
	return base64.StdEncoding.EncodeToString(k)
}

func TestLanAddressCollision(t *testing.T) {
	s := newTestService(t)
	f := &fakeLink{}
	s.setLan(f)
	low, mid, high := key(0x01), key(0x80), key(0xF0)
	// Base64 text order differs from key order here ('/' < 'A' as text),
	// which keyLess must not be fooled by.
	if !keyLess(low, high) || keyLess(high, low) {
		t.Fatal("keyLess")
	}
	s.setLanInfo(&LanInfo{IP: "100.96.3.4", WGPub: mid, On: true})

	// A higher key on our address: they move, not us.
	s.onMessage(presence(t, "hi", presenceBody{Name: "Hi", Lan: &LanInfo{IP: "100.96.3.4", WGPub: high, On: true}}))
	// A lower key on another address, or with LAN off: no collision.
	s.onMessage(presence(t, "other", presenceBody{Name: "O", Lan: &LanInfo{IP: "100.96.3.5", WGPub: low, On: true}}))
	s.onMessage(presence(t, "off", presenceBody{Name: "Off", Lan: &LanInfo{IP: "100.96.3.4", WGPub: low, On: false}}))
	if len(f.salts) != 0 {
		t.Fatalf("readdress without a collision: %v", f.salts)
	}
	// A lower key on our address: we move, once, with the next salt.
	s.onMessage(presence(t, "lo", presenceBody{Name: "Lo", Lan: &LanInfo{IP: "100.96.3.4", WGPub: low, On: true}}))
	s.onMessage(presence(t, "lo", presenceBody{Name: "Lo", Lan: &LanInfo{IP: "100.96.3.4", WGPub: low, On: true}}))
	if !slices.Equal(f.salts, []uint32{1}) {
		t.Fatalf("salts %v, want [1]", f.salts)
	}
	if got := readStreamJSON(t)["lanSalt"]; got != float64(1) {
		t.Fatalf("saved lanSalt %v", got)
	}
	// Still colliding after the gap: the salt moves on.
	s.mu.Lock()
	s.readdressAt = time.Now().Add(-2 * readdressGap)
	s.mu.Unlock()
	s.onMessage(presence(t, "lo", presenceBody{Name: "Lo", Lan: &LanInfo{IP: "100.96.3.4", WGPub: low, On: true}}))
	if !slices.Equal(f.salts, []uint32{1, 2}) {
		t.Fatalf("salts %v, want [1 2]", f.salts)
	}
}

// --- LanService -------------------------------------------------------------

type fakeOS struct {
	mu        sync.Mutex
	info      lanSvcInfo
	startErr  error
	starts    int
	installer func(verb string) error
	ran       []string
}

func (o *fakeOS) supported() bool { return true }
func (o *fakeOS) query() lanSvcInfo {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.info
}
func (o *fakeOS) start() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.starts++
	return o.startErr
}
func (o *fakeOS) runInstaller(verb string) error {
	o.mu.Lock()
	o.ran = append(o.ran, verb)
	fn := o.installer
	o.mu.Unlock()
	if fn != nil {
		return fn(verb)
	}
	return nil
}

type fakePipe struct {
	h      client.Handlers
	mu     sync.Mutex
	sent   []api.Message
	start  bool
	closed bool
}

func (p *fakePipe) record(m api.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, m)
	return nil
}
func (p *fakePipe) Start()            { p.mu.Lock(); p.start = true; p.mu.Unlock() }
func (p *fakePipe) Close()            { p.mu.Lock(); p.closed = true; p.mu.Unlock() }
func (p *fakePipe) Up(u api.Up) error { return p.record(api.Message{Type: api.TypeUp, Up: &u}) }
func (p *fakePipe) Down() error       { return p.record(api.Message{Type: api.TypeDown}) }
func (p *fakePipe) SetICE(s []api.ICEServer) error {
	return p.record(api.Message{Type: api.TypeSetICE, SetICE: &api.SetICE{ICEServers: s}})
}
func (p *fakePipe) Signal(peer string, blob json.RawMessage) error {
	return p.record(api.Message{Type: api.TypeSignal, Signal: &api.Signal{Peer: peer, Blob: blob}})
}
func (p *fakePipe) Drop(peer string) error {
	return p.record(api.Message{Type: api.TypeDrop, Drop: &api.Drop{Peer: peer}})
}
func (p *fakePipe) Readdress(salt uint32) error {
	return p.record(api.Message{Type: api.TypeReaddress, Readdress: &api.Readdress{Salt: salt}})
}
func (p *fakePipe) SetPolicy(pol api.Policy) error {
	return p.record(api.Message{Type: api.TypeSetPolicy, SetPolicy: &pol})
}
func (p *fakePipe) RequestStatus() error { return p.record(api.Message{Type: api.TypeStatus}) }

func (p *fakePipe) types() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, m := range p.sent {
		out = append(out, m.Type)
	}
	return out
}

func (p *fakePipe) last(typ string) *api.Message {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.sent) - 1; i >= 0; i-- {
		if p.sent[i].Type == typ {
			m := p.sent[i]
			return &m
		}
	}
	return nil
}

func newTestLan(t *testing.T, info lanSvcInfo) (*StreamService, *LanService, *fakeOS, func() *fakePipe) {
	t.Helper()
	s := newTestService(t)
	o := &fakeOS{info: info}
	var mu sync.Mutex
	var pipes []*fakePipe
	l := newLanService(s, "0.13.0", o, func(h client.Handlers) lanPipe {
		p := &fakePipe{h: h}
		mu.Lock()
		pipes = append(pipes, p)
		mu.Unlock()
		return p
	})
	latest := func() *fakePipe {
		mu.Lock()
		defer mu.Unlock()
		if len(pipes) == 0 {
			return nil
		}
		return pipes[len(pipes)-1]
	}
	return s, l, o, latest
}

func TestLanServiceNotInstalled(t *testing.T) {
	_, l, _, _ := newTestLan(t, lanSvcInfo{})
	if st := l.GetState(); st.State != LanSetup {
		t.Fatalf("state %q, want setup", st.State)
	}
	l.roomChanged("room1")
	if _, err := l.Enable(); err == nil {
		t.Fatal("Enable with no service")
	}
	if st := l.GetState(); st.State != LanSetup || st.Wanted {
		t.Fatalf("after Enable: %+v", st)
	}
}

func TestLanServiceUpAndDown(t *testing.T) {
	s, l, o, latest := newTestLan(t, lanSvcInfo{installed: true})
	if _, err := l.Enable(); err == nil {
		t.Fatal("Enable outside a room")
	}
	l.roomChanged("room1")
	st, err := l.Enable()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Wanted || !s.GetSettings().LanOn {
		t.Fatalf("switch not on: %+v", st)
	}
	waitFor(t, "the pipe", func() bool { p := latest(); return p != nil && p.start })
	p := latest()
	o.mu.Lock()
	starts := o.starts
	o.mu.Unlock()
	if l.GetState().State != LanStarting || starts != 1 {
		t.Fatalf("state %q starts %d", l.GetState().State, starts)
	}

	pub := key(0x42)
	p.h.Connected(api.Hello{Major: api.ProtocolMajor, Minor: api.ProtocolMinor, Version: "0.13.0", PubKey: pub, IP: "100.96.9.9"})
	up := p.last(api.TypeUp)
	if up == nil || up.Up.Room != "room1" || up.Up.Self != s.self || len(up.Up.ICEServers) != len(stunServers) {
		t.Fatalf("Up %+v", up)
	}
	p.h.Status(api.Status{State: api.StateOn, IP: "100.96.9.9", PubKey: pub,
		Peers: []api.PeerState{{Peer: "f", IP: "100.96.1.2", State: api.PeerConnected, Path: api.PathDirect, RTTMs: 12}}})
	st = l.GetState()
	if st.State != LanOn || st.IP != "100.96.9.9" || st.Peers["f"].RTTMs != 12 || st.Relay != RelayNone {
		t.Fatalf("on: %+v", st)
	}
	s.mu.Lock()
	ours := s.lanSelf
	s.mu.Unlock()
	if ours == nil || !ours.On || ours.IP != "100.96.9.9" || ours.WGPub != pub {
		t.Fatalf("presence LanInfo %+v", ours)
	}

	// Room traffic flows both ways, and leavers are dropped.
	s.onMessage(Envelope{From: "f", Type: msgLan, Body: json.RawMessage(`{"kind":"cand"}`)})
	if m := p.last(api.TypeSignal); m == nil || m.Signal.Peer != "f" {
		t.Fatalf("signal to pipe %+v", m)
	}
	s.onMessage(Envelope{From: "f", Type: msgLeave})
	if m := p.last(api.TypeDrop); m == nil || m.Drop.Peer != "f" {
		t.Fatalf("drop %+v", m)
	}
	if _, ok := l.GetState().Peers["f"]; ok {
		t.Fatal("dropped peer still listed")
	}
	p.h.PeerState(api.PeerState{Peer: "g", State: api.PeerConnecting})
	if l.GetState().Peers["g"].State != api.PeerConnecting {
		t.Fatal("PeerState not applied")
	}

	// Fresh TURN credentials and policy changes reach the service.
	l.iceChanged()
	if p.last(api.TypeSetICE) == nil {
		t.Fatal("no SetICE")
	}
	l.SetPolicy(true, false)
	if m := p.last(api.TypeSetPolicy); m == nil || !m.SetPolicy.AllowServices || m.SetPolicy.MDNS {
		t.Fatalf("SetPolicy %+v", m)
	}
	if !s.GetSettings().LanAllowServices {
		t.Fatal("policy not saved")
	}

	// A new room swaps peers: Down for the old, Up for the new.
	l.roomChanged("")
	if p.last(api.TypeDown) == nil {
		t.Fatal("leaving the room didn't send Down")
	}
	if st := l.GetState(); st.State != LanOff || !st.Wanted {
		t.Fatalf("out of a room: %+v", st)
	}
	l.roomChanged("room2")
	waitFor(t, "Up for room2", func() bool { m := p.last(api.TypeUp); return m != nil && m.Up.Room == "room2" })

	// The service goes away and comes back: Up again on reconnect.
	p.h.Disconnected(errors.New("pipe closed"))
	if st := l.GetState(); st.State != LanStarting {
		t.Fatalf("after disconnect: %q", st.State)
	}
	n := len(p.types())
	p.h.Connected(api.Hello{Major: api.ProtocolMajor, Version: "0.13.0", PubKey: pub, IP: "100.96.9.9"})
	if m := p.last(api.TypeUp); m == nil || len(p.types()) == n {
		t.Fatal("no Up after reconnect")
	}

	l.Disable()
	if !p.closed || s.GetSettings().LanOn {
		t.Fatalf("Disable: closed %v lanOn %v", p.closed, s.GetSettings().LanOn)
	}
	s.mu.Lock()
	ours = s.lanSelf
	s.mu.Unlock()
	if ours == nil || ours.On {
		t.Fatalf("presence after Disable %+v", ours)
	}
	// A late callback from the closed pipe changes nothing.
	p.h.Status(api.Status{State: api.StateOn})
	if l.GetState().State != LanOff {
		t.Fatal("closed pipe's Status applied")
	}
}

func TestLanServiceErrors(t *testing.T) {
	_, l, o, latest := newTestLan(t, lanSvcInfo{installed: true})
	l.roomChanged("room1")

	// The service manager won't start it.
	o.mu.Lock()
	o.startErr = errors.New("access denied")
	o.mu.Unlock()
	if _, err := l.Enable(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the failure", func() bool { return l.GetState().State == LanFailed })
	if st := l.GetState(); st.Error != LanErrService {
		t.Fatalf("error %+v", st)
	}

	// It starts, but the range is taken.
	o.mu.Lock()
	o.startErr = nil
	o.mu.Unlock()
	if _, err := l.Enable(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a pipe", func() bool { p := latest(); return p != nil && p.start })
	p := latest()
	p.h.Connected(api.Hello{Major: api.ProtocolMajor, Version: "0.13.0"})
	p.h.Status(api.Status{State: api.StateError, Error: api.ErrRangeInUse, Detail: "Tailscale"})
	if st := l.GetState(); st.State != LanFailed || st.Error != api.ErrRangeInUse || st.Detail != "Tailscale" {
		t.Fatalf("range in use: %+v", st)
	}

	// An older service: Update LAN, and no Up.
	n := len(p.types())
	p.h.Disconnected(errors.New("restart"))
	p.h.Connected(api.Hello{Major: api.ProtocolMajor, Version: "0.12.0"})
	if st := l.GetState(); st.State != LanUpdate {
		t.Fatalf("older service: %q", st.State)
	}
	for _, typ := range p.types()[n:] {
		if typ == api.TypeUp {
			t.Fatal("Up sent to an older service")
		}
	}
	// Another major protocol.
	p.h.Disconnected(&client.IncompatibleError{Hello: api.Hello{Major: api.ProtocolMajor + 1}})
	if st := l.GetState(); st.State != LanUpdate {
		t.Fatalf("incompatible service: %q", st.State)
	}

	// The user declines UAC.
	o.mu.Lock()
	o.installer = func(string) error { return errUACCancelled }
	o.mu.Unlock()
	if _, err := l.Setup(); err == nil || !strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("Setup with UAC declined: %v", err)
	}
	if l.GetState().Busy != "" {
		t.Fatal("still busy")
	}
}

func TestLanServiceSetupTurnsOn(t *testing.T) {
	_, l, o, latest := newTestLan(t, lanSvcInfo{})
	l.roomChanged("room1")
	o.mu.Lock()
	o.installer = func(verb string) error {
		o.mu.Lock()
		o.info = lanSvcInfo{installed: verb == "install"}
		o.mu.Unlock()
		return nil
	}
	o.mu.Unlock()
	st, err := l.Setup()
	if err != nil {
		t.Fatal(err)
	}
	if !st.Wanted {
		t.Fatal("first setup in a room should turn LAN on")
	}
	waitFor(t, "the pipe", func() bool { p := latest(); return p != nil && p.start })
	if _, err := l.Uninstall(); err != nil {
		t.Fatal(err)
	}
	if st := l.GetState(); st.State != LanSetup || st.Wanted || !latest().closed {
		t.Fatalf("after Uninstall: %+v", st)
	}
	if !slices.Equal(o.ran, []string{"install", "uninstall"}) {
		t.Fatalf("installer ran %v", o.ran)
	}
}

func TestVersionLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.12.0", "0.13.0", true},
		{"v0.13.0", "0.13.0", false},
		{"0.13.1", "0.13.0", false},
		{"1.0", "0.99.9", false},
		{"dev", "0.13.0", true},
		{"0.13.0-rc1", "0.13.0", false},
		{"0.13.0", "dev", false},
	} {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestPrepareWintun(t *testing.T) {
	vendored := filepath.Join("..", "..", "build", "windows", "wintun")
	if _, err := os.Stat(filepath.Join(vendored, api.WintunDLL)); err != nil {
		t.Skip("vendored wintun.dll not present")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, api.LanExe)
	if err := prepareWintun(exe, []string{filepath.Join(dir, "nowhere"), vendored}); err != nil {
		t.Fatal(err)
	}
	if sum, err := fileSHA256(filepath.Join(dir, api.WintunDLL)); err != nil || sum != api.WintunSHA256 {
		t.Fatalf("copied dll: %s %v", sum, err)
	}
	// Some other DLL is never copied.
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, api.WintunDLL), []byte("not wintun"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := prepareWintun(filepath.Join(t.TempDir(), api.LanExe), []string{bad}); err == nil {
		t.Fatal("copied a DLL with the wrong hash")
	}
	if err := prepareWintun(filepath.Join(t.TempDir(), api.LanExe), nil); err == nil {
		t.Fatal("no DLL anywhere should be an error")
	}
	if d := lanExeDirs(`C:\a`, `C:\l`, `C:\a`); len(d) != 4 {
		t.Fatalf("lanExeDirs dedupe: %v", d)
	}
}
