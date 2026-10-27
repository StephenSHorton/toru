package pipe

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
	"unsafe"

	"github.com/Microsoft/go-winio"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/sys/windows"
)

// server_windows.go is the pipe server. Each GUI connection says Hello
// first; the session (and user) that sends Up owns the LAN until Down, and
// every other connection is refused anything that would change it.
// Comparing against the client's own session, not the active console
// session, keeps RDP users working (docs/lan.md, "Client check").

// Identity is who is on the other end of a connection.
type Identity struct {
	Session uint32 // Windows session id (GetNamedPipeClientSessionId)
	PID     uint32
	SID     string // the client process's user, e.g. S-1-5-21-…
}

// Owner is the session and user that brought the LAN up.
type Owner struct {
	Session uint32
	SID     string
}

// Backend is what the server drives: the service's adapter, key and node.
// Calls are serialized by the server; errors that are *CodeError reach the
// GUI with their code, anything else as api.ErrAdapter.
type Backend interface {
	// Identity returns the user's public key and overlay address, creating
	// the key on first use. It must not need a running LAN (Hello comes
	// before Up).
	Identity(sid string) (pub [32]byte, addr netip.Addr, err error)
	Up(owner Owner, up api.Up) error
	Down() error
	Signal(peer string, blob api.SignalBlob) error
	Drop(peer string) error
	SetICE(servers []api.ICEServer) error
	SetPolicy(p api.Policy) error
	// Readdress stores a new salt for sid and, if that user's LAN is up,
	// re-addresses it.
	Readdress(sid string, salt uint32, running bool) error
	Status() api.Status
}

// Config configures a Server. Zero values take the production defaults.
type Config struct {
	Name    string // api.PipeName
	SDDL    string // api.PipeSDDL
	Version string // service build version, sent in Hello
	Backend Backend
	Logf    func(format string, args ...any)

	// IdleTimeout is how long the LAN stays up with no connection from its
	// owner, and the service runs with no connection at all (60 s).
	IdleTimeout time.Duration
	// OnIdle runs when no client has been connected for IdleTimeout (the
	// service stops itself). The LAN is already down by then.
	OnIdle func()
	// MaxClients bounds concurrent connections (8).
	MaxClients int
	// Identify overrides how a connection's identity is read; tests only.
	Identify func(c net.Conn) (Identity, error)
}

// Server is a running pipe server.
type Server struct {
	cfg Config
	ln  net.Listener

	// call serializes every Backend call and owner change.
	call sync.Mutex

	mu        sync.Mutex
	clients   map[*Client]struct{}
	owner     *Owner
	peers     map[string]struct{}
	ownerIdle *time.Timer
	allIdle   *time.Timer
	closed    bool
	wg        sync.WaitGroup
}

// Client is one GUI connection.
type Client struct {
	Identity
	conn  net.Conn
	wmu   sync.Mutex
	hello bool // sent a valid Hello
}

const writeTimeout = 5 * time.Second

// Send writes one message; a GUI that stops reading is disconnected.
func (c *Client) Send(m api.Message) error {
	b, err := Encode(m)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err = c.conn.Write(b)
	if err != nil {
		c.conn.Close()
	}
	return err
}

// Listen creates the pipe and starts accepting.
func Listen(cfg Config) (*Server, error) {
	if cfg.Name == "" {
		cfg.Name = api.PipeName
	}
	if cfg.SDDL == "" {
		cfg.SDDL = api.PipeSDDL
	}
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	if cfg.IdleTimeout == 0 {
		cfg.IdleTimeout = 60 * time.Second
	}
	if cfg.MaxClients == 0 {
		cfg.MaxClients = 8
	}
	if cfg.Identify == nil {
		cfg.Identify = identify
	}
	if cfg.Backend == nil {
		return nil, errors.New("pipe: no backend")
	}
	ln, err := winio.ListenPipe(cfg.Name, &winio.PipeConfig{
		SecurityDescriptor: cfg.SDDL,
		MessageMode:        false,
		InputBufferSize:    MaxLine,
		OutputBufferSize:   MaxLine,
	})
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", cfg.Name, err)
	}
	s := &Server{cfg: cfg, ln: ln, clients: map[*Client]struct{}{}, peers: map[string]struct{}{}}
	s.mu.Lock()
	s.armTimersLocked() // nobody connected yet: the service still stops on its own
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop()
	return s, nil
}

