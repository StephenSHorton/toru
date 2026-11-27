// Package boardshare shares whiteboards between Toru installs
// (docs/whiteboard.md, section 2, "Shared boards"). It rides the room Toru
// already has for Call and LAN: everything goes as sealed room messages,
// so sharing opens no port and no socket of its own, and each person's
// agents keep talking only to their own Toru over its own pipe.
//
// One Toru, the host, owns a shared board and its one undo history. Every
// other Toru in the room that joins it keeps a mirror (board.Store's
// mirror.go):
//
//   - The host sends every op its store applies to a shared board to the
//     room, and a "hello" every few seconds listing what it shares and at
//     which rev.
//   - A joiner asks for the whole board ("join"), saves it as a mirror, and
//     applies the host's ops in rev order. A gap that doesn't fill, or a
//     hello with a rev it never reached, asks for the whole board again.
//   - A change made on a mirror, by the person or an agent, goes to the host
//     as a request; the host stamps who made it from the sender's name in
//     the room (never from the request), applies it through its own store,
//     which checks it like any change, and answers with the op as applied.
//   - Pictures go by name on demand: whoever applies an op with a picture
//     it lacks asks the peer the op came from, and checks the bytes match
//     the name.
//
// Room messages reach only this: board ops on boards their host shared,
// whole boards, and pictures those boards show. Nothing here reaches the
// agent command registry, exports, imports or windows.
package boardshare

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
)

// Room is the room connection boardshare rides: stream.StreamService, or a
// fake in tests.
type Room interface {
	// RoomPeer is this install's peer id, its name in the room, and the
	// room's code ("" when not in one).
	RoomPeer() (self, name, code string)
	// HostName is a member's name, "" when unknown.
	HostName(peer string) string
	// SendBoard sends one message to peer to, "" for everyone.
	SendBoard(to string, body json.RawMessage) error
}

// Timing.
var (
	helloEvery   = 4 * time.Second
	offerTimeout = 13 * time.Second // a host not heard from this long has stopped sharing
	tickEvery    = time.Second
	reqTimeout   = 20 * time.Second
	joinTimeout  = 20 * time.Second
	gapWait      = 1500 * time.Millisecond // a gap in revs waits this long, then asks for the board
	snapAgain    = 4 * time.Second         // asks for the whole board at most this often
	imageAgain   = 3 * time.Second
	imageTries   = 10
	partTimeout  = 2 * time.Minute
)

// Sizes.
const (
	partSize      = 48 << 10 // one part of a big message, before base64
	maxWhole      = 64 << 20 // a whole message put back from parts
	maxPartials   = 8        // big messages from one peer in flight at once
	queueLen      = 4096
	EventState    = "board:share" // State, whenever it changes
	somebody      = "Someone"
	maxSharesSent = 64
)

// Kinds of message.
const (
	tHello   = "hello"   // everyone, every helloEvery: what I share, what I've joined
	tJoin    = "join"    // joiner → host: send me the whole board
	tSnap    = "snap"    // host → joiner: the whole board
	tOp      = "op"      // host → everyone: an op on a shared board, as applied
	tReq     = "req"     // joiner → host: make this change, undo or redo
	tRes     = "res"     // host → joiner: the answer to a req
	tUnshare = "unshare" // host → everyone: I stopped sharing this board
	tImgReq  = "img?"    // anyone → the peer an op came from: send me this picture
	tImg     = "img"     // the answer: the picture, or missing
	tPart    = "part"    // one part of a message too big to send whole
)

