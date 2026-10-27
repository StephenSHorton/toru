package stream

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Events the Stream page and watch windows listen to.
const (
	EventState = "stream:state" // State, whenever anything on the page changes
	EventOffer = "stream:offer" // Offer, to the watch window for that session
	EventHost  = "stream:host"  // HostEvent: a streamer restarted or ended a session
)

// presenceEvery is how often each app re-announces itself; members not heard
// from for memberTimeout are dropped.
const (
	presenceEvery = 4 * time.Second
	memberTimeout = 13 * time.Second
)

// Member is someone else in the room.
type Member struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Live     *LiveInfo `json:"live"`
	InCall   bool      `json:"inCall"`
	Muted    bool      `json:"muted"`
	Deafened bool      `json:"deafened"`
	Speaking bool      `json:"speaking"` // as heard by our call window
}

// RoomState is the room as the page shows it.
type RoomState struct {
	Code      string   `json:"code"`
	Connected int      `json:"connected"` // relays connected right now
	Relays    int      `json:"relays"`    // relays tried
	Members   []Member `json:"members"`   // everyone else, sorted by name
}

// State is everything the Stream page shows.
type State struct {
	Self    string     `json:"self"`
	Preview bool       `json:"preview"` // "Show my stream" is on
	Room    *RoomState `json:"room"`    // nil when not in a room
	Live    LiveStatus `json:"live"`
	Call    CallState  `json:"call"`
}

// Offer is a streamer's WebRTC offer for one watch window.
type Offer struct {
	Host    string   `json:"host"`
	Session string   `json:"session"`
	SDP     string   `json:"sdp"`
	Info    LiveInfo `json:"info"`
}

// HostEvent tells a watch window its stream restarted ("restart": ask
// again), ended ("ended"), or should close
// ("close": the preview was turned off).
type HostEvent struct {
	Host    string `json:"host"`
	Session string `json:"session"`
	Kind    string `json:"kind"`
}