// Close stops accepting, disconnects every client and waits for them. It
// does not bring the LAN down; the service does that on stop.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	for c := range s.clients {
		c.conn.Close()
	}
	stopTimer(s.ownerIdle)
	stopTimer(s.allIdle)
	s.mu.Unlock()
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

func stopTimer(t *time.Timer) {
	if t != nil {
		t.Stop()
	}
}

// Owner returns the session that owns the running LAN.
func (s *Server) Owner() (Owner, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == nil {
		return Owner{}, false
	}
	return *s.owner, true
}

// SendOwner sends m to every connection of the owning session and user
// (normally one GUI). The backend uses it for Signal, PeerState and Status.
func (s *Server) SendOwner(m api.Message) {
	for _, c := range s.ownerClients() {
		if err := c.Send(m); err != nil {
			s.cfg.Logf("pipe: send %s to pid %d: %v", m.Type, c.PID, err)
		}
	}
}

// Released tells the server the backend went down by itself (an error after
// Up); the session binding ends with it.
func (s *Server) Released() {
	s.mu.Lock()
	s.owner = nil
	s.peers = map[string]struct{}{}
	s.armTimersLocked()
	s.mu.Unlock()
}

func (s *Server) ownerClients() []*Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner == nil {
		return nil
	}
	var out []*Client
	for c := range s.clients {
		if c.hello && s.isOwnerLocked(c) {
			out = append(out, c)
		}
	}
	return out
}

func (s *Server) isOwnerLocked(c *Client) bool {
	return s.owner != nil && s.owner.Session == c.Session && s.owner.SID == c.SID
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, winio.ErrPipeListenerClosed) {
				return
			}
			s.cfg.Logf("pipe: accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		s.wg.Add(1)
		go s.serve(conn)
	}
}

func (s *Server) serve(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()
	id, err := s.cfg.Identify(conn)
	if err != nil {
		s.cfg.Logf("pipe: identify client: %v", err)
		return
	}
	c := &Client{Identity: id, conn: conn}
	s.mu.Lock()
	if s.closed || len(s.clients) >= s.cfg.MaxClients {
		s.mu.Unlock()
		c.Send(errMsg(0, &CodeError{Code: api.ErrBadRequest, Detail: "too many clients"}))
		return
	}
	s.clients[c] = struct{}{}
	s.armTimersLocked()
	s.mu.Unlock()
	s.cfg.Logf("pipe: client pid %d session %d user %s connected", id.PID, id.Session, id.SID)
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.armTimersLocked()
		s.mu.Unlock()
		s.cfg.Logf("pipe: client pid %d disconnected", id.PID)
	}()

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 4096), MaxLine)
	for sc.Scan() {
		m, blob, err := Decode(sc.Bytes())
		if err != nil {
			c.Send(errMsg(m.ID, err))
			if !c.hello {
				return
			}
			continue
		}
		if !c.hello && m.Type != api.TypeHello {
			c.Send(errMsg(m.ID, &CodeError{Code: api.ErrProtocol, Detail: "hello first"}))
			return
		}
		if !s.handle(c, m, blob) {
			return
		}
	}
	if err := sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		c.Send(errMsg(0, badRequest("message over %d bytes", MaxLine)))
	}
}

func errMsg(id uint64, err error) api.Message {
	var ce *CodeError
	if !errors.As(err, &ce) {
		ce = &CodeError{Code: api.ErrAdapter, Detail: err.Error()}
	}
	return api.Message{Type: api.TypeError, ID: id, Error: &api.Error{Code: ce.Code, Detail: ce.Detail}}
}

var errWrongSession = &CodeError{Code: api.ErrWrongSession, Detail: "another Windows session owns the running LAN"}

