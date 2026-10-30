package main

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/stream"
)

// agent_commands_stream.go registers the agent commands for calls, screen
// sharing and LAN (docs/agent-control.md). The rules from the Call page hold
// here too: sharing only works inside a call, and the self-preview is left
// as the person set it (off by default). The room code is a secret: no
// result carries it unless the command was asked for it (showCode).

// streamAgent is what the commands use of *stream.StreamService.
type streamAgent interface {
	GetState() stream.State
	CreateRoom() (stream.State, error)
	JoinRoom(code string) (stream.State, error)
	LeaveRoom() stream.State
	JoinCall() (stream.State, error)
	LeaveCall() stream.State
	SetMuted(on bool) stream.State
	SetDeafened(on bool) stream.State
	GoLive() (stream.State, error)
	StopLive() stream.State
	SetPreview(on bool) stream.State
}

// lanAgent is what the commands use of *stream.LanService.
type lanAgent interface {
	GetState() stream.LanState
	Enable() (stream.LanState, error)
	Disable() stream.LanState
}

// callView is a stream.State as commands return it: the room code only when
// asked for.
func callView(st stream.State, showCode bool) map[string]any {
	out := map[string]any{
		"self":    st.Self,
		"inRoom":  st.Room != nil,
		"call":    st.Call,
		"sharing": st.Live,
		"preview": st.Preview,
	}
	if st.Room != nil {
		room := map[string]any{"connected": st.Room.Connected, "relays": st.Room.Relays, "members": st.Room.Members}
		if showCode {
			room["code"] = st.Room.Code
		}
		out["room"] = room
	}
	return out
}

func lanView(st stream.LanState) map[string]any {
	return map[string]any{
		"state": st.State, "on": st.Wanted, "inRoom": st.InRoom, "ip": st.IP,
		"error": st.Error, "detail": st.Detail, "relay": st.Relay,
		"serviceVersion": st.ServiceVersion, "appVersion": st.AppVersion,
	}
}

var propShowCode = map[string]any{"type": "boolean", "description": "Include the room code in the result. It's the room's only key: leave this off unless the person asked for the code."}

