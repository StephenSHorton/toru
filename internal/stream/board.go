package stream

import "encoding/json"

// board.go carries shared whiteboards (internal/boardshare,
// docs/whiteboard.md section 2) through the room, the way lan.go carries
// LAN signaling: as msgBoard messages whose bodies this side never reads.
// Board sync opens no socket of its own and no port: everything rides the
// sealed room every Toru in the room already has.

// maxBoardMsg bounds one msgBoard body. boardshare splits anything bigger
// into parts well under it.
const maxBoardMsg = 256 << 10

// SetBoardHandler sets what hears every msgBoard from another member: the
// sender's peer id and the body as sent.
//
//wails:ignore
func (s *StreamService) SetBoardHandler(fn func(from string, body json.RawMessage)) {
	s.mu.Lock()
	s.onBoard = fn
	s.mu.Unlock()
}

// SendBoard sends one msgBoard body to peer to ("" = everyone in the room).
//
//wails:ignore
func (s *StreamService) SendBoard(to string, body json.RawMessage) error {
	if len(body) > maxBoardMsg {
		return errBoardTooBig
	}
	s.mu.Lock()
	room := s.room
	s.mu.Unlock()
	if room == nil {
		return errNotInRoom
	}
	return room.Send(to, msgBoard, body)
}

// RoomPeer is this install's peer id, its name in the room, and the room's
// code ("" when not in a room).
//
//wails:ignore
func (s *StreamService) RoomPeer() (self, name, code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.room != nil {
		code = s.room.Code()
	}
	return s.self, s.settings.Name, code
}

func (s *StreamService) onBoardMessage(env Envelope) {
	if len(env.Body) > maxBoardMsg {
		return
	}
	s.mu.Lock()
	fn := s.onBoard
	_, member := s.members[env.From]
	s.mu.Unlock()
	if fn == nil || !member {
		// Only members we've heard presence from: a name to show, and a
		// peer that has to keep announcing itself to stay.
		return
	}
	fn(env.From, env.Body)
}