// Monitor is one capturable display.
type Monitor struct {
	ID      int    `json:"id"`
	Label   string `json:"label"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Primary bool   `json:"primary"`
}

type member struct {
	Member
	seen time.Time
}

// StreamService is the Wails-bound API behind the Stream page and the watch
// windows.
//
// JS binding name: StreamService.*
type StreamService struct {
	mu        sync.Mutex
	app       *application.App
	self      string
	settings  Settings
	room      *Room
	members   map[string]*member
	bc        *broadcaster
	sessions  map[string]string // watch session → host peer (this app watching)
	openWatch func(host, name string)
	openCall  func()
	inCall    bool
	muted     bool
	deafened  bool
	speaking  map[string]bool   // call peers talking now (call window reports)
	callPeers map[string]string // call peer → connection state
	quit      chan struct{}

	emitMu   sync.Mutex
	lastEmit time.Time
	pending  bool
}

// NewService returns the service with persisted settings loaded.
func NewService() *StreamService {
	return &StreamService{
		self:     randHex(8),
		settings: loadSettings(),
		members:  map[string]*member{},
		sessions: map[string]string{},
		quit:     make(chan struct{}),
	}
}

// SetApp injects the running app (event emits).
//
//wails:ignore
func (s *StreamService) SetApp(app *application.App) {
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

// SetWatchOpener installs the function that opens a watch window.
//
//wails:ignore
func (s *StreamService) SetWatchOpener(fn func(host, name string)) {
	s.mu.Lock()
	s.openWatch = fn
	s.mu.Unlock()
}

// Init rejoins the saved room and starts announcing presence.
//
//wails:ignore
func (s *StreamService) Init() {
	s.mu.Lock()
	code := s.settings.RoomCode
	s.mu.Unlock()
	if code != "" {
		if _, err := s.JoinRoom(code); err != nil {
			logf("rejoin room: %v", err)
		}
	}
	go s.presenceLoop()
}

// Shutdown ends a live stream and leaves the room.
//
//wails:ignore
func (s *StreamService) Shutdown() {
	s.mu.Lock()
	bc, room := s.bc, s.room
	s.bc, s.room = nil, nil
	select {
	case <-s.quit:
	default:
		close(s.quit)
	}
	s.mu.Unlock()
	if bc != nil {
		bc.stop()
	}
	if room != nil {
		_ = room.Send("", msgLeave, struct{}{})
		room.Close()
	}
}

// --- settings ---------------------------------------------------------------

// GetSettings returns the current settings.
func (s *StreamService) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings saves new settings (normalized) and returns them. While live,
// the stream picks them up at once; viewers stay connected unless the
// codec, monitor or audio on/off changed, and then they reconnect by
// themselves.
func (s *StreamService) SetSettings(in Settings) (Settings, error) {
	s.mu.Lock()
	in.RoomCode = s.settings.RoomCode                 // the room is changed by Join/Leave only
	in.ShowPreview = s.settings.ShowPreview           // and the preview by SetPreview
	in.NoiseSuppression = s.settings.NoiseSuppression // voice settings go through SetVoice
	in.MicDevice, in.SpeakerDevice = s.settings.MicDevice, s.settings.SpeakerDevice
	next := Normalize(in)
	prev := s.settings
	s.settings = next
	bc := s.bc
	s.mu.Unlock()
	if err := saveSettings(next); err != nil {
		logf("save settings: %v", err)
	}
	var err error
	if bc != nil && liveChanged(prev, next) {
		err = bc.update(next)
	}
	if prev.Name != next.Name {
		s.announce()
	}
	s.emitState()
	return next, err
}

func liveChanged(a, b Settings) bool {
	a.Name, b.Name, a.RoomCode, b.RoomCode = "", "", "", ""
	a.NotifyLive, b.NotifyLive, a.ShowPreview, b.ShowPreview = false, false, false, false
	return a != b
}

// ListMonitors returns the monitors that can be streamed.
func (s *StreamService) ListMonitors() []Monitor {
	out := []Monitor{}
	for _, d := range capture.EnumDisplays() {
		primary := d.X == 0 && d.Y == 0
		label := fmt.Sprintf("Display %d · %d×%d", d.Index+1, d.W, d.H)
		if primary {
			label += " (primary)"
		}
		out = append(out, Monitor{ID: d.Index, Label: label, W: d.W, H: d.H, Primary: primary})
	}
	return out
}

// EncoderSupport reports which codecs this PC can stream (hardware encoder
// present). The first call probes (~1s); results are cached.
func (s *StreamService) EncoderSupport() map[string]bool {
	return map[string]bool{
		CodecH264: capture.ReplayEncoderAvailable(capture.ReplayCodecH264),
		CodecAV1:  capture.ReplayEncoderAvailable(capture.ReplayCodecAV1),
	}
}

// --- room -------------------------------------------------------------------

// GetState returns the page's state.
func (s *StreamService) GetState() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stateLocked()
}

func (s *StreamService) stateLocked() State {
	st := State{Self: s.self, Preview: s.settings.ShowPreview, Call: s.callStateLocked()}
	if s.bc != nil {
		st.Live = s.bc.Status()
	} else {
		st.Live.Viewers = []ViewerInfo{}
	}
	if s.room != nil {
		rs := &RoomState{Code: s.room.Code(), Connected: s.room.Connected(), Relays: len(brokerSets), Members: []Member{}}
		for _, m := range s.members {
			mm := m.Member
			mm.Speaking = s.inCall && mm.InCall && s.speaking[mm.ID] && !mm.Muted
			rs.Members = append(rs.Members, mm)
		}
		sort.Slice(rs.Members, func(i, j int) bool {
			if rs.Members[i].Name != rs.Members[j].Name {
				return rs.Members[i].Name < rs.Members[j].Name
			}
			return rs.Members[i].ID < rs.Members[j].ID
		})
		st.Room = rs
	}
	return st
}

// CreateRoom makes a new room and joins it.
func (s *StreamService) CreateRoom() (State, error) {
	return s.JoinRoom(NewCode())
}

// JoinRoom leaves the current room (if any) and joins the one for code.
func (s *StreamService) JoinRoom(code string) (State, error) {
	canon, ok := NormalizeCode(code)
	if !ok {
		return s.GetState(), errors.New("that isn't a room code (16 letters and numbers, like ABCD-EFGH-JKMN-PQRS)")
	}
	s.mu.Lock()
	if s.room != nil && s.room.Code() == canon {
		st := s.stateLocked()
		s.mu.Unlock()
		return st, nil
	}
	s.mu.Unlock()
	s.leave()
	room, err := OpenRoom(canon, s.self, s.onMessage, func() {
		s.announce()
		s.emitState()
	})
	if err != nil {
		return s.GetState(), err
	}
	s.mu.Lock()
	s.room = room
	s.members = map[string]*member{}
	s.settings.RoomCode = canon
	settings := s.settings
	s.mu.Unlock()
	if err := saveSettings(settings); err != nil {
		logf("save settings: %v", err)
	}
	s.emitState()
	return s.GetState(), nil
}

// LeaveRoom leaves the room (ending a live stream) and forgets its code.
func (s *StreamService) LeaveRoom() State {
	s.LeaveCall()
	s.leave()
	s.mu.Lock()
	s.settings.RoomCode = ""
	settings := s.settings
	s.mu.Unlock()
	if err := saveSettings(settings); err != nil {
		logf("save settings: %v", err)
	}
	s.emitState()
	return s.GetState()
}

func (s *StreamService) leave() {
	s.mu.Lock()
	room := s.room
	s.room = nil
	s.members = map[string]*member{}
	s.mu.Unlock()
	if room != nil {
		_ = room.Send("", msgLeave, struct{}{})
		room.Close()
	}
}

func (s *StreamService) send(to, typ string, body any) error {
	if to == s.self {
		// Previewing our own stream: the same messages, delivered in-process.
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		go s.onMessage(Envelope{V: 1, From: s.self, To: s.self, Type: typ, At: time.Now().UnixMilli(), Body: raw})
		return nil
	}
	s.mu.Lock()
	room := s.room
	s.mu.Unlock()
	if room == nil {
		return errors.New("not in a room")
	}
	return room.Send(to, typ, body)
}

func (s *StreamService) announce() {
	s.mu.Lock()
	room := s.room
	body := presenceBody{Name: s.settings.Name, InCall: s.inCall, Muted: s.muted || s.deafened, Deafened: s.deafened}
	if s.bc != nil {
		info := s.bc.Info()
		body.Live = &info
	}
	s.mu.Unlock()
	if room != nil {
		_ = room.Send("", msgPresence, body)
	}
}

func (s *StreamService) presenceLoop() {
	t := time.NewTicker(presenceEvery)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
		}
		s.announce()
		now := time.Now()
		var gone []string
		s.mu.Lock()
		for id, m := range s.members {
			if now.Sub(m.seen) > memberTimeout {
				gone = append(gone, id)
				delete(s.members, id)
			}
		}
		bc := s.bc
		s.mu.Unlock()
		for _, id := range gone {
			if bc != nil {
				bc.removePeer(id)
			}
		}
		s.emitState() // relay connection counts change without messages
	}
}

// onMessage handles one room message from another peer.
func (s *StreamService) onMessage(env Envelope) {
	switch env.Type {
	case msgPresence:
		p, err := decodeBody[presenceBody](env)
		if err != nil {
			return
		}
		s.mu.Lock()
		m := s.members[env.From]
		isNew := m == nil
		wasLive := m != nil && m.Live != nil
		if m == nil {
			m = &member{}
			s.members[env.From] = m
		}
		m.ID, m.Name, m.Live, m.seen = env.From, clipName(p.Name), p.Live, time.Now()
		m.InCall, m.Muted, m.Deafened = p.InCall, p.Muted, p.Deafened
		notify := s.settings.NotifyLive && p.Live != nil && !wasLive
		name := m.Name
		s.mu.Unlock()
		if isNew {
			s.announce() // let a newcomer see us without waiting a full period
		}
		if notify {
			s.notifyLive(env.From, name)
		}
		s.emitState()

	case msgLeave:
		s.mu.Lock()
		delete(s.members, env.From)
		bc := s.bc
		s.mu.Unlock()
		if bc != nil {
			bc.removePeer(env.From)
		}
		s.emitState()

	case msgWatch:
		w, err := decodeBody[watchBody](env)
		if err != nil {
			return
		}
		s.mu.Lock()
		bc := s.bc
		s.mu.Unlock()
		if bc == nil {
			_ = s.send(env.From, msgNotLive, restartBody{Session: w.Session})
			return
		}
		go func() {
			if err := bc.addViewer(env.From, w.Session, w.Name); err != nil {
				logf("viewer %s: %v", w.Name, err)
			}
		}()

	case msgAnswer:
		a, err := decodeBody[answerBody](env)
		if err != nil {
			return
		}
		s.mu.Lock()
		bc := s.bc
		s.mu.Unlock()
		if bc != nil {
			if err := bc.answer(env.From, a.Session, a.SDP); err != nil {
				logf("answer: %v", err)
			}
		}

	case msgUnwatch:
		u, err := decodeBody[restartBody](env)
		if err != nil {
			return
		}
		s.mu.Lock()
		bc := s.bc
		s.mu.Unlock()
		if bc != nil {
			bc.removeViewer(env.From, u.Session)
		}

	case msgCall:
		s.onCallMessage(env)

	case msgOffer:
		o, err := decodeBody[offerBody](env)
		if err != nil {
			return
		}
		if s.ownsSession(o.Session, env.From) {
			s.emit(EventOffer, Offer{Host: env.From, Session: o.Session, SDP: o.SDP, Info: o.Info})
		}

	case msgRestart, msgEnded, msgNotLive:
		r, err := decodeBody[restartBody](env)
		if err != nil {
			return
		}
		if s.ownsSession(r.Session, env.From) {
			kind := "restart"
			if env.Type != msgRestart {
				kind = "ended"
			}
			s.emit(EventHost, HostEvent{Host: env.From, Session: r.Session, Kind: kind})
		}
	}
}

func (s *StreamService) ownsSession(session, host string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[session] == host
}

func (s *StreamService) notifyLive(host, name string) {
	toast.Post(toast.Note{
		ID:    "stream-live-" + host,
		Title: name + " is live",
		Body:  "They're sharing their screen in the call",
		Kind:  toast.Info,
		Actions: []toast.Action{{Label: "Join & watch", Run: func() {
			if _, err := s.JoinCall(); err == nil {
				_ = s.OpenWatch(host)
			}
		}}},
	})
}

// --- going live -------------------------------------------------------------

// GoLive starts streaming to the room with the current settings.
func (s *StreamService) GoLive() (State, error) {
	s.mu.Lock()
	if !s.inCall {
		s.mu.Unlock()
		return s.GetState(), errors.New("join the call to share your screen")
	}
	if s.bc != nil {
		st := s.stateLocked()
		s.mu.Unlock()
		return st, nil
	}
	cfg := s.settings
	s.mu.Unlock()

	bc, err := startBroadcast(cfg, s.send, s.emitState)
	if err != nil {
		return s.GetState(), err
	}
	s.mu.Lock()
	if s.bc != nil || s.room == nil { // raced with another GoLive or a leave
		s.mu.Unlock()
		bc.stop()
		return s.GetState(), nil
	}
	s.bc = bc
	preview := s.settings.ShowPreview
	s.mu.Unlock()
	s.announce()
	if preview {
		_ = s.OpenWatch(s.self)
	}
	s.emitState()
	return s.GetState(), nil
}

// StopLive ends the stream; viewers see it end.
func (s *StreamService) StopLive() State {
	s.closePreview()
	s.mu.Lock()
	bc := s.bc
	s.bc = nil
	s.mu.Unlock()
	if bc != nil {
		bc.stop()
		s.announce()
	}
	s.emitState()
	return s.GetState()
}

// --- watching ---------------------------------------------------------------

// OpenWatch opens a window watching host's stream. host == the State's Self
// previews this app's own stream exactly as viewers get it.
func (s *StreamService) OpenWatch(host string) error {
	s.mu.Lock()
	open := s.openWatch
	var name string
	live := false
	if host == s.self {
		name, live = s.settings.Name+" (preview)", s.bc != nil
	} else if m := s.members[host]; m != nil {
		name, live = m.Name, m.Live != nil
	}
	inCall := s.inCall
	s.mu.Unlock()
	if !inCall {
		return errors.New("join the call to watch")
	}
	if !live {
		return errors.New("they aren't live right now")
	}
	if open == nil {
		return errors.New("can't open a window")
	}
	open(host, name)
	return nil
}

// WatchStart (watch window) asks host for its stream under a new session id;
// the offer arrives on stream:offer.
func (s *StreamService) WatchStart(host, session string) error {
	if session == "" {
		return errors.New("missing session")
	}
	s.mu.Lock()
	s.sessions[session] = host
	name := s.settings.Name
	s.mu.Unlock()
	return s.send(host, msgWatch, watchBody{Session: session, Name: name})
}

// WatchAnswer (watch window) sends the WebRTC answer for session.
func (s *StreamService) WatchAnswer(host, session, sdp string) error {
	if !s.ownsSession(session, host) {
		return errors.New("unknown session")
	}
	return s.send(host, msgAnswer, answerBody{Session: session, SDP: sdp})
}

// WatchStop (watch window closing) tells host this session is done.
func (s *StreamService) WatchStop(host, session string) {
	s.mu.Lock()
	delete(s.sessions, session)
	s.mu.Unlock()
	_ = s.send(host, msgUnwatch, restartBody{Session: session})
}

// SetPreview turns "Show my stream" on or off, opening or closing the
// preview window right away while live.
func (s *StreamService) SetPreview(on bool) State {
	s.mu.Lock()
	s.settings.ShowPreview = on
	settings := s.settings
	live := s.bc != nil
	s.mu.Unlock()
	if err := saveSettings(settings); err != nil {
		logf("save settings: %v", err)
	}
	if live {
		if on {
			_ = s.OpenWatch(s.self)
		} else {
			s.closePreview()
		}
	}
	s.emitState()
	return s.GetState()
}

// WatchClosed (watch window closed by the user) is WatchStop; closing the
// preview window by hand also turns "Show my stream" off.
func (s *StreamService) WatchClosed(host, session string) {
	s.WatchStop(host, session)
	if host != s.self {
		return
	}
	s.mu.Lock()
	changed := s.settings.ShowPreview
	s.settings.ShowPreview = false
	settings := s.settings
	s.mu.Unlock()
	if changed {
		if err := saveSettings(settings); err != nil {
			logf("save settings: %v", err)
		}
		s.emitState()
	}
}

// closePreview tells every preview window (watching ourselves) to close.
func (s *StreamService) closePreview() {
	s.mu.Lock()
	var sessions []string
	for session, host := range s.sessions {
		if host == s.self {
			sessions = append(sessions, session)
		}
	}
	s.mu.Unlock()
	for _, session := range sessions {
		s.emit(EventHost, HostEvent{Host: s.self, Session: session, Kind: "close"})
	}
}

// HostName returns the room name for peer id ("" if unknown).
func (s *StreamService) HostName(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m := s.members[host]; m != nil {
		return m.Name
	}
	return ""
}

// --- events -----------------------------------------------------------------

func (s *StreamService) emit(name string, data any) {
	s.mu.Lock()
	app := s.app
	s.mu.Unlock()
	if app != nil {
		app.Event.Emit(name, data)
	}
}

// emitState pushes State to the page, at most ~4 times a second (stats and
// relay chatter would otherwise re-render it constantly).
func (s *StreamService) emitState() {
	const gap = 250 * time.Millisecond
	s.emitMu.Lock()
	if s.pending {
		s.emitMu.Unlock()
		return
	}
	wait := gap - time.Since(s.lastEmit)
	if wait > 0 {
		s.pending = true
		s.emitMu.Unlock()
		time.AfterFunc(wait, func() {
			s.emitMu.Lock()
			s.pending = false
			s.lastEmit = time.Now()
			s.emitMu.Unlock()
			s.emit(EventState, s.GetState())
		})
		return
	}
	s.lastEmit = time.Now()
	s.emitMu.Unlock()
	s.emit(EventState, s.GetState())
}