// handle runs one validated message; false closes the connection.
func (s *Server) handle(c *Client, m api.Message, blob *api.SignalBlob) bool {
	s.call.Lock()
	defer s.call.Unlock()
	b := s.cfg.Backend

	s.mu.Lock()
	up := s.owner != nil
	mine := s.isOwnerLocked(c)
	s.mu.Unlock()

	reply := func(err error) {
		if err != nil {
			c.Send(errMsg(m.ID, err))
		}
	}
	// Anything that touches a running LAN needs to be its owner.
	needOwner := func() error {
		if !up {
			return &CodeError{Code: api.ErrNotUp, Detail: m.Type + " before up"}
		}
		if !mine {
			return errWrongSession
		}
		return nil
	}

	switch m.Type {
	case api.TypeHello:
		return s.hello(c, m)

	case api.TypeStatus:
		st := b.Status()
		if up && !mine {
			// Another user's room, address and peers are none of this
			// client's business.
			st = api.Status{State: st.State, Version: st.Version, Error: api.ErrWrongSession}
		}
		c.Send(api.Message{Type: api.TypeStatus, ID: m.ID, Status: &st})

	case api.TypeUp:
		if up && !mine {
			reply(errWrongSession)
			return true
		}
		if up {
			// A second Up from the owner is a room switch: tear down first.
			if err := b.Down(); err != nil {
				s.cfg.Logf("pipe: down before re-up: %v", err)
			}
		}
		owner := Owner{Session: c.Session, SID: c.SID}
		s.mu.Lock()
		s.owner = &owner
		s.peers = map[string]struct{}{}
		s.armTimersLocked()
		s.mu.Unlock()
		if err := b.Up(owner, *m.Up); err != nil {
			s.Released()
			reply(err)
		}

	case api.TypeDown:
		if !up {
			return true // already off; Down is idempotent
		}
		if !mine {
			reply(errWrongSession)
			return true
		}
		err := b.Down()
		s.Released()
		reply(err)

	case api.TypeSignal:
		if err := needOwner(); err != nil {
			reply(err)
			return true
		}
		if err := s.notePeer(m.Signal.Peer); err != nil {
			reply(err)
			return true
		}
		reply(b.Signal(m.Signal.Peer, *blob))

	case api.TypeDrop:
		if err := needOwner(); err != nil {
			reply(err)
			return true
		}
		s.mu.Lock()
		delete(s.peers, m.Drop.Peer)
		s.mu.Unlock()
		reply(b.Drop(m.Drop.Peer))

	case api.TypeSetICE:
		if err := needOwner(); err != nil {
			reply(err)
			return true
		}
		reply(b.SetICE(m.SetICE.ICEServers))

	case api.TypeSetPolicy:
		if err := needOwner(); err != nil {
			reply(err)
			return true
		}
		reply(b.SetPolicy(*m.SetPolicy))

	case api.TypeReaddress:
		// Only the caller's own address; with another session's LAN up,
		// the salt is stored for later and the running LAN is left alone.
		reply(b.Readdress(c.SID, m.Readdress.Salt, up && mine))
	}
	return true
}

func (s *Server) notePeer(peer string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.peers[peer]; ok {
		return nil
	}
	if len(s.peers) >= MaxPeers {
		return badRequest("more than %d peers", MaxPeers)
	}
	s.peers[peer] = struct{}{}
	return nil
}

func (s *Server) hello(c *Client, m api.Message) bool {
	b := s.cfg.Backend
	out := api.Hello{Major: api.ProtocolMajor, Minor: api.ProtocolMinor, Version: s.cfg.Version}
	if m.Hello.Major != api.ProtocolMajor {
		// Say who we are anyway, so the GUI can show "Update LAN".
		c.Send(api.Message{Type: api.TypeHello, ID: m.ID, Hello: &out})
		c.Send(errMsg(m.ID, &CodeError{Code: api.ErrProtocol,
			Detail: fmt.Sprintf("service speaks %d.%d, GUI %d.%d", api.ProtocolMajor, api.ProtocolMinor, m.Hello.Major, m.Hello.Minor)}))
		return false
	}
	pub, addr, err := b.Identity(c.SID)
	if err != nil {
		s.cfg.Logf("pipe: identity for %s: %v", c.SID, err)
		c.Send(errMsg(m.ID, err))
		return false
	}
	out.PubKey = base64.StdEncoding.EncodeToString(pub[:])
	out.IP = addr.String()
	s.mu.Lock()
	c.hello = true
	s.armTimersLocked() // the owner's GUI may just have reconnected
	s.mu.Unlock()
	c.Send(api.Message{Type: api.TypeHello, ID: m.ID, Hello: &out})
	return true
}

