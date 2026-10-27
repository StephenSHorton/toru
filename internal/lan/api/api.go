// Package api is the contract between Toru (the per-user GUI process) and
// toru-lan.exe (the LocalSystem service that owns the Wintun adapter and the
// WireGuard device). It is the only thing both sides import, so it holds no
// behaviour: names, constants and the JSON messages that cross the pipe.
//
// Design: docs/lan.md. The service never sees the room code and has no MQTT
// client; the GUI carries LAN signaling between the room and this pipe.
package api

import "encoding/json"

// Protocol is the pipe protocol version. Bump the minor whenever the
// service must be replaced for this GUI: additive pipe changes (new optional
// fields, new message types an older peer can ignore) and any service
// change peers or the GUI rely on (signaling, checks, firewall rules). Bump
// the major on anything an older peer would misread. The GUI shows "Update
// LAN (needs admin)" only when the service's major differs or its minor is
// older; the app version is not compared, so a Toru release that leaves the
// service alone needs no UAC prompt.
//
// Minor 1: Hello carries the address salt (peers check it), data folder
// reparse-point checks, the firewall's system-RPC block rules.
const (
	ProtocolMajor = 1
	ProtocolMinor = 1
)

// PipeName is the service's named pipe. ProtectedPrefix\Administrators stops
// a non-admin process from creating the name first.
const PipeName = `\\.\pipe\ProtectedPrefix\Administrators\Toru\lan`

// PipeSDDL lets SYSTEM and Administrators do anything and interactive users
// connect (read and write data), nobody else. Interactive users get
// 0x12019b, not GW: a GW grant includes FILE_CREATE_PIPE_INSTANCE, which
// would let a non-admin add a server instance of the pipe and catch the GUI's
// next connection.
const PipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12019b;;;IU)"

// PipeClientAccess is the narrowest access the GUI needs to talk on the pipe:
// FILE_GENERIC_READ | FILE_WRITE_DATA | FILE_WRITE_EA | FILE_WRITE_ATTRIBUTES.
// winio's default GENERIC_READ|GENERIC_WRITE also connects under PipeSDDL
// (a client open doesn't need the instance right); TestPipeSDDL checks both.
const PipeClientAccess = 0x12019b

// ServiceName is the Windows service; "toru-lan.exe install" registers it demand-start
// with a security descriptor that lets interactive users start and stop it.
const (
	ServiceName        = "ToruLan"
	ServiceDisplayName = "Toru LAN"
	ServiceDescription = "Virtual LAN for Toru rooms: lets LAN games, calls and streams reach friends in the same room."
)

// AdapterName is the Wintun adapter's name; TunnelType keeps Toru out of
// WireGuard's adapter pool (Wintun license §3e: don't use the WireGuard name).
const (
	AdapterName = "Toru LAN"
	TunnelType  = "Toru"
	// AdapterGUID is fixed so Windows keeps one network profile (Private, the
	// firewall profile) for the adapter across restarts.
	AdapterGUID = "{6B2F2E54-0B7A-4C1F-9D3E-54D0A0C7A001}"
)

// Overlay addressing: every node is 100.96.x.y/16, derived from its WireGuard
// public key (see AddrFromKey in package lan). The /16 is on-link on the
// adapter; there is never a default route through it.
const (
	OverlayPrefix   = "100.96.0.0/16"
	OverlayBcast    = "100.96.255.255"
	MTU             = 1280
	BroadcastPort   = 7787 // UDP port the shim wraps fanned-out broadcasts on
	BroadcastMarker = "TBC1"
	FrameMarker     = "TEF1" // reserved for whole Ethernet frames (future L2 backend)
)

// Install layout (machine-wide; written only by the elevated installer).
const (
	InstallSubdir = `Toru\lan` // under %ProgramFiles%
	DataSubdir    = `Toru\lan` // under %ProgramData%, SYSTEM-only ACL
	// LanExe is both the elevated installer ("toru-lan.exe install" /
	// "uninstall", run once with UAC) and the service binary itself.
	LanExe    = "toru-lan.exe"
	WintunDLL = "wintun.dll"
	// WintunSHA256 is the vendored build/windows/wintun/wintun.dll (0.14.1
	// amd64); install refuses any other file.
	WintunSHA256 = "e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce"
)

