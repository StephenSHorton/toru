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
)

type presenceBody struct {
	Name     string    `json:"name"`
	Live     *LiveInfo `json:"live,omitempty"`
	InCall   bool      `json:"inCall,omitempty"`
	Muted    bool      `json:"muted,omitempty"`
	Deafened bool      `json:"deafened,omitempty"`
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
