package hato

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/psanford/wormhole-william/wormhole"
)

// emitInterval throttles progress events. wormhole-william calls the progress
// callback once per 16 KiB transit record (and our counting reader once per
// Read), which on a LAN is thousands of times a second — far more than the
// webview can paint. Phase changes and the final progress always emit.
const emitInterval = 100 * time.Millisecond

// Manager is the transfer state machine: one outgoing and one incoming
// transfer, each with its own generation counter so a stale goroutine (one the
// user already stopped, or replaced by a newer StartSend) can never write its
// late result over the current state. It has no Wails dependency — events go
// through an injectable emitter — so tests can run two Managers in one process.
type Manager struct {
	mu sync.Mutex

	// emit broadcasts an event to the frontend. nil until SetEmitter (tests may
	// leave it nil, or install a recorder).
	emit func(name string, data any)

	// newClient builds the wormhole client per transfer. Overridable so tests can
	// point at a private rendezvous; defaults to the env-driven client.
	newClient func() *wormhole.Client

	send         SendState
	sendGen      atomic.Uint64
	sendCancel   context.CancelFunc
	sendLastEmit time.Time

	recv         ReceiveState
	recvGen      atomic.Uint64
	recvCancel   context.CancelFunc
	recvLastEmit time.Time

	// wg counts live transfer goroutines (ours, not the library's) so Shutdown
	// can give an in-flight receive a bounded moment to delete its .part file.
	wg sync.WaitGroup

	// onSendExit / onRecvExit are test hooks fired when a transfer goroutine
	// returns (regardless of generation). They observe Toru's wrapper goroutine
	// (runSend / runReceive), which selects on ctx itself — not the library's
	// goroutine behind it, which may outlive the wrapper in the windows where
	// the library does not consult ctx.
	onSendExit func()
	onRecvExit func()
}

// NewManager returns an idle Manager using the default (env-configured) client.
func NewManager() *Manager {
	return &Manager{
		newClient: defaultClient,
		send:      SendState{Phase: SendIdle},
		recv:      ReceiveState{Phase: RecvIdle},
	}
}

// SetEmitter installs the Go->JS event sink (app.Event.Emit in production).
func (m *Manager) SetEmitter(emit func(name string, data any)) {
	m.mu.Lock()
	m.emit = emit
	m.mu.Unlock()
}

// defaultClient builds a wormhole client from the environment. Empty env values
// fall through to the library defaults (the public relay.magic-wormhole.io
// rendezvous and transit.magic-wormhole.io relay). AppID is deliberately left
// empty so it resolves to the CLI's AppID — two clients can only talk if their
// AppIDs match, and interop with `wormhole receive` is a feature.
// PassPhraseComponentLength 2 gives the familiar "7-guitarist-revenge" shape.
func defaultClient() *wormhole.Client {
	return &wormhole.Client{
		PassPhraseComponentLength: 2,
		RendezvousURL:             os.Getenv("TORU_WORMHOLE_RENDEZVOUS"),
		TransitRelayAddress:       os.Getenv("TORU_WORMHOLE_TRANSIT"),
	}
}

// emitEvent broadcasts outside the mutex. The progress callbacks run on the
// library's goroutine and a JS handler may synchronously call SendStatus(), so
// emitting while holding mu would deadlock.
func (m *Manager) emitEvent(name string, data any) {
	m.mu.Lock()
	emit := m.emit
	m.mu.Unlock()
	if emit != nil {
		emit(name, data)
	}
}

// updateSend applies fn to the send state IF gen is still the live generation,
// then emits (throttled unless force). Returns false when the caller is stale.
func (m *Manager) updateSend(gen uint64, force bool, fn func(*SendState)) bool {
	m.mu.Lock()
	if gen != m.sendGen.Load() {
		m.mu.Unlock()
		return false
	}
	fn(&m.send)
	now := time.Now()
	if !force && now.Sub(m.sendLastEmit) < emitInterval {
		m.mu.Unlock()
		return true
	}
	m.sendLastEmit = now
	snap := m.send
	m.mu.Unlock()
	m.emitEvent(EventSend, snap)
	return true
}