// Firewall rule names the installer creates (and removes on uninstall).
const (
	FirewallRuleInbound = "Toru LAN (inbound)"
	FirewallRuleICMP    = "Toru LAN ICMP"
)

// Message is one JSON line on the pipe, either direction. Type picks which
// of the payload fields is set.
type Message struct {
	Type string `json:"type"`
	// ID correlates a request with its reply (Hello, Status, Sign); 0 for
	// unsolicited messages.
	ID uint64 `json:"id,omitempty"`

	Hello     *Hello     `json:"hello,omitempty"`
	Up        *Up        `json:"up,omitempty"`
	SetICE    *SetICE    `json:"setIce,omitempty"`
	Signal    *Signal    `json:"signal,omitempty"`
	Drop      *Drop      `json:"drop,omitempty"`
	Readdress *Readdress `json:"readdress,omitempty"`
	SetPolicy *Policy    `json:"setPolicy,omitempty"`
	Status    *Status    `json:"status,omitempty"`
	PeerState *PeerState `json:"peerState,omitempty"`
	Error     *Error     `json:"error,omitempty"`
}

// Message types. GUI → service: Hello, Up, SetICE, Signal, Drop, Readdress,
// SetPolicy, Down, Status. Service → GUI: Hello, Signal, PeerState, Status,
// Error. The pipe never accepts routes, firewall rules, file paths or
// addresses outside the overlay.
const (
	TypeHello     = "hello"
	TypeUp        = "up"
	TypeSetICE    = "setIce"
	TypeSignal    = "signal"
	TypeDrop      = "drop"
	TypeReaddress = "readdress"
	TypeSetPolicy = "setPolicy"
	TypeDown      = "down"
	TypeStatus    = "status"
	TypePeerState = "peerState"
	TypeError     = "error"
)

// Hello is the first message each way. The GUI sends Major/Minor; the
// service replies with its own plus its version, public key and address.
type Hello struct {
	Major   int    `json:"major"`
	Minor   int    `json:"minor"`
	Version string `json:"version,omitempty"` // service build version (service → GUI)
	PubKey  string `json:"pubKey,omitempty"`  // base64 WireGuard public key (service → GUI)
	IP      string `json:"ip,omitempty"`      // overlay address, e.g. 100.96.12.34 (service → GUI)
}

// ICEServer mirrors webrtc.ICEServer: STUN and TURN URLs with optional
// short-lived TURN REST credentials (fetched by the GUI, never shipped).
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// Up brings the adapter and WireGuard device up for one room. Room is an
// opaque hash of the room topic (so the service can tell rooms apart in logs
// without learning the code). Self is the GUI's stable peer id (InstallID).
type Up struct {
	Room       string      `json:"room"`
	Self       string      `json:"self"`
	ICEServers []ICEServer `json:"iceServers"`
	Policy     Policy      `json:"policy"`
}

// SetICE replaces the ICE server list (fresh TURN credentials) for new agents.
type SetICE struct {
	ICEServers []ICEServer `json:"iceServers"`
}

// Signal carries one opaque LAN signaling blob to or from a room peer. The
// GUI seals it into the room as msgLan and never parses Blob; the service
// owns its format (SignalBlob). From the service, an empty Peer means "every
// room member" (the service doesn't know who is in the room; it announces its
// Hello that way on Up and after a Readdress).
type Signal struct {
	Peer string          `json:"peer"`
	Blob json.RawMessage `json:"blob"`
}