// armTimersLocked starts or stops the two idle timers from the current
// client set. Owner gone for IdleTimeout: the LAN comes down. Nobody at all
// for IdleTimeout: the LAN comes down and OnIdle stops the service.
func (s *Server) armTimersLocked() {
	if s.closed {
		return
	}
	ownerHere := false
	for c := range s.clients {
		if s.isOwnerLocked(c) {
			ownerHere = true
			break
		}
	}
	switch {
	case s.owner == nil || ownerHere:
		stopTimer(s.ownerIdle)
		s.ownerIdle = nil
	case s.ownerIdle == nil:
		s.ownerIdle = time.AfterFunc(s.cfg.IdleTimeout, s.ownerGone)
	}
	switch {
	case len(s.clients) > 0:
		stopTimer(s.allIdle)
		s.allIdle = nil
	case s.allIdle == nil:
		s.allIdle = time.AfterFunc(s.cfg.IdleTimeout, s.nobodyHere)
	}
}

func (s *Server) ownerGone() {
	s.call.Lock()
	defer s.call.Unlock()
	s.mu.Lock()
	if s.closed || s.owner == nil || s.ownerIdle == nil {
		s.mu.Unlock()
		return
	}
	for c := range s.clients {
		if s.isOwnerLocked(c) { // reconnected meanwhile
			s.mu.Unlock()
			return
		}
	}
	s.ownerIdle = nil
	s.mu.Unlock()
	s.cfg.Logf("pipe: owner GUI gone for %v; LAN down", s.cfg.IdleTimeout)
	if err := s.cfg.Backend.Down(); err != nil {
		s.cfg.Logf("pipe: down: %v", err)
	}
	s.Released()
}

func (s *Server) nobodyHere() {
	s.call.Lock()
	s.mu.Lock()
	if s.closed || len(s.clients) > 0 || s.allIdle == nil {
		s.mu.Unlock()
		s.call.Unlock()
		return
	}
	s.allIdle = nil
	wasUp := s.owner != nil
	s.mu.Unlock()
	if wasUp {
		s.cfg.Logf("pipe: no GUI for %v; LAN down", s.cfg.IdleTimeout)
		if err := s.cfg.Backend.Down(); err != nil {
			s.cfg.Logf("pipe: down: %v", err)
		}
		s.Released()
	}
	s.call.Unlock()
	s.cfg.Logf("pipe: no GUI for %v; stopping", s.cfg.IdleTimeout)
	if s.cfg.OnIdle != nil {
		s.cfg.OnIdle()
	}
}

// Identity of a pipe client: process id and session from the pipe handle,
// user SID from that process's token.

var procGetNamedPipeClientSessionId = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetNamedPipeClientSessionId")

func identify(conn net.Conn) (Identity, error) {
	fd, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return Identity{}, errors.New("not a pipe connection")
	}
	h := windows.Handle(fd.Fd())
	var id Identity
	if err := windows.GetNamedPipeClientProcessId(h, &id.PID); err != nil {
		return id, fmt.Errorf("client pid: %w", err)
	}
	r, _, err := procGetNamedPipeClientSessionId.Call(uintptr(h), uintptr(unsafe.Pointer(&id.Session)))
	if r == 0 {
		return id, fmt.Errorf("client session: %w", err)
	}
	sid, err := processUserSID(id.PID)
	if err != nil {
		return id, err
	}
	id.SID = sid
	return id, nil
}

func processUserSID(pid uint32) (string, error) {
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", fmt.Errorf("open client process %d: %w", pid, err)
	}
	defer windows.CloseHandle(p)
	var tok windows.Token
	if err := windows.OpenProcessToken(p, windows.TOKEN_QUERY, &tok); err != nil {
		return "", fmt.Errorf("client token: %w", err)
	}
	defer tok.Close()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("client user: %w", err)
	}
	return u.User.Sid.String(), nil
}

// MarshalBlob turns a node's blob into a Signal for the GUI.
func MarshalBlob(peer string, blob api.SignalBlob) (api.Message, error) {
	raw, err := json.Marshal(blob)
	if err != nil {
		return api.Message{}, err
	}
	return api.Message{Type: api.TypeSignal, Signal: &api.Signal{Peer: peer, Blob: raw}}, nil
}