// updateRecv is updateSend for the receive side.
func (m *Manager) updateRecv(gen uint64, force bool, fn func(*ReceiveState)) bool {
	m.mu.Lock()
	if gen != m.recvGen.Load() {
		m.mu.Unlock()
		return false
	}
	fn(&m.recv)
	now := time.Now()
	if !force && now.Sub(m.recvLastEmit) < emitInterval {
		m.mu.Unlock()
		return true
	}
	m.recvLastEmit = now
	snap := m.recv
	m.mu.Unlock()
	m.emitEvent(EventReceive, snap)
	return true
}

// SendStatus returns a copy of the current outgoing-transfer state.
func (m *Manager) SendStatus() SendState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.send
}

// ReceiveStatus returns a copy of the current incoming-transfer state.
func (m *Manager) ReceiveStatus() ReceiveState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.recv
}

// StartSend begins sending the file or folder at path, replacing (cancelling)
// any send already in flight — the UI has one send slot and a fresh drop means
// the user changed their mind. Validation errors (missing path, drive root,
// empty folder) return synchronously so the caller can show them; everything
// after that flows through the hato:send phases.
func (m *Manager) StartSend(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("hato: no path to send")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("hato: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("hato: %w", err)
	}
	name := filepath.Base(abs)
	// A drive root ("C:\") has no base name, and the library refuses a directory
	// name containing a separator. Nobody means to send their whole disk anyway.
	if name == "" || strings.ContainsAny(name, `\/`) || filepath.VolumeName(abs) == abs || abs == filepath.Dir(abs) {
		return errors.New("hato: cannot send a drive root")
	}
	kind := KindFile
	if info.IsDir() {
		kind = KindFolder
	}

	m.mu.Lock()
	if m.sendCancel != nil {
		m.sendCancel()
		m.sendCancel = nil
	}
	gen := m.sendGen.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	m.sendCancel = cancel
	m.send = SendState{
		Active:  true,
		Phase:   SendPreparing,
		Path:    abs,
		Name:    name,
		Kind:    kind,
		Message: "Preparing…",
	}
	m.sendLastEmit = time.Now()
	snap := m.send
	m.wg.Add(1)
	m.mu.Unlock()
	m.emitEvent(EventSend, snap)

	go m.runSend(ctx, cancel, gen, abs, name, kind == KindFolder)
	return nil
}

// FailIdleSend publishes a synchronous StartSend rejection (missing path, drive
// root) as an error-phase SendState, for callers that have nobody to return the
// error to — the hub window's drop handler. It only writes when no send is live:
// StartSend validates before touching state, so a rejected drop during a
// transfer must not clobber the transfer that is still running (it is logged
// instead). The path's base name is kept so the page can say what failed.
func (m *Manager) FailIdleSend(path string, err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	if m.send.Active {
		m.mu.Unlock()
		log.Printf("hato: ignored failed start of %q while a send is active: %v", path, err)
		return
	}
	name := filepath.Base(path)
	if name == "." || name == string(filepath.Separator) {
		name = path
	}
	msg := strings.TrimPrefix(err.Error(), "hato: ")
	m.sendGen.Add(1)
	m.send = SendState{
		Phase:   SendError,
		Path:    path,
		Name:    name,
		Error:   msg,
		Message: "Failed: " + msg,
	}
	snap := m.send
	m.mu.Unlock()
	m.emitEvent(EventSend, snap)
}

// StopSend cancels the in-flight send. The public state flips to "stopped"
// synchronously and the generation is bumped, so whatever the library goroutine
// reports later is dropped as stale. We never wait on the result channel:
// wormhole-william honours ctx while waiting for a receiver (ReadPake selects on
// ctx.Done), while accepting the transit connection, and while streaming (a
// watcher closes the conn), but NOT in the window between the receiver's PAKE
// and its file-ack — i.e. while a CLI receiver sits on "ok? [y/N]". In that
// window the goroutine lingers until the receiver answers or the websocket
// drops; the UI must not hang with it. Idempotent: no active send → nil.
func (m *Manager) StopSend() error {
	m.mu.Lock()
	if !m.send.Active {
		m.mu.Unlock()
		return nil
	}
	if m.sendCancel != nil {
		m.sendCancel()
		m.sendCancel = nil
	}
	m.sendGen.Add(1)
	m.send.Active = false
	m.send.Phase = SendStopped
	m.send.Message = "Stopped"
	snap := m.send
	m.mu.Unlock()
	m.emitEvent(EventSend, snap)
	return nil
}

