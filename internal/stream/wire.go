package stream

// Room message types and bodies.
const (
	msgPresence = "presence" // everyone, every presenceEvery: who I am, am I live
	msgLeave    = "leave"    // everyone: I left the room
	msgWatch    = "watch"    // viewer → streamer: send me your stream
	msgOffer    = "offer"    // streamer → viewer: WebRTC offer
	msgAnswer   = "answer"   // viewer → streamer: WebRTC answer
	msgUnwatch  = "unwatch"  // viewer → streamer: I closed the stream
	msgRestart  = "restart"  // streamer → viewer: stream changed shape, watch again
	msgEnded    = "ended"    // streamer → viewer: I stopped streaming
	msgNotLive  = "notlive"  // streamer → viewer: answer to a watch while not live
	// msgLan carries one LAN signaling blob (api.SignalBlob) between the LAN
	// services of two members, to one peer or everyone. The body is the blob
	// itself; this side never parses it (lan.go).
	msgLan = "lan"
)

type presenceBody struct {
	Name     string    `json:"name"`
	Live     *LiveInfo `json:"live,omitempty"`
	InCall   bool      `json:"inCall,omitempty"`
	Muted    bool      `json:"muted,omitempty"`
	Deafened bool      `json:"deafened,omitempty"`
	Lan      *LanInfo  `json:"lan,omitempty"`
}

// LanInfo is a member's LAN as their presence announces it: their overlay
// address and WireGuard public key (both from their LAN service), and
// whether LAN is on right now. nil = no LAN service known (never set up, or
// an older Toru).
type LanInfo struct {
	IP    string `json:"ip"`
	WGPub string `json:"wgPub"`
	On    bool   `json:"on"`
}

type watchBody struct {
	Session string `json:"session"`
	Name    string `json:"name"`
}

type offerBody struct {
	Session string   `json:"session"`
	SDP     string   `json:"sdp"`
	Info    LiveInfo `json:"info"`
}

type answerBody struct {
	Session string `json:"session"`
	SDP     string `json:"sdp"`
}

type restartBody struct {
	Session string `json:"session"`
}
