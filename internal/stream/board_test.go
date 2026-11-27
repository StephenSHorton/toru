package stream

import (
	"encoding/json"
	"testing"
	"time"
)

// Board messages reach the board handler only from members heard from in
// presence, are never parsed here, and can't be sent outside a room.
func TestBoardMessages(t *testing.T) {
	s := newTestService(t)
	var got []string
	s.SetBoardHandler(func(from string, body json.RawMessage) { got = append(got, from+" "+string(body)) })

	body := json.RawMessage(`{"t":"hello"}`)
	s.onMessage(Envelope{V: 1, ID: "1", From: "stranger", Type: msgBoard, At: time.Now().UnixMilli(), Body: body})
	if len(got) != 0 {
		t.Fatalf("a board message from someone not in the room got through: %v", got)
	}
	s.mu.Lock()
	s.members["friend"] = &member{Member: Member{ID: "friend", Name: "Blake"}, seen: time.Now()}
	s.mu.Unlock()
	s.onMessage(Envelope{V: 1, ID: "2", From: "friend", Type: msgBoard, At: time.Now().UnixMilli(), Body: body})
	if len(got) != 1 || got[0] != `friend {"t":"hello"}` {
		t.Fatalf("got %v", got)
	}
	big := make(json.RawMessage, maxBoardMsg+1)
	s.onMessage(Envelope{V: 1, ID: "3", From: "friend", Type: msgBoard, At: time.Now().UnixMilli(), Body: big})
	if len(got) != 1 {
		t.Fatal("an oversized board message got through")
	}
	if err := s.SendBoard("", body); err == nil {
		t.Fatal("SendBoard outside a room succeeded")
	}
	if self, _, code := s.RoomPeer(); self == "" || code != "" {
		t.Fatalf("RoomPeer = %q, code %q", self, code)
	}
}
