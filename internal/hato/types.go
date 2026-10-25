// Package hato is Toru's built-in peer-to-peer file transfer: "a carrier pigeon
// for your files". The sender gets a short spoken code (7-guitarist-revenge);
// anyone with the code — another Toru, or the magic-wormhole CLI — receives the
// file or folder directly, end-to-end encrypted, with the public rendezvous and
// transit relays seeing only ciphertext.
//
// Transport is magic-wormhole via github.com/psanford/wormhole-william, pure Go,
// in-process: no sidecar, no second binary, and the default AppID is kept so
// `wormhole receive <code>` interoperates. The package is split into the
// Manager (transfer state machine + events, no Wails dependency, so it is
// testable with two instances in one process) and the HatoService (the Wails
// binding: dialogs, prefs, Explorer, QR).
package hato

// Event names broadcast Go->JS via app.Event.Emit. The typed ones are
// registered in main.go's init() so the binding generator emits their models;
// EventOpen carries no payload (it just tells the hub to switch pages) and is
// therefore NOT registered.
const (
	EventSend    = "hato:send"    // payload SendState
	EventReceive = "hato:receive" // payload ReceiveState
	EventOpen    = "hato:open"    // no payload
)

// Send phases, in the order a healthy transfer passes through them.
const (
	SendIdle      = "idle"
	SendPreparing = "preparing" // reading the file / zipping the folder, no code yet
	SendWaiting   = "waiting"   // code issued, nobody has claimed it yet
	SendSending   = "sending"   // bytes are flowing
	SendDone      = "done"
	SendStopped   = "stopped" // user hit Stop (or Toru shut down)
	SendError     = "error"
)

// Receive phases.
const (
	RecvIdle       = "idle"
	RecvConnecting = "connecting" // code entered, rendezvous + PAKE in progress
	RecvReceiving  = "receiving"
	RecvDone       = "done"
	RecvCancelled  = "cancelled"
	RecvError      = "error"
)

// Transfer kinds.
const (
	KindFile   = "file"
	KindFolder = "folder"
)

// SendState is the whole UI model of the ONE outgoing transfer. It is the
// hato:send event payload and the SendStatus() return value, so a page that
// mounts mid-transfer can render from a single fetch.
type SendState struct {
	Active  bool   `json:"active"`
	Phase   string `json:"phase"`   // "idle" | "preparing" | "waiting" | "sending" | "done" | "stopped" | "error"
	Path    string `json:"path"`    // absolute source path
	Name    string `json:"name"`    // base name shown in UI
	Kind    string `json:"kind"`    // "file" | "folder"
	Code    string `json:"code"`    // wormhole code once issued (phase >= waiting)
	Sent    int64  `json:"sent"`    // bytes sent so far (wire bytes)
	Total   int64  `json:"total"`   // total wire bytes (0 until known)
	Message string `json:"message"` // short human status line
	Error   string `json:"error"`   // set when Phase == "error"
}

// ReceiveState is the UI model of the ONE incoming transfer, independent of the
// send side (you can receive while a send is waiting for its receiver).
type ReceiveState struct {
	Active    bool   `json:"active"`
	Phase     string `json:"phase"` // "idle" | "connecting" | "receiving" | "done" | "cancelled" | "error"
	Code      string `json:"code"`
	Name      string `json:"name"`      // offered name
	Kind      string `json:"kind"`      // "file" | "folder"
	Dir       string `json:"dir"`       // destination folder
	OutPath   string `json:"outPath"`   // final written path (file or folder) when done
	Received  int64  `json:"received"`  // wire bytes received so far
	Total     int64  `json:"total"`     // TransferBytes64 from the offer (zip size for a folder)
	FileCount int    `json:"fileCount"` // files in the offer (1 for a single file)
	Message   string `json:"message"`
	Error     string `json:"error"`
}
