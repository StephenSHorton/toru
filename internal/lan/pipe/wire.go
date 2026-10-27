// Package pipe is the service end of the Toru LAN named pipe: JSON lines of
// api.Message between the per-user GUI and the LocalSystem service. It
// checks who is on the other end (Windows session and user SID), binds the
// running LAN to the session that sent Up, validates every message, and
// keeps one WireGuard key per Windows user. Design: docs/lan.md, "The
// service and the pipe".
package pipe

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/StephenSHorton/toru/internal/lan/api"
)

// Limits. Anything bigger is a bug or an attack; the GUI never needs more.
const (
	MaxLine       = 64 << 10 // one JSON message
	MaxBlob       = 4 << 10  // one Signal blob
	MaxPeers      = 64       // distinct room peers per LAN session
	MaxICEServers = 8
	MaxICEURLs    = 4
	maxIDLen      = 64
	maxRoomLen    = 128
	maxURLLen     = 256
	maxCredLen    = 512
	maxCandLen    = 1024
	maxUfragLen   = 256
)

// CodeError is a failure the GUI sees as api.Error.
type CodeError struct {
	Code   string
	Detail string
}

func (e *CodeError) Error() string {
	if e.Detail == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func badRequest(format string, args ...any) *CodeError {
	return &CodeError{Code: api.ErrBadRequest, Detail: fmt.Sprintf(format, args...)}
}

// fromGUI lists the types a GUI may send and the payload each must carry.
var fromGUI = map[string]func(m *api.Message) bool{
	api.TypeHello:     func(m *api.Message) bool { return m.Hello != nil },
	api.TypeUp:        func(m *api.Message) bool { return m.Up != nil },
	api.TypeSetICE:    func(m *api.Message) bool { return m.SetICE != nil },
	api.TypeSignal:    func(m *api.Message) bool { return m.Signal != nil },
	api.TypeDrop:      func(m *api.Message) bool { return m.Drop != nil },
	api.TypeReaddress: func(m *api.Message) bool { return m.Readdress != nil },
	api.TypeSetPolicy: func(m *api.Message) bool { return m.SetPolicy != nil },
	api.TypeDown:      func(m *api.Message) bool { return true },
	api.TypeStatus:    func(m *api.Message) bool { return true },
}

// payloads counts the set payload fields; a GUI message carries at most one.
func payloads(m *api.Message) int {
	n := 0
	for _, set := range []bool{m.Hello != nil, m.Up != nil, m.SetICE != nil, m.Signal != nil, m.Drop != nil,
		m.Readdress != nil, m.SetPolicy != nil, m.Status != nil, m.PeerState != nil, m.Error != nil} {
		if set {
			n++
		}
	}
	return n
}

// Decode parses one line from the GUI and validates it. For a Signal it
// also returns the parsed blob, so nothing downstream re-parses GUI bytes.
func Decode(line []byte) (api.Message, *api.SignalBlob, error) {
	var m api.Message
	if len(line) > MaxLine {
		return m, nil, badRequest("message over %d bytes", MaxLine)
	}
	// Unknown fields are ignored: the protocol adds optional fields in minor
	// versions, which an older service is meant to skip.
	if err := json.Unmarshal(line, &m); err != nil {
		return m, nil, badRequest("bad JSON: %v", err)
	}
	blob, err := Validate(&m)
	return m, blob, err
}

// Validate checks a GUI message against the pipe's rules: known type, the
// one matching payload, bounded sizes, peer ids and ICE URLs well formed, no
// address outside the overlay.
func Validate(m *api.Message) (*api.SignalBlob, error) {
	has, ok := fromGUI[m.Type]
	if !ok {
		return nil, badRequest("type %q not accepted from the GUI", m.Type)
	}
	if !has(m) || payloads(m) > 1 || (m.Type == api.TypeDown || m.Type == api.TypeStatus) && payloads(m) != 0 {
		return nil, badRequest("type %q with the wrong payload", m.Type)
	}
	switch m.Type {
	case api.TypeUp:
		if err := checkText("room", m.Up.Room, maxRoomLen, false); err != nil {
			return nil, err
		}
		if err := checkPeerID(m.Up.Self); err != nil {
			return nil, err
		}
		return nil, checkICE(m.Up.ICEServers)
	case api.TypeSetICE:
		return nil, checkICE(m.SetICE.ICEServers)
	case api.TypeSignal:
		if err := checkPeerID(m.Signal.Peer); err != nil {
			return nil, err
		}
		return DecodeBlob(m.Signal.Blob)
	case api.TypeDrop:
		return nil, checkPeerID(m.Drop.Peer)
	}
	return nil, nil
}

// DecodeBlob parses and checks a Signal blob from a room peer (through the
// GUI). Unknown fields are allowed: a newer peer's service may add some.
func DecodeBlob(raw json.RawMessage) (*api.SignalBlob, error) {
	if len(raw) == 0 || len(raw) > MaxBlob {
		return nil, badRequest("signal blob of %d bytes (max %d)", len(raw), MaxBlob)
	}
	var b api.SignalBlob
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, badRequest("bad signal blob: %v", err)
	}
	switch b.Kind {
	case api.KindHello, api.KindCreds, api.KindCand, api.KindBye:
	default:
		return nil, badRequest("signal kind %q", b.Kind)
	}
	for _, f := range []struct {
		name, v string
		max     int
	}{{"ufrag", b.Ufrag, maxUfragLen}, {"pwd", b.Pwd, maxUfragLen}, {"cand", b.Cand, maxCandLen}} {
		if err := checkText(f.name, f.v, f.max, true); err != nil {
			return nil, err
		}
	}
	if b.WGPub != "" {
		k, err := base64.StdEncoding.DecodeString(b.WGPub)
		if err != nil || len(k) != 32 {
			return nil, badRequest("wgPub is not a base64 32-byte key")
		}
	}
	if b.IP != "" {
		a, err := netip.ParseAddr(b.IP)
		if err != nil || !nodeAddr(a) {
			return nil, badRequest("ip %q is not an overlay node address", b.IP)
		}
	}
	return &b, nil
}