func registerStreamAgentCommands(reg *agentctl.Registry, s streamAgent, lan lanAgent) {
	reg.Register(agentctl.Command{
		Name:        "call.status",
		Description: "The voice call and room: whether you're in a room and the call, muted or deafened, who else is there, and whether you're sharing your screen.",
		Input:       schema(map[string]any{"showCode": propShowCode}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				ShowCode bool `json:"showCode"`
			}](in)
			if err != nil {
				return nil, err
			}
			return callView(s.GetState(), v.ShowCode), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "call.start",
		Description: "Join the voice call: in the current room, in the room for code, or in a new room (new: true). " +
			"Moving to another room while in one forgets the old room's code, so that needs confirm: true. Opens the call window.",
		Input: schema(map[string]any{
			"code":     map[string]any{"type": "string", "description": "A room code to join (16 letters and numbers, like ABCD-EFGH-JKMN-PQRS)."},
			"new":      map[string]any{"type": "boolean", "description": "Make a new room and start its call."},
			"confirm":  map[string]any{"type": "boolean", "description": "Needed to leave the current room for another one."},
			"showCode": propShowCode,
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Code     string `json:"code"`
				New      bool   `json:"new"`
				Confirm  bool   `json:"confirm"`
				ShowCode bool   `json:"showCode"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.New && v.Code != "" {
				return nil, errors.New("give code or new, not both")
			}
			cur := s.GetState().Room
			switch {
			case v.New || v.Code != "":
				if cur != nil && !v.Confirm {
					if c, ok := stream.NormalizeCode(v.Code); !ok || c != cur.Code {
						return nil, errors.New(`you're already in a room; moving to another forgets its code, so send "confirm": true (or leave out code and new to join this room's call)`)
					}
				}
				if v.New {
					_, err = s.CreateRoom()
				} else {
					_, err = s.JoinRoom(v.Code)
				}
				if err != nil {
					return nil, err
				}
			case cur == nil:
				return nil, errors.New("not in a room: give a room code, or new: true to make one")
			}
			st, err := s.JoinCall()
			if err != nil {
				return nil, err
			}
			// A new room's code is shown: there's no other way to learn it.
			return callView(st, v.ShowCode || v.New), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "call.leave",
		Description: "Hang up: stops sharing, closes streams being watched and the call window. You stay in the room (room.leave leaves it).",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return callView(s.LeaveCall(), false), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "room.leave",
		Description: "Leave the room: hangs up and forgets the room's code, so getting back in needs the code again. Needs confirm: true.",
		Destructive: true,
		Input:       schema(map[string]any{"confirm": propConfirm}, "confirm"),
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return callView(s.LeaveRoom(), false), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "call.mute",
		Description: "Mute or unmute the mic in the call (muted, default true), and optionally deafen (silence everyone else; also mutes the mic). Unmuting also undeafens.",
		Input: schema(map[string]any{
			"muted":    map[string]any{"type": "boolean", "default": true},
			"deafened": map[string]any{"type": "boolean"},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Muted    *bool `json:"muted"`
				Deafened *bool `json:"deafened"`
			}](in)
			if err != nil {
				return nil, err
			}
			st := s.SetMuted(v.Muted == nil || *v.Muted)
			if v.Deafened != nil {
				st = s.SetDeafened(*v.Deafened)
			}
			return callView(st, false), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "share.start",
		Description: "Share your screen with the call, using the Call page's stream settings. Only works while in the call (call.start). " +
			`The "Show my stream" preview stays as set unless preview is given.`,
		Input: schema(map[string]any{
			"preview": map[string]any{"type": "boolean", "description": "Turn the self-preview window on or off. Leave it out to keep the current setting."},
		}),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				Preview *bool `json:"preview"`
			}](in)
			if err != nil {
				return nil, err
			}
			if !s.GetState().Call.InCall {
				return nil, errors.New("screen sharing only works inside a call: run call.start first")
			}
			if v.Preview != nil {
				s.SetPreview(*v.Preview)
			}
			st, err := s.GoLive()
			if err != nil {
				return nil, err
			}
			return callView(st, false), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "share.stop",
		Description: "Stop sharing your screen. You stay in the call.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return callView(s.StopLive(), false), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "lan.status",
		Description: "LAN (play LAN games with the people in your room): its state (setup, update, off, starting, on, error), " +
			"this PC's LAN IP, and any error.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			return lanView(lan.GetState()), nil
		},
	})

	reg.Register(agentctl.Command{
		Name: "lan.set",
		Description: "Turn LAN on or off for the current room. On needs a room and LAN set up (the one-time administrator setup on the Call page); " +
			"it returns at once while LAN comes up, so check lan.status.",
		Input: schema(map[string]any{"on": map[string]any{"type": "boolean"}}, "on"),
		Handler: func(_ context.Context, in json.RawMessage) (any, error) {
			v, err := decode[struct {
				On *bool `json:"on"`
			}](in)
			if err != nil {
				return nil, err
			}
			if v.On == nil {
				return nil, errors.New(`give "on": true or false`)
			}
			if !*v.On {
				return lanView(lan.Disable()), nil
			}
			st, err := lan.Enable()
			if err != nil {
				return nil, err
			}
			return lanView(st), nil
		},
	})

	reg.Register(agentctl.Command{
		Name:        "lan.peers",
		Description: "The people in the room and their LAN tunnels: name, LAN IP, connection state (connecting, connected, failed), path (direct or relay) and round-trip time.",
		Handler: func(context.Context, json.RawMessage) (any, error) {
			ls := lan.GetState()
			type peer struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				IP    string `json:"ip,omitempty"`
				LanOn bool   `json:"lanOn"`
				State string `json:"state,omitempty"`
				Path  string `json:"path,omitempty"`
				RTTMs int    `json:"rttMs,omitempty"`
			}
			out := []peer{}
			seen := map[string]bool{}
			if room := s.GetState().Room; room != nil {
				for _, m := range room.Members {
					p := peer{ID: m.ID, Name: m.Name}
					if m.Lan != nil {
						p.IP, p.LanOn = m.Lan.IP, m.Lan.On
					}
					if t, ok := ls.Peers[m.ID]; ok {
						p.IP, p.State, p.Path, p.RTTMs = t.IP, t.State, t.Path, t.RTTMs
					}
					seen[m.ID] = true
					out = append(out, p)
				}
			}
			// Tunnels to people no longer heard from in the room (held a while).
			ids := make([]string, 0, len(ls.Peers))
			for id := range ls.Peers {
				if !seen[id] {
					ids = append(ids, id)
				}
			}
			sort.Strings(ids)
			for _, id := range ids {
				t := ls.Peers[id]
				out = append(out, peer{ID: id, IP: t.IP, State: t.State, Path: t.Path, RTTMs: t.RTTMs})
			}
			return map[string]any{"lan": ls.State, "ip": ls.IP, "peers": out}, nil
		},
	})
}