type message struct {
	T string `json:"t"`

	Shares []shareInfo `json:"shares,omitempty"` // hello
	Joined []joinRef   `json:"joined,omitempty"` // hello

	Board  string          `json:"board,omitempty"`
	Req    string          `json:"req,omitempty"`
	Action string          `json:"action,omitempty"` // req: apply, undo, redo
	Op     *board.Op       `json:"op,omitempty"`
	Src    board.Source    `json:"src,omitempty"`
	Err    string          `json:"err,omitempty"`
	None   bool            `json:"none,omitempty"` // res: nothing to undo or redo
	Snap   json.RawMessage `json:"snap,omitempty"`

	Asset   string `json:"asset,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Missing bool   `json:"missing,omitempty"`

	ID   string `json:"id,omitempty"` // part
	I    int    `json:"i,omitempty"`
	N    int    `json:"n,omitempty"`
	Part []byte `json:"part,omitempty"`
}

type shareInfo struct {
	Board string `json:"board"`
	Name  string `json:"name"`
	Rev   int    `json:"rev"`
}

type joinRef struct {
	Host  string `json:"host"`
	Board string `json:"board"`
}

// key names a shared board: its host and its id on the host.
type key struct{ host, board string }

type offer struct {
	shareInfo
	seen time.Time
}

type hosted struct {
	viewers map[string]time.Time // peer → last hello naming this board
}

type mirror struct {
	key
	local    string
	hostName string
	live     bool // has its board, and its Remote is set
	joined   chan struct{}
	buffered map[int]board.Op // ops past a gap, by rev
	gapSince time.Time
	snapAt   time.Time // when the whole board was last asked for
	lastRev  int       // the rev at the last hello, to see a mirror that's stuck
}

type imageWant struct {
	from  string
	tries int
	at    time.Time
}

type partial struct {
	n     int
	parts map[int][]byte
	size  int
	at    time.Time
}

type incoming struct {
	from string
	body json.RawMessage
}

// ShareService shares boards in the room. Bind it to the board windows and the
// Whiteboards page; Attach it to the room.
//
// JS binding name: ShareService.*
type ShareService struct {
	store *board.Store

	mu       sync.Mutex
	room     Room
	code     string // the room the state below belongs to
	hosting  map[string]*hosted
	offers   map[key]*offer
	mirrors  map[key]*mirror
	waiting  map[string]chan message
	parts    map[string]*partial // from + "/" + id
	images   map[string]*imageWant
	sentImgs map[string]bool // pictures in changes this Toru sent, which it may serve
	emit     func(name string, data any)
	pics     pictures

	in   chan incoming
	out  chan outgoing
	quit chan struct{}
	once sync.Once
}

// pictures is where board pictures are: board's ImagesDir, or a folder of
// each Toru's own in tests.
type pictures struct {
	has     func(asset string) bool
	read    func(asset string) ([]byte, error)
	import_ func(data []byte) (string, error)
}

var boardPictures = pictures{
	has: func(asset string) bool {
		path, err := board.ImagePath(asset)
		return err == nil && fileExists(path)
	},
	read: board.ReadImage,
	import_: func(data []byte) (string, error) {
		img, err := board.ImportImage(data)
		return img.Asset, err
	},
}

type outgoing struct {
	to  string
	msg message
}

// New is a Service over store, the one store windows and agents use.
func New(store *board.Store) *ShareService {
	return &ShareService{
		store:    store,
		hosting:  map[string]*hosted{},
		offers:   map[key]*offer{},
		mirrors:  map[key]*mirror{},
		waiting:  map[string]chan message{},
		parts:    map[string]*partial{},
		images:   map[string]*imageWant{},
		sentImgs: map[string]bool{},
		pics:     boardPictures,
		in:       make(chan incoming, queueLen),
		out:      make(chan outgoing, queueLen),
		quit:     make(chan struct{}),
	}
}

// Attach starts sharing over room. emit, if set, hears EventState.
// Messages from the room go to Receive.
//
//wails:ignore
func (s *ShareService) Attach(room Room, emit func(name string, data any)) {
	s.mu.Lock()
	s.room, s.emit = room, emit
	s.mu.Unlock()
	s.once.Do(func() {
		go s.receiveLoop()
		go s.sendLoop()
		go s.tickLoop()
	})
}

// Stop ends the loops.
//
//wails:ignore
func (s *ShareService) Stop() {
	select {
	case <-s.quit:
	default:
		close(s.quit)
	}
}

// Receive takes one board message from peer from. It never blocks: when
// the queue is full the message is dropped, and catching up fixes it.
//
//wails:ignore
func (s *ShareService) Receive(from string, body json.RawMessage) {
	select {
	case s.in <- incoming{from, append(json.RawMessage(nil), body...)}:
	default:
	}
}

// OnOp hears every op the store applies (chained from Store.OnOp). It
// sends ops on boards this Toru shares to the room. It runs under the
// store's lock, so it only queues.
//
//wails:ignore
func (s *ShareService) OnOp(id string, op board.Op) {
	if op.Kind == board.KindReset {
		return
	}
	s.mu.Lock()
	_, shared := s.hosting[id]
	s.mu.Unlock()
	if shared {
		s.send("", message{T: tOp, Board: id, Op: &op})
	}
}

// ── what the page, the window and agents see ───────────────────────────

// State is sharing as the Whiteboards page and agents see it.
type State struct {
	InRoom bool   `json:"inRoom"`
	Code   string `json:"code"`
	// Name is this person's name in the room.
	Name string `json:"name"`
	// Sharing is this Toru's boards shared in the room.
	Sharing []Shared `json:"sharing"`
	// Offered is every board others in the room share.
	Offered []Offered `json:"offered"`
}

// Shared is one of this Toru's boards shared in the room, and who has it
// open.
type Shared struct {
	Board   string   `json:"board"`
	Name    string   `json:"name"`
	Viewers []string `json:"viewers"`
}

// Offered is a board someone else in the room shares.
type Offered struct {
	Host     string `json:"host"`
	HostName string `json:"hostName"`
	Board    string `json:"board"`
	Name     string `json:"name"`
	// Local is this Toru's copy's id once joined, else "".
	Local string `json:"local"`
	// Live is true while the copy follows the host.
	Live bool `json:"live"`
}

// BoardStatus is one board's sharing, for its window.
type BoardStatus struct {
	InRoom bool   `json:"inRoom"`
	Code   string `json:"code"`
	// Sharing: this Toru shares the board; Viewers are who has it open.
	Sharing bool     `json:"sharing"`
	Viewers []string `json:"viewers"`
	// HostName is set on a copy of someone else's board; Live while it
	// follows them.
	HostName string `json:"hostName"`
	Live     bool   `json:"live"`
}

// GetState is sharing as it stands.
func (s *ShareService) GetState() State {
	s.mu.Lock()
	st := s.stateLocked()
	s.mu.Unlock()
	s.nameShares(&st)
	return st
}

// stateLocked is the state with this Toru's boards named by id; the store
// is never called under s.mu (its OnOp calls in under its own lock), so
// nameShares names them after.
func (s *ShareService) stateLocked() State {
	st := State{Sharing: []Shared{}, Offered: []Offered{}}
	if s.room != nil {
		_, st.Name, st.Code = s.room.RoomPeer()
		st.InRoom = st.Code != ""
	}
	for id, h := range s.hosting {
		st.Sharing = append(st.Sharing, Shared{Board: id, Name: id, Viewers: s.viewersLocked(h)})
	}
	for k, o := range s.offers {
		off := Offered{Host: k.host, HostName: s.nameLocked(k.host), Board: k.board, Name: o.Name}
		if m := s.mirrors[k]; m != nil {
			off.Local, off.Live = m.local, m.live
		}
		st.Offered = append(st.Offered, off)
	}
	sort.Slice(st.Offered, func(i, j int) bool {
		a, b := st.Offered[i], st.Offered[j]
		if a.HostName != b.HostName {
			return a.HostName < b.HostName
		}
		return a.Name < b.Name
	})
	return st
}

func (s *ShareService) nameShares(st *State) {
	for i := range st.Sharing {
		if b, err := s.store.Load(st.Sharing[i].Board); err == nil {
			st.Sharing[i].Name = b.Name
		}
	}
	sort.Slice(st.Sharing, func(i, j int) bool { return st.Sharing[i].Name < st.Sharing[j].Name })
}

// Status is one board's sharing.
func (s *ShareService) Status(id string) BoardStatus {
	s.mu.Lock()
	st := BoardStatus{Viewers: []string{}}
	if s.room != nil {
		_, _, st.Code = s.room.RoomPeer()
		st.InRoom = st.Code != ""
	}
	if h, ok := s.hosting[id]; ok {
		st.Sharing, st.Viewers = true, s.viewersLocked(h)
	}
	for _, m := range s.mirrors {
		if m.local == id {
			st.HostName, st.Live = m.hostName, m.live
			s.mu.Unlock()
			return st
		}
	}
	s.mu.Unlock()
	if b, err := s.store.Load(id); err == nil && b.MirrorOf != nil {
		st.HostName = b.MirrorOf.HostName
	}
	return st
}

func (s *ShareService) viewersLocked(h *hosted) []string {
	out := []string{}
	for p := range h.viewers {
		out = append(out, s.nameLocked(p))
	}
	sort.Strings(out)
	return out
}

func (s *ShareService) nameLocked(peer string) string {
	if s.room != nil {
		if n := s.room.HostName(peer); n != "" {
			return n
		}
	}
	return somebody
}

func (s *ShareService) changed() {
	s.mu.Lock()
	emit := s.emit
	st := s.stateLocked()
	s.mu.Unlock()
	s.nameShares(&st)
	if emit != nil {
		emit(EventState, st)
	}
}

// ── sharing a board ────────────────────────────────────────────────────

var (
	errNoRoom = errors.New("join a room first: everyone who should see the board joins the same room code")
	errMirror = errors.New("this is a copy of someone else's shared board: only they can share it")
)

// Share puts one of this Toru's boards in the room: everyone in the room
// can join it, see every change live and make changes, and so can each of
// their agents.
func (s *ShareService) Share(id string) (BoardStatus, error) {
	b, err := s.store.Load(id)
	if err != nil {
		return BoardStatus{}, err
	}
	if s.store.IsMirror(id) {
		return BoardStatus{}, errMirror
	}
	s.tick(time.Now()) // catch up with a room joined a moment ago
	s.mu.Lock()
	if s.room == nil || s.code == "" {
		s.mu.Unlock()
		return BoardStatus{}, errNoRoom
	}
	if _, ok := s.hosting[id]; !ok {
		s.hosting[id] = &hosted{viewers: map[string]time.Time{}}
	}
	s.mu.Unlock()
	_ = b.Name
	s.hello()
	s.changed()
	return s.Status(id), nil
}

// Unshare takes a board out of the room. Others keep the copy they have,
// which stops following this one.
func (s *ShareService) Unshare(id string) BoardStatus {
	s.mu.Lock()
	_, ok := s.hosting[id]
	delete(s.hosting, id)
	s.mu.Unlock()
	if ok {
		s.send("", message{T: tUnshare, Board: id})
		s.hello()
		s.changed()
	}
	return s.Status(id)
}

// Join makes a live copy of a board someone in the room shares, and
// returns the copy's id. Changes made to the copy, by the person or their
// agents, go to the host; the host's come back. host is the sharer's peer
// id (GetState lists them); with host "" the only board shared under that
// id is joined.
func (s *ShareService) Join(host, boardID string) (string, error) {
	s.tick(time.Now()) // catch up with a room joined a moment ago
	s.mu.Lock()
	if s.room == nil || s.code == "" {
		s.mu.Unlock()
		return "", errNoRoom
	}
	var k key
	var found bool
	for kk := range s.offers {
		if kk.board == boardID && (host == "" || kk.host == host) {
			if found {
				s.mu.Unlock()
				return "", fmt.Errorf("more than one person shares a board %q: say whose (host)", boardID)
			}
			k, found = kk, true
		}
	}
	if !found {
		s.mu.Unlock()
		return "", fmt.Errorf("nobody in the room shares a board %q", boardID)
	}
	if m := s.mirrors[k]; m != nil {
		joined := m.joined
		s.mu.Unlock()
		return s.waitJoined(k, joined)
	}
	hostName := s.nameLocked(k.host)
	s.mu.Unlock()

	local, err := s.localID(k)
	if err != nil {
		return "", err
	}
	m := &mirror{key: k, local: local, hostName: hostName, joined: make(chan struct{}), buffered: map[int]board.Op{}}
	s.mu.Lock()
	if old := s.mirrors[k]; old != nil {
		joined := old.joined
		s.mu.Unlock()
		return s.waitJoined(k, joined)
	}
	s.mirrors[k] = m
	m.snapAt = time.Now()
	s.mu.Unlock()
	s.send(k.host, message{T: tJoin, Board: k.board})
	s.hello()
	return s.waitJoined(k, m.joined)
}

func (s *ShareService) waitJoined(k key, joined chan struct{}) (string, error) {
	select {
	case <-joined:
	case <-time.After(joinTimeout):
		s.mu.Lock()
		m := s.mirrors[k]
		if m != nil && !m.live {
			delete(s.mirrors, k)
		}
		name := s.nameLocked(k.host)
		s.mu.Unlock()
		return "", fmt.Errorf("%s's Toru didn't send the board: check they're still in the room and sharing it", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m := s.mirrors[k]
	if m == nil || !m.live {
		return "", errors.New("the board stopped being shared")
	}
	return m.local, nil
}

// localID is the id this Toru keeps its copy of k under: the copy it
// already has, else the host's id if it's free here, else a new one. A
// copy never takes the id of a board of this Toru's own.
func (s *ShareService) localID(k key) (string, error) {
	list, err := s.store.List()
	if err != nil {
		return "", err
	}
	taken := map[string]bool{}
	for _, b := range list {
		if b.MirrorOf != nil && b.MirrorOf.Host == k.host && b.MirrorOf.Board == k.board {
			return b.ID, nil
		}
		taken[b.ID] = true
	}
	s.mu.Lock()
	for _, m := range s.mirrors {
		taken[m.local] = true
	}
	s.mu.Unlock()
	if !taken[k.board] {
		return k.board, nil
	}
	for {
		id := "brd-" + randHex(8)
		if !taken[id] {
			return id, nil
		}
	}
}

// Leave stops a copy following its host. The copy stays on this Toru.
func (s *ShareService) Leave(id string) BoardStatus {
	s.mu.Lock()
	var gone *mirror
	for k, m := range s.mirrors {
		if m.local == id {
			gone = m
			delete(s.mirrors, k)
		}
	}
	s.mu.Unlock()
	if gone != nil {
		s.store.SetRemote(id, nil)
		s.hello()
		s.changed()
		s.reset(id)
	}
	return s.Status(id)
}

// reset tells open windows of board id to load it again (it stopped or
// started following a host).
func (s *ShareService) reset(id string) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit("board:shared", map[string]any{"board": id})
	}
}

// ── the loops ──────────────────────────────────────────────────────────

func (s *ShareService) receiveLoop() {
	for {
		select {
		case <-s.quit:
			return
		case m := <-s.in:
			s.handle(m.from, m.body)
		}
	}
}

func (s *ShareService) sendLoop() {
	for {
		select {
		case <-s.quit:
			return
		case o := <-s.out:
			s.mu.Lock()
			room := s.room
			s.mu.Unlock()
			if room != nil {
				s.write(room, o.to, o.msg)
			}
		}
	}
}

// write sends one message, in parts when it's big.
func (s *ShareService) write(room Room, to string, m message) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if len(data) <= partSize {
		_ = room.SendBoard(to, data)
		return
	}
	if len(data) > maxWhole {
		return
	}
	id := randHex(8)
	n := (len(data) + partSize - 1) / partSize
	for i := 0; i < n; i++ {
		part := data[i*partSize : min((i+1)*partSize, len(data))]
		raw, err := json.Marshal(message{T: tPart, ID: id, I: i, N: n, Part: part})
		if err != nil {
			return
		}
		_ = room.SendBoard(to, raw)
	}
}

func (s *ShareService) send(to string, m message) {
	select {
	case s.out <- outgoing{to, m}:
	default: // full: catching up fixes it
	}
}

func (s *ShareService) tickLoop() {
	tick := time.NewTicker(tickEvery)
	defer tick.Stop()
	lastHello := time.Time{}
	for {
		select {
		case <-s.quit:
			return
		case now := <-tick.C:
			s.tick(now)
			if now.Sub(lastHello) >= helloEvery {
				lastHello = now
				s.hello()
			}
		}
	}
}

// tick notices a room change, hosts gone quiet, viewers gone, gaps that
// didn't fill, and pictures still missing.
func (s *ShareService) tick(now time.Time) {
	s.mu.Lock()
	code := ""
	if s.room != nil {
		_, _, code = s.room.RoomPeer()
	}
	var detach []*mirror
	var snaps []key
	var imgs []outgoing
	roomChanged := code != s.code
	if roomChanged {
		// A new room (or none): nothing shared carries over.
		s.code = code
		for k, m := range s.mirrors {
			detach = append(detach, m)
			delete(s.mirrors, k)
		}
		s.hosting = map[string]*hosted{}
		s.offers = map[key]*offer{}
	}
	for k, o := range s.offers {
		if now.Sub(o.seen) > offerTimeout {
			delete(s.offers, k)
			if m := s.mirrors[k]; m != nil {
				detach = append(detach, m)
				delete(s.mirrors, k)
			}
		}
	}
	for _, h := range s.hosting {
		for p, t := range h.viewers {
			if now.Sub(t) > offerTimeout {
				delete(h.viewers, p)
			}
		}
	}
	for k, m := range s.mirrors {
		if !m.gapSince.IsZero() && now.Sub(m.gapSince) > gapWait && now.Sub(m.snapAt) > snapAgain {
			m.snapAt = now
			snaps = append(snaps, k)
		}
		if !m.live && now.Sub(m.snapAt) > snapAgain {
			m.snapAt = now
			snaps = append(snaps, k)
		}
	}
	for asset, w := range s.images {
		if now.Sub(w.at) < imageAgain {
			continue
		}
		if w.tries >= imageTries {
			delete(s.images, asset)
			continue
		}
		w.tries++
		w.at = now
		imgs = append(imgs, outgoing{w.from, message{T: tImgReq, Asset: asset}})
	}
	for id, p := range s.parts {
		if now.Sub(p.at) > partTimeout {
			delete(s.parts, id)
		}
	}
	s.mu.Unlock()
	for _, m := range detach {
		s.store.SetRemote(m.local, nil)
		s.reset(m.local)
	}
	for _, k := range snaps {
		s.send(k.host, message{T: tJoin, Board: k.board})
	}
	for _, o := range imgs {
		s.send(o.to, o.msg)
	}
	if roomChanged || len(detach) > 0 {
		s.changed()
	}
}

// hello tells the room what this Toru shares and has joined.
func (s *ShareService) hello() {
	s.mu.Lock()
	if s.room == nil || s.code == "" {
		s.mu.Unlock()
		return
	}
	ids := make([]string, 0, len(s.hosting))
	for id := range s.hosting {
		ids = append(ids, id)
	}
	var joined []joinRef
	for k := range s.mirrors {
		joined = append(joined, joinRef{Host: k.host, Board: k.board})
	}
	s.mu.Unlock()
	sort.Strings(ids)
	m := message{T: tHello, Joined: joined}
	for _, id := range ids {
		if len(m.Shares) == maxSharesSent {
			break
		}
		b, err := s.store.Load(id)
		if err != nil {
			continue
		}
		m.Shares = append(m.Shares, shareInfo{Board: id, Name: b.Name, Rev: b.Rev})
	}
	s.send("", m)
}

// ── handling messages ──────────────────────────────────────────────────

func (s *ShareService) handle(from string, body json.RawMessage) {
	var m message
	if err := json.Unmarshal(body, &m); err != nil {
		return
	}
	if m.T == tPart {
		whole := s.putPart(from, m)
		if whole == nil {
			return
		}
		m = message{}
		if err := json.Unmarshal(whole, &m); err != nil || m.T == tPart {
			return
		}
	}
	s.mu.Lock()
	stale := false
	if s.room != nil {
		_, _, code := s.room.RoomPeer()
		stale = code != s.code
	}
	s.mu.Unlock()
	if stale {
		s.tick(time.Now()) // a room joined a moment ago
	}
	switch m.T {
	case tHello:
		s.onHello(from, m)
	case tJoin:
		s.onJoin(from, m)
	case tSnap:
		s.onSnap(from, m)
	case tOp:
		s.onOp(from, m)
	case tReq:
		s.onReq(from, m)
	case tRes:
		s.mu.Lock()
		ch := s.waiting[m.Req]
		delete(s.waiting, m.Req)
		s.mu.Unlock()
		if ch != nil {
			ch <- m
		}
	case tUnshare:
		s.mu.Lock()
		k := key{from, m.Board}
		delete(s.offers, k)
		mi := s.mirrors[k]
		delete(s.mirrors, k)
		s.mu.Unlock()
		if mi != nil {
			s.store.SetRemote(mi.local, nil)
			s.reset(mi.local)
		}
		s.changed()
	case tImgReq:
		s.onImageReq(from, m)
	case tImg:
		s.onImage(m)
	}
}

func (s *ShareService) putPart(from string, m message) []byte {
	if m.N < 2 || m.N > maxWhole/partSize+1 || m.I < 0 || m.I >= m.N || len(m.Part) > partSize || m.ID == "" || len(m.ID) > 32 {
		return nil
	}
	id := from + "/" + m.ID
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.parts[id]
	if p == nil {
		n := 0
		for k := range s.parts {
			if strings.HasPrefix(k, from+"/") {
				n++
			}
		}
		if n >= maxPartials {
			return nil
		}
		p = &partial{n: m.N, parts: map[int][]byte{}, at: time.Now()}
		s.parts[id] = p
	}
	if p.n != m.N {
		return nil
	}
	if _, dup := p.parts[m.I]; dup {
		return nil
	}
	p.parts[m.I] = m.Part
	p.size += len(m.Part)
	if p.size > maxWhole {
		delete(s.parts, id)
		return nil
	}
	if len(p.parts) < p.n {
		return nil
	}
	delete(s.parts, id)
	whole := make([]byte, 0, p.size)
	for i := 0; i < p.n; i++ {
		whole = append(whole, p.parts[i]...)
	}
	return whole
}

func (s *ShareService) onHello(from string, m message) {
	now := time.Now()
	s.mu.Lock()
	if s.code == "" {
		s.mu.Unlock()
		return
	}
	self := ""
	if s.room != nil {
		self, _, _ = s.room.RoomPeer()
	}
	offered := map[string]bool{}
	changed := false
	for _, sh := range m.Shares {
		if !board.ValidID(sh.Board) {
			continue
		}
		offered[sh.Board] = true
		k := key{from, sh.Board}
		o := s.offers[k]
		if o == nil {
			o = &offer{}
			s.offers[k] = o
			changed = true
		}
		if o.Name != sh.Name {
			changed = true
		}
		o.shareInfo, o.seen = sh, now
	}
	var detach []*mirror
	var snaps []key
	for k := range s.offers {
		if k.host == from && !offered[k.board] {
			delete(s.offers, k)
			changed = true
			if mi := s.mirrors[k]; mi != nil {
				detach = append(detach, mi)
				delete(s.mirrors, k)
			}
		}
	}
	for _, j := range m.Joined {
		if j.Host != self {
			continue
		}
		if h := s.hosting[j.Board]; h != nil {
			if _, ok := h.viewers[from]; !ok {
				changed = true
			}
			h.viewers[from] = now
		}
	}
	// A copy that's behind the host's rev and hasn't moved since the last
	// hello lost an op off the end: ask for the board.
	type check struct {
		k   key
		m   *mirror
		rev int
	}
	var checks []check
	for k, mi := range s.mirrors {
		if k.host == from && mi.live {
			if o := s.offers[k]; o != nil {
				checks = append(checks, check{k, mi, o.Rev})
			}
		}
	}
	s.mu.Unlock()
	for _, c := range checks {
		have := s.store.Rev(c.m.local)
		s.mu.Lock()
		if have < c.rev && have == c.m.lastRev && time.Since(c.m.snapAt) > snapAgain {
			c.m.snapAt = time.Now()
			snaps = append(snaps, c.k)
		}
		c.m.lastRev = have
		s.mu.Unlock()
	}
	for _, mi := range detach {
		s.store.SetRemote(mi.local, nil)
		s.reset(mi.local)
	}
	for _, k := range snaps {
		s.send(k.host, message{T: tJoin, Board: k.board})
	}
	if changed || len(detach) > 0 {
		s.changed()
	}
}

// onJoin sends a shared board whole to a member who asked.
func (s *ShareService) onJoin(from string, m message) {
	s.mu.Lock()
	_, shared := s.hosting[m.Board]
	s.mu.Unlock()
	if !shared {
		return
	}
	b, err := s.store.Load(m.Board)
	if err != nil {
		return
	}
	data, err := board.Encode(b)
	if err != nil {
		return
	}
	s.mu.Lock()
	if h := s.hosting[m.Board]; h != nil {
		h.viewers[from] = time.Now()
	}
	s.mu.Unlock()
	s.send(from, message{T: tSnap, Board: m.Board, Snap: data})
	s.changed()
}

// onSnap saves a host's whole board as this Toru's copy.
func (s *ShareService) onSnap(from string, m message) {
	k := key{from, m.Board}
	s.mu.Lock()
	mi := s.mirrors[k]
	myName := ""
	if s.room != nil {
		_, myName, _ = s.room.RoomPeer()
	}
	s.mu.Unlock()
	if mi == nil {
		return
	}
	b, err := board.Decode(m.Snap)
	if err != nil || b.ID != m.Board {
		return
	}
	hostName := s.hostName(from)
	b.MapSources(toLocal(hostName, myName))
	b.ID = mi.local
	b.MirrorOf = &board.MirrorOf{Host: from, HostName: hostName, Board: m.Board}
	if _, err := s.store.Mirror(b); err != nil {
		return
	}
	s.store.SetRemote(mi.local, &remote{s: s, k: k})
	s.mu.Lock()
	first := !mi.live
	mi.live, mi.hostName, mi.gapSince = true, hostName, time.Time{}
	for rev := range mi.buffered {
		if rev <= b.Rev {
			delete(mi.buffered, rev)
		}
	}
	if first {
		close(mi.joined)
	}
	s.mu.Unlock()
	s.drain(mi)
	s.wantImages(from, board.Assets(b))
	s.changed()
}

// onOp applies a host's op to this Toru's copy.
func (s *ShareService) onOp(from string, m message) {
	if m.Op == nil {
		return
	}
	k := key{from, m.Board}
	s.mu.Lock()
	mi := s.mirrors[k]
	s.mu.Unlock()
	if mi == nil {
		return
	}
	op := *m.Op
	s.localize(&op, from)
	s.mu.Lock()
	if !mi.live {
		mi.buffered[op.Rev] = op
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	s.applyMirror(mi, op)
	s.wantImages(from, opAssets(op))
}

// applyMirror applies one op to a copy, buffering it past a gap, then any
// buffered ops that now follow.
func (s *ShareService) applyMirror(mi *mirror, op board.Op) {
	if !s.store.IsMirror(mi.local) {
		// The copy was deleted here (board.delete, the Whiteboards page):
		// stop following.
		s.mu.Lock()
		if s.mirrors[mi.key] == mi && mi.live {
			delete(s.mirrors, mi.key)
		}
		s.mu.Unlock()
		return
	}
	_, err := s.store.ApplyMirror(mi.local, op)
	if errors.Is(err, board.ErrGap) {
		s.mu.Lock()
		mi.buffered[op.Rev] = op
		if mi.gapSince.IsZero() {
			mi.gapSince = time.Now()
		}
		s.mu.Unlock()
		return
	}
	if err != nil {
		// The copy and the host disagree: start again from the host's board.
		s.mu.Lock()
		mi.gapSince = time.Now().Add(-gapWait)
		mi.snapAt = time.Time{}
		s.mu.Unlock()
		return
	}
	s.drain(mi)
}

// drain applies buffered ops that follow the copy's rev.
func (s *ShareService) drain(mi *mirror) {
	for {
		rev := s.store.Rev(mi.local)
		s.mu.Lock()
		for r := range mi.buffered {
			if r <= rev {
				delete(mi.buffered, r)
			}
		}
		next, ok := mi.buffered[rev+1]
		if ok {
			delete(mi.buffered, rev+1)
		}
		if len(mi.buffered) == 0 {
			mi.gapSince = time.Time{}
		}
		s.mu.Unlock()
		if !ok {
			return
		}
		if _, err := s.store.ApplyMirror(mi.local, next); err != nil {
			return
		}
	}
}

// onReq makes a change a member sent for a board this Toru shares.
func (s *ShareService) onReq(from string, m message) {
	reply := message{T: tRes, Req: m.Req}
	if m.Req == "" || len(m.Req) > 32 {
		return
	}
	s.mu.Lock()
	_, shared := s.hosting[m.Board]
	s.mu.Unlock()
	if !shared {
		reply.Err = "that board isn't shared any more"
		s.send(from, reply)
		return
	}
	name := s.hostName(from)
	// Who made it comes from who sent it, never from the request: their
	// person is "person:<name>", their agents "agent:<agent> (<name>)".
	var src board.Source
	switch {
	case m.Src == board.Person:
		src = board.PersonNamed(name)
	case m.Src.IsAgent() && m.Src.Check() == nil:
		src = board.AgentNamed(m.Src.Name() + " (" + name + ")")
	default:
		reply.Err = "bad source"
		s.send(from, reply)
		return
	}
	var op board.Op
	var err error
	switch m.Action {
	case "apply":
		if m.Op == nil {
			reply.Err = "no change sent"
			s.send(from, reply)
			return
		}
		in := *m.Op
		in.Source, in.Rev, in.At, in.Kind = src, 0, time.Time{}, ""
		op, err = s.store.Apply(m.Board, in)
	case "undo":
		op, err = s.store.Undo(m.Board, src)
	case "redo":
		op, err = s.store.Redo(m.Board, src)
	default:
		reply.Err = "unknown action"
		s.send(from, reply)
		return
	}
	switch {
	case errors.Is(err, board.ErrNothingToUndo), errors.Is(err, board.ErrNothingToRedo):
		reply.None = true
	case err != nil:
		reply.Err = err.Error()
	default:
		reply.Op = &op
	}
	s.send(from, reply)
	if err == nil {
		s.wantImages(from, opAssets(op))
	}
}

// ── a copy's way to its host ───────────────────────────────────────────

type remote struct {
	s *ShareService
	k key
}

func (r *remote) Apply(op board.Op) (board.Op, error) {
	return r.s.request(r.k, "apply", &op, op.Source)
}
func (r *remote) Undo(src board.Source) (board.Op, error) {
	op, err := r.s.request(r.k, "undo", nil, src)
	if errors.Is(err, errNone) {
		return board.Op{}, board.ErrNothingToUndo
	}
	return op, err
}
func (r *remote) Redo(src board.Source) (board.Op, error) {
	op, err := r.s.request(r.k, "redo", nil, src)
	if errors.Is(err, errNone) {
		return board.Op{}, board.ErrNothingToRedo
	}
	return op, err
}

var errNone = errors.New("nothing to do")

// request sends a change to the host and waits for it as applied, then for
// the copy to have it.
func (s *ShareService) request(k key, action string, op *board.Op, src board.Source) (board.Op, error) {
	if src == "" {
		src = board.Person
	}
	if err := src.Check(); err != nil {
		return board.Op{}, err
	}
	if src != board.Person && !src.IsAgent() {
		return board.Op{}, errors.New("bad source")
	}
	id := randHex(8)
	ch := make(chan message, 1)
	s.mu.Lock()
	mi := s.mirrors[k]
	if mi == nil || !mi.live {
		s.mu.Unlock()
		return board.Op{}, errors.New("this board isn't shared any more")
	}
	s.waiting[id] = ch
	hostName := mi.hostName
	s.mu.Unlock()
	if op != nil {
		for _, a := range opAssets(*op) {
			s.mu.Lock()
			s.sentImgs[a] = true
			s.mu.Unlock()
		}
	}
	s.send(k.host, message{T: tReq, Board: k.board, Req: id, Action: action, Op: op, Src: src})
	var res message
	select {
	case res = <-ch:
	case <-time.After(reqTimeout):
		s.mu.Lock()
		delete(s.waiting, id)
		s.mu.Unlock()
		return board.Op{}, fmt.Errorf("%s's Toru didn't answer: the change may not have been made", hostName)
	}
	switch {
	case res.None:
		return board.Op{}, errNone
	case res.Err != "":
		return board.Op{}, fmt.Errorf("%s's Toru refused it: %s", hostName, res.Err)
	case res.Op == nil:
		return board.Op{}, errors.New("no answer")
	}
	got := *res.Op
	s.localize(&got, k.host)
	s.applyMirror(mi, got)
	// The copy may be waiting on ops before this one: give them a moment,
	// so whoever made the change reads the board with it.
	deadline := time.Now().Add(3 * time.Second)
	for s.store.Rev(mi.local) < got.Rev && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	return got, nil
}

// ── who made what ──────────────────────────────────────────────────────

// localize turns an op as the host has it into this Toru's view.
func (s *ShareService) localize(op *board.Op, host string) {
	s.mu.Lock()
	myName := ""
	if s.room != nil {
		_, myName, _ = s.room.RoomPeer()
	}
	s.mu.Unlock()
	op.MapSources(toLocal(s.hostName(host), myName))
}

// toLocal maps who made what from the host's view to this Toru's: the
// host's own person is them by name, and this Toru's person, whom the host
// knows by name, is "person" again (shown as "You").
func toLocal(hostName, myName string) func(board.Source) board.Source {
	me := board.PersonNamed(myName)
	host := board.PersonNamed(hostName)
	return func(src board.Source) board.Source {
		switch src {
		case board.Person:
			return host
		case me:
			return board.Person
		}
		return src
	}
}

func (s *ShareService) hostName(peer string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nameLocked(peer)
}

// ── pictures ───────────────────────────────────────────────────────────

func opAssets(op board.Op) []string {
	return board.Assets(board.Board{Blocks: op.Blocks})
}

// wantImages asks peer for the pictures in assets this Toru lacks.
func (s *ShareService) wantImages(peer string, assets []string) {
	for _, a := range assets {
		if s.pics.has(a) {
			continue
		}
		s.mu.Lock()
		_, asked := s.images[a]
		if !asked {
			s.images[a] = &imageWant{from: peer, at: time.Now()}
		}
		s.mu.Unlock()
		if !asked {
			s.send(peer, message{T: tImgReq, Asset: a})
		}
	}
}

// onImageReq sends a picture a shared board (or a change this Toru sent)
// shows, to a member who asked.
func (s *ShareService) onImageReq(from string, m message) {
	if !s.mayServe(m.Asset) {
		s.send(from, message{T: tImg, Asset: m.Asset, Missing: true})
		return
	}
	data, err := s.pics.read(m.Asset)
	if err != nil {
		s.send(from, message{T: tImg, Asset: m.Asset, Missing: true})
		return
	}
	s.send(from, message{T: tImg, Asset: m.Asset, Data: data})
}

func (s *ShareService) mayServe(asset string) bool {
	s.mu.Lock()
	ids := []string{}
	for id := range s.hosting {
		ids = append(ids, id)
	}
	for _, m := range s.mirrors {
		ids = append(ids, m.local)
	}
	sent := s.sentImgs[asset]
	s.mu.Unlock()
	if sent {
		return true
	}
	for _, id := range ids {
		b, err := s.store.Load(id)
		if err != nil {
			continue
		}
		for _, a := range board.Assets(b) {
			if a == asset {
				return true
			}
		}
	}
	return false
}

func (s *ShareService) onImage(m message) {
	s.mu.Lock()
	w := s.images[m.Asset]
	s.mu.Unlock()
	if w == nil || m.Missing || len(m.Data) == 0 {
		return // asked again on a tick
	}
	asset, err := s.pics.import_(m.Data)
	if err != nil || asset != m.Asset {
		return
	}
	s.mu.Lock()
	delete(s.images, m.Asset)
	emit := s.emit
	s.mu.Unlock()
	if emit != nil {
		emit("board:image", map[string]any{"asset": m.Asset})
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