// nodeAddr matches adapter.ValidAddr; it is repeated here so the pipe
// package (and its tests) do not pull in Wintun.
func nodeAddr(a netip.Addr) bool {
	if !a.Is4() || !overlay.Contains(a) {
		return false
	}
	b := a.As4()
	host := uint16(b[2])<<8 | uint16(b[3])
	return host != 0 && host != 1 && host != 0xffff
}

var overlay = netip.MustParsePrefix(api.OverlayPrefix)

func checkPeerID(id string) error {
	if id == "" || len(id) > maxIDLen {
		return badRequest("peer id of %d bytes", len(id))
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return badRequest("peer id %q has a disallowed character", id)
		}
	}
	return nil
}

// checkText bounds a free-form string and rejects control characters (they
// end up in logs).
func checkText(name, s string, max int, empty bool) error {
	if s == "" && !empty {
		return badRequest("%s is empty", name)
	}
	if len(s) > max {
		return badRequest("%s over %d bytes", name, max)
	}
	for _, c := range s {
		if c < 0x20 || c == 0x7f {
			return badRequest("%s has a control character", name)
		}
	}
	return nil
}

func checkICE(servers []api.ICEServer) error {
	if len(servers) > MaxICEServers {
		return badRequest("%d ICE servers (max %d)", len(servers), MaxICEServers)
	}
	for _, s := range servers {
		if len(s.URLs) == 0 || len(s.URLs) > MaxICEURLs {
			return badRequest("ICE server with %d URLs", len(s.URLs))
		}
		for _, u := range s.URLs {
			if err := checkText("ICE URL", u, maxURLLen, false); err != nil {
				return err
			}
			scheme, _, _ := strings.Cut(u, ":")
			switch scheme {
			case "stun", "stuns", "turn", "turns":
			default:
				return badRequest("ICE URL %q is not stun:, stuns:, turn: or turns:", u)
			}
		}
		if err := checkText("ICE username", s.Username, maxCredLen, true); err != nil {
			return err
		}
		if err := checkText("ICE credential", s.Credential, maxCredLen, true); err != nil {
			return err
		}
	}
	return nil
}

// Encode marshals one message as a JSON line.
func Encode(m api.Message) ([]byte, error) {
	b, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