// SignalBlob is the service's signaling payload inside Signal.Blob.
type SignalBlob struct {
	Kind  string `json:"kind"` // hello | cand | creds | bye
	Gen   uint32 `json:"gen"`  // agent generation; a new agent (restart, upgrade) bumps it
	Ufrag string `json:"ufrag,omitempty"`
	Pwd   string `json:"pwd,omitempty"`
	Cand  string `json:"cand,omitempty"` // one ICE candidate, ice.Candidate.Marshal()
	WGPub string `json:"wgPub,omitempty"`
	IP    string `json:"ip,omitempty"`
	// Salt is the address salt in a Hello: IP must be AddrFromKey(WGPub,
	// Salt), so an address is bound to the key that claims it.
	Salt uint32 `json:"salt,omitempty"`
	// Direct marks the second, relay-less agent built to upgrade a relayed
	// path (make-before-break); it runs alongside the working one.
	Direct bool `json:"direct,omitempty"`
}

// Signal blob kinds.
const (
	KindHello = "hello" // I'm on LAN: wgPub, ip (sent on Up and to each new peer)
	KindCreds = "creds" // ICE ufrag/pwd for agent Gen
	KindCand  = "cand"  // one trickled candidate for agent Gen ("" = end of candidates)
	KindBye   = "bye"   // I left LAN
)

// Drop removes a peer (left the room, timed out in presence).
type Drop struct {
	Peer string `json:"peer"`
}

// Readdress asks the service to re-derive its overlay address with Salt after
// the GUI saw a collision in presence (the lower public key keeps the address).
type Readdress struct {
	Salt uint32 `json:"salt"`
}

// Policy is the per-room LAN policy.
type Policy struct {
	// AllowServices opens SMB, RPC, NetBIOS, RDP and WinRM between members;
	// off by default, enforced in the shim's data path.
	AllowServices bool `json:"allowServices"`
	// MDNS fans out mDNS (224.0.0.251) and SSDP (239.255.255.250); off by default.
	MDNS bool `json:"mdns"`
}

// Status is the service's state, sent on request and on every change. Its
// Peers replace the GUI's whole peer list; a PeerState message updates one.
type Status struct {
	State   string      `json:"state"` // off | starting | on | error
	IP      string      `json:"ip,omitempty"`
	PubKey  string      `json:"pubKey,omitempty"`
	Room    string      `json:"room,omitempty"`
	Error   string      `json:"error,omitempty"` // Error code when State == error
	Detail  string      `json:"detail,omitempty"`
	Peers   []PeerState `json:"peers,omitempty"`
	Version string      `json:"version,omitempty"`
}

// Service states.
const (
	StateOff      = "off"
	StateStarting = "starting"
	StateOn       = "on"
	StateError    = "error"
)

// PeerState is one peer's tunnel as the service sees it.
type PeerState struct {
	Peer  string `json:"peer"`
	IP    string `json:"ip,omitempty"`
	State string `json:"state"`          // connecting | connected | failed
	Path  string `json:"path,omitempty"` // direct | relay
	RTTMs int    `json:"rttMs,omitempty"`
	// LastHandshake is unix seconds of the last WireGuard handshake, 0 = none.
	LastHandshake int64  `json:"lastHandshake,omitempty"`
	RxBytes       uint64 `json:"rxBytes,omitempty"`
	TxBytes       uint64 `json:"txBytes,omitempty"`
}

// Peer states and paths.
const (
	PeerConnecting = "connecting"
	PeerConnected  = "connected"
	PeerFailed     = "failed"
	PathDirect     = "direct"
	PathRelay      = "relay"
)

// Error is a failure the GUI can show. Code is stable; Detail is for logs.
type Error struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// Error codes.
const (
	ErrRangeInUse   = "range-in-use"  // 100.96.0.0/16 overlaps an existing route (Detail names the adapter)
	ErrAdapter      = "adapter"       // Wintun adapter could not be created or configured
	ErrWrongSession = "wrong-session" // another Windows session owns the running LAN
	ErrProtocol     = "protocol"      // incompatible pipe protocol
	ErrBadRequest   = "bad-request"   // malformed or disallowed message
	ErrNotUp        = "not-up"        // Signal/Drop before Up
)
