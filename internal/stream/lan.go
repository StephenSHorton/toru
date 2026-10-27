package stream

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
)

// lan.go is the room side of LAN (docs/lan.md): the LAN service never sees
// the room, so its signaling rides here as msgLan, exactly like the call
// window's SDP rides as msgCall. The GUI relays blobs without reading them,
// turns members that leave or time out into Drop, and watches presence for
// two members on the same overlay address.

// lanLink is what the room talks to on the LAN side: LanService, or a fake
// in tests. Methods are unexported so Wails doesn't bind them.
type lanLink interface {
	signal(peer string, blob json.RawMessage)  // a msgLan from peer, for the service
	drop(peer string)                          // peer left the room
	expired(peer string)                       // peer timed out of presence (it may come back)
	memberSeen(peer string, lanOn, fresh bool) // presence from peer; fresh = new, or just turned LAN on
	readdress(salt uint32)                     // our address collides; re-derive it with salt
	roomChanged(room string)                   // joined room (its hash), or "" = left
	roomConnected()                            // a broker (re)connected
	iceChanged()                               // the ICE list changed (fresh TURN credentials)
}

// readdressGap is how long after a Readdress the same collision is left
// alone, while the service re-derives and presence catches up.
const readdressGap = 30 * time.Second

// maxLanBlob bounds one msgLan body (a candidate or credentials are ~200 bytes).
const maxLanBlob = 8 << 10

var overlayPrefix = netip.MustParsePrefix(api.OverlayPrefix)

var (
	errBadLanBlob = errors.New("bad LAN signaling message")
	errNotInRoom  = errors.New("not in a room")
)

// roomHash is the opaque room id the LAN service gets in Up: a hash of the
// room's topic, so it can tell rooms apart without learning the code.
func roomHash(topic string) string {
	sum := sha256.Sum256([]byte("toru-lan-room:" + topic))
	return hex.EncodeToString(sum[:8])
}

// cleanLanInfo validates a member's announced LanInfo: an address inside the
// overlay and a key of sane length, or nil.
func cleanLanInfo(in *LanInfo) *LanInfo {
	if in == nil || len(in.WGPub) > 64 {
		return nil
	}
	ip, err := netip.ParseAddr(in.IP)
	if err != nil || !ip.Is4() || !overlayPrefix.Contains(ip) {
		return nil
	}
	out := *in
	out.IP = ip.String()
	return &out
}

// keyLess orders WireGuard public keys by their bytes (base64 text doesn't
// sort the same way); the lower key keeps a contested address.
func keyLess(a, b string) bool {
	ab, errA := base64.StdEncoding.DecodeString(a)
	bb, errB := base64.StdEncoding.DecodeString(b)
	if errA != nil || errB != nil {
		return a < b
	}
	return bytes.Compare(ab, bb) < 0
}

// setLan connects the room to the LAN side.
func (s *StreamService) setLan(l lanLink) {
	s.mu.Lock()
	s.lan = l
	s.mu.Unlock()
}

// setLanInfo is what our presence says about our LAN (nil = nothing to
// say); it is announced at once when it changes.
func (s *StreamService) setLanInfo(info *LanInfo) {
	s.mu.Lock()
	same := (s.lanSelf == nil && info == nil) || (s.lanSelf != nil && info != nil && *s.lanSelf == *info)
	if info != nil {
		cp := *info
		info = &cp
	}
	s.lanSelf = info
	s.mu.Unlock()
	if !same {
		s.announce()
		s.emitState()
	}
}

// lanSend sends one service blob to peer ("" = everyone in the room).
func (s *StreamService) lanSend(peer string, blob json.RawMessage) error {
	if len(blob) == 0 || len(blob) > maxLanBlob || !json.Valid(blob) {
		return errBadLanBlob
	}
	if peer == s.self {
		return nil // the service never talks to itself
	}
	s.mu.Lock()
	room := s.room
	s.mu.Unlock()
	if room == nil {
		return errNotInRoom
	}
	return room.Send(peer, msgLan, blob)
}

// onLanMessage hands a msgLan to the LAN side.
func (s *StreamService) onLanMessage(env Envelope) {
	if len(env.Body) == 0 || len(env.Body) > maxLanBlob || !json.Valid(env.Body) {
		return
	}
	s.mu.Lock()
	l := s.lan
	s.mu.Unlock()
	if l != nil {
		l.signal(env.From, append(json.RawMessage(nil), env.Body...))
	}
}

// lanDropped tells the LAN side these members left.
func (s *StreamService) lanDropped(ids ...string) {
	s.mu.Lock()
	l := s.lan
	s.mu.Unlock()
	if l == nil {
		return
	}
	for _, id := range ids {
		l.drop(id)
	}
}

// lanExpired tells the LAN side these members timed out of presence.
func (s *StreamService) lanExpired(ids ...string) {
	s.mu.Lock()
	l := s.lan
	s.mu.Unlock()
	if l == nil {
		return
	}
	for _, id := range ids {
		l.expired(id)
	}
}

// lanRoomConnected tells the LAN side a broker (re)connected.
func (s *StreamService) lanRoomConnected() {
	s.mu.Lock()
	l := s.lan
	s.mu.Unlock()
	if l != nil {
		l.roomConnected()
	}
}

// isMember reports whether id is in the room's presence right now.
func (s *StreamService) isMember(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.members[id] != nil
}

// lanRoomChanged tells the LAN side which room we're in now ("" = none).
func (s *StreamService) lanRoomChanged(room string) {
	s.mu.Lock()
	l := s.lan
	s.mu.Unlock()
	if l != nil {
		l.roomChanged(room)
	}
}

// lanCollisionLocked checks one member's LanInfo against ours: when both
// are on with the same address and their key is lower, ours must move. It
// returns the salt to send (0 = nothing to do) and records it. s.mu held.
func (s *StreamService) lanCollisionLocked(other *LanInfo) uint32 {
	ours := s.lanSelf
	if ours == nil || other == nil || !ours.On || !other.On || ours.IP == "" || other.IP != ours.IP {
		return 0
	}
	if other.WGPub == ours.WGPub || !keyLess(other.WGPub, ours.WGPub) {
		return 0 // they move (their Toru sees the mirror image), or it's us
	}
	now := time.Now()
	if s.readdressIP == ours.IP && now.Sub(s.readdressAt) < readdressGap {
		return 0
	}
	s.readdressIP, s.readdressAt = ours.IP, now
	s.settings.LanSalt++
	return s.settings.LanSalt
}

// roomTopicHash is the current room's hash for the LAN service ("" = no room).
func (s *StreamService) roomTopicHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.room == nil {
		return ""
	}
	return roomHash(s.room.topic)
}
