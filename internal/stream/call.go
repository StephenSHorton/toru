package stream

import "errors"

// call.go is the voice call: everyone in a room can join one call, and the
// call is where screen sharing happens (you share and watch from inside it).
// Audio runs in a hidden call window (WebView2's WebRTC: echo cancellation,
// noise suppression, Opus, jitter buffer), one peer connection per other
// person in the call; this file is its state and its signaling relay.

// Call events.
const (
	EventCallSignal = "call:signal" // CallSignal, to the call window
	EventCallEnd    = "call:end"    // the call window should hang up and close
)

// msgCall carries one call-window signaling message between peers.
const msgCall = "call"

type callBody struct {
	Kind string `json:"kind"` // "offer" | "answer" | "hello"
	SDP  string `json:"sdp,omitempty"`
}

// CallSignal is a signaling message for the call window.
type CallSignal struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	SDP  string `json:"sdp"`
}

// CallState is this app's side of the call.
type CallState struct {
	InCall           bool   `json:"inCall"`
	Muted            bool   `json:"muted"`
	Deafened         bool   `json:"deafened"` // also mutes the mic
	Speaking         bool   `json:"speaking"`
	NoiseSuppression bool   `json:"noiseSuppression"`
	MicDevice        string `json:"micDevice"`     // WebView deviceId; "" = system default
	SpeakerDevice    string `json:"speakerDevice"` // WebView sinkId; "" = system default
	// Peers is each other call member's connection state as the call window
	// sees it ("connecting", "connected", "failed"...).
	Peers map[string]string `json:"peers"`
}

// CallReport is what the call window reports a few times a second.
type CallReport struct {
	Speaking []string          `json:"speaking"` // peer ids talking now; may include our own id
	Peers    map[string]string `json:"peers"`
}

// SetCallOpener installs the function that opens the hidden call window.
//
//wails:ignore
func (s *StreamService) SetCallOpener(fn func()) {
	s.mu.Lock()
	s.openCall = fn
	s.mu.Unlock()
}

// JoinCall joins the room's voice call.
func (s *StreamService) JoinCall() (State, error) {
	s.mu.Lock()
	if s.room == nil {
		s.mu.Unlock()
		return s.GetState(), errors.New("join or create a room first")
	}
	if s.inCall {
		st := s.stateLocked()
		s.mu.Unlock()
		return st, nil
	}
	s.inCall = true
	s.speaking = map[string]bool{}
	s.callPeers = map[string]string{}
	open := s.openCall
	s.mu.Unlock()
	if open != nil {
		open()
	}
	s.announce()
	s.emitState()
	return s.GetState(), nil
}

// LeaveCall hangs up: stops sharing, closes streams being watched, and
// closes the call window.
func (s *StreamService) LeaveCall() State {
	s.StopLive()
	s.mu.Lock()
	was := s.inCall
	s.inCall = false
	s.speaking = map[string]bool{}
	s.callPeers = map[string]string{}
	var watching []HostEvent
	for session, host := range s.sessions {
		watching = append(watching, HostEvent{Host: host, Session: session, Kind: "close"})
	}
	s.mu.Unlock()
	if was {
		for _, ev := range watching {
			s.emit(EventHost, ev)
		}
		s.emit(EventCallEnd, nil)
		s.announce()
	}
	s.emitState()
	return s.GetState()
}

// SetMuted mutes or unmutes the mic. Unmuting also undeafens.
func (s *StreamService) SetMuted(on bool) State {
	s.mu.Lock()
	s.muted = on
	if !on {
		s.deafened = false
	}
	s.mu.Unlock()
	s.announce()
	s.emitState()
	return s.GetState()
}

// SetDeafened silences everyone else (and mutes the mic while on).
func (s *StreamService) SetDeafened(on bool) State {
	s.mu.Lock()
	s.deafened = on
	s.mu.Unlock()
	s.announce()
	s.emitState()
	return s.GetState()
}

// SetVoice saves the voice preferences: noise suppression and devices. The
// call window applies them at once.
func (s *StreamService) SetVoice(noiseSuppression bool, micDevice, speakerDevice string) State {
	s.mu.Lock()
	s.settings.NoiseSuppression = noiseSuppression
	s.settings.MicDevice = micDevice
	s.settings.SpeakerDevice = speakerDevice
	settings := s.settings
	s.mu.Unlock()
	if err := saveSettings(settings); err != nil {
		logf("save settings: %v", err)
	}
	s.emitState()
	return s.GetState()
}

// CallSend (call window) relays one signaling message to peer to.
func (s *StreamService) CallSend(to, kind, sdp string) error {
	s.mu.Lock()
	in := s.inCall
	s.mu.Unlock()
	if !in {
		return errors.New("not in the call")
	}
	return s.send(to, msgCall, callBody{Kind: kind, SDP: sdp})
}

// CallReport (call window) reports who is speaking and how each peer
// connection is doing.
func (s *StreamService) CallReport(r CallReport) {
	s.mu.Lock()
	if !s.inCall {
		s.mu.Unlock()
		return
	}
	speaking := map[string]bool{}
	for _, id := range r.Speaking {
		speaking[id] = true
	}
	changed := len(speaking) != len(s.speaking) || len(r.Peers) != len(s.callPeers)
	for id := range speaking {
		if !s.speaking[id] {
			changed = true
		}
	}
	for id, st := range r.Peers {
		if s.callPeers[id] != st {
			changed = true
		}
	}
	s.speaking = speaking
	s.callPeers = r.Peers
	s.mu.Unlock()
	if changed {
		s.emitState()
	}
}

func (s *StreamService) onCallMessage(env Envelope) {
	b, err := decodeBody[callBody](env)
	if err != nil {
		return
	}
	s.mu.Lock()
	in := s.inCall
	s.mu.Unlock()
	if in {
		s.emit(EventCallSignal, CallSignal{From: env.From, Kind: b.Kind, SDP: b.SDP})
	}
}

// callStateLocked builds CallState; s.mu held.
func (s *StreamService) callStateLocked() CallState {
	peers := map[string]string{}
	for id, st := range s.callPeers {
		peers[id] = st
	}
	return CallState{
		InCall:           s.inCall,
		Muted:            s.muted || s.deafened,
		Deafened:         s.deafened,
		Speaking:         s.inCall && s.speaking[s.self] && !s.muted && !s.deafened,
		NoiseSuppression: s.settings.NoiseSuppression,
		MicDevice:        s.settings.MicDevice,
		SpeakerDevice:    s.settings.SpeakerDevice,
		Peers:            peers,
	}
}