// StartReceive begins receiving the transfer behind code into dir, replacing
// any receive in flight. code is normalised (trim, lowercase, spaces → "-") so a
// code read aloud and typed with spaces still works. The nameplate (leading
// number) is checked synchronously because the library's error for it is
// cryptic; a wrong-but-well-formed code surfaces as a phase error.
func (m *Manager) StartReceive(code, dir string) error {
	code = NormalizeCode(code)
	if code == "" {
		return errors.New("hato: enter the code the sender gave you")
	}
	nameplate, _, _ := strings.Cut(code, "-")
	if _, err := strconv.Atoi(nameplate); err != nil {
		return errors.New("hato: a code looks like 7-guitarist-revenge (number, dash, words)")
	}
	if strings.TrimSpace(dir) == "" {
		return errors.New("hato: no receive folder configured")
	}

	m.mu.Lock()
	if m.recvCancel != nil {
		m.recvCancel()
		m.recvCancel = nil
	}
	gen := m.recvGen.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	m.recvCancel = cancel
	m.recv = ReceiveState{
		Active:  true,
		Phase:   RecvConnecting,
		Code:    code,
		Dir:     dir,
		Message: "Connecting…",
	}
	m.recvLastEmit = time.Now()
	snap := m.recv
	m.wg.Add(1)
	m.mu.Unlock()
	m.emitEvent(EventReceive, snap)

	go m.runReceive(ctx, cancel, gen, code, dir)
	return nil
}

// CancelReceive cancels the in-flight receive: state flips to "cancelled" now,
// the generation is bumped, and the goroutine removes its partial file when it
// notices. The library checks ctx only at the top of each Read, so a receive
// blocked on a stalled sender is reaped at the next record (or when the
// connection drops) — the UI does not wait for that. Idempotent.
func (m *Manager) CancelReceive() error {
	m.mu.Lock()
	if !m.recv.Active {
		m.mu.Unlock()
		return nil
	}
	if m.recvCancel != nil {
		m.recvCancel()
		m.recvCancel = nil
	}
	m.recvGen.Add(1)
	m.recv.Active = false
	m.recv.Phase = RecvCancelled
	m.recv.Message = "Cancelled"
	snap := m.recv
	m.mu.Unlock()
	m.emitEvent(EventReceive, snap)
	return nil
}

// shutdownGrace bounds how long Shutdown waits for the transfer goroutines.
const shutdownGrace = time.Second

// Shutdown cancels both transfers and waits up to shutdownGrace for the
// goroutines to exit. Wired to app.OnShutdown; process exit frees everything
// anyway, but cancelling first gives an in-flight receive the chance to delete
// its .part file instead of leaving a half-written download behind. Best
// effort: the library notices ctx only at the top of a Read, so a receive
// parked inside a record read on a stalled sender can still outlive the grace
// period, and its .part then stays until the user deletes it.
func (m *Manager) Shutdown() {
	_ = m.StopSend()
	_ = m.CancelReceive()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownGrace):
	}
}

// NormalizeCode canonicalises a wormhole code as typed or pasted: trims,
// lowercases, and turns whitespace runs into the dashes the protocol expects
// ("7 guitarist revenge" → "7-guitarist-revenge").
func NormalizeCode(code string) string {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(code)))
	return strings.Join(fields, "-")
}

// percent is the status-line helper; 0 total → 0.
func percent(done, total int64) int {
	if total <= 0 {
		return 0
	}
	p := int(done * 100 / total)
	if p > 100 {
		p = 100
	}
	return p
}
