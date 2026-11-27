package agentctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventCall asks a window to run one of its commands:
// {id, window, name, input}. The window answers with Service.Respond(id, ...).
const EventCall = "agent:call"

// windowCallTimeout is how long a window gets to answer one command.
const windowCallTimeout = 30 * time.Second

// WindowCommand is a command a window registers (frontend/src/lib/agent.ts).
type WindowCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Input       any    `json:"input"` // JSON Schema object; nil = no input
	Destructive bool   `json:"destructive"`
	// TimeoutSec is how long the window may take to answer (0 = 30s; at
	// most 30 minutes): an export runs for minutes.
	TimeoutSec int `json:"timeoutSec,omitempty"`
}

// maxWindowCallTimeout caps a window command's own timeout.
const maxWindowCallTimeout = 30 * time.Minute

// WindowCall is the "agent:call" payload.
type WindowCall struct {
	ID     string          `json:"id"`
	Window string          `json:"window"`
	Name   string          `json:"name"`
	Input  json.RawMessage `json:"input"`
	// Caller is who called: the MCP client's name, or "toru-ctl" (see
	// Caller). A window tags the changes a command makes with it, like the
	// whiteboard's "agent:<caller>".
	Caller string `json:"caller,omitempty"`
}

type windowReply struct {
	result any
	err    string
}

// Service is the Wails-bound side of agent control: the per-user pipe
// (open whenever Toru runs), how to connect an agent to it, and the bridge
// that lets windows register commands.
type Service struct {
	reg *Registry

	mu     sync.Mutex
	server *Server
	// windows maps a window name to the commands it registered, in the order
	// windows last registered or were touched (most recent last).
	windows map[string]map[string]CommandInfo
	// timeouts holds a window command's own answer timeout, by window then name.
	timeouts map[string]map[string]time.Duration
	order    []string
	pending  map[string]chan windowReply
	seq      atomic.Uint64

	// Seams for tests; production values come from New.
	pipeName    string                       // "" = PipeName()
	exePath     func() (string, error)       // this toru.exe
	emit        func(event string, data any) // to every window
	windowAlive func(name string) bool       // nil = assume alive
	callTimeout time.Duration
}

// New returns the service over reg. Call Start once the app runs.
func New(reg *Registry) *Service {
	return &Service{
		reg:      reg,
		windows:  map[string]map[string]CommandInfo{},
		timeouts: map[string]map[string]time.Duration{},
		pending:  map[string]chan windowReply{},
		exePath:  os.Executable,
		emit: func(event string, data any) {
			if app := application.Get(); app != nil {
				app.Event.Emit(event, data)
			}
		},
		windowAlive: func(name string) bool {
			app := application.Get()
			if app == nil {
				return true
			}
			_, ok := app.Window.GetByName(name)
			return ok
		},
		callTimeout: windowCallTimeout,
	}
}

// Registry is the registry this service serves.
//
//wails:ignore
func (s *Service) Registry() *Registry { return s.reg }

// Start opens the pipe. Go-only. Agents and scripts can always reach a
// running Toru; the pipe's per-user lock is what keeps other users out.
//
//wails:ignore
func (s *Service) Start() error {
	return s.setListening(true)
}

// Shutdown closes the pipe. Go-only.
//
//wails:ignore
func (s *Service) Shutdown() {
	_ = s.setListening(false)
}

// ConnectInfo is what Settings > Connect your agent shows: this install's
// toru.exe and the two ways to add it to an MCP client.
type ConnectInfo struct {
	// Exe is the full path of this toru.exe.
	Exe string `json:"exe"`
	// ClaudeCommand adds Toru to Claude Code for every project.
	ClaudeCommand string `json:"claudeCommand"`
	// MCPConfig is the mcpServers JSON other MCP clients take.
	MCPConfig string `json:"mcpConfig"`
}

// GetConnectInfo returns the commands that connect an agent to this
// install of Toru.
func (s *Service) GetConnectInfo() (ConnectInfo, error) {
	exe, err := s.exePath()
	if err != nil {
		return ConnectInfo{}, err
	}
	if p, err := filepath.EvalSymlinks(exe); err == nil {
		exe = p
	}
	return connectInfo(exe), nil
}

func connectInfo(exe string) ConnectInfo {
	type server struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	cfg := map[string]any{"mcpServers": map[string]server{"toru": {Command: exe, Args: []string{"mcp"}}}}
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return ConnectInfo{
		Exe:           exe,
		ClaudeCommand: `claude mcp add --scope user toru -- "` + exe + `" mcp`,
		MCPConfig:     string(b),
	}
}

func (s *Service) setListening(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case on && s.server == nil:
		srv, err := Listen(s.reg, s.pipeName)
		if err != nil {
			return err
		}
		s.server = srv
	case !on && s.server != nil:
		err := s.server.Close()
		s.server = nil
		return err
	}
	return nil
}

// RegisterWindowCommands sets the commands window offers (replacing what it
// registered before). window is the page's window name.
func (s *Service) RegisterWindowCommands(window string, cmds []WindowCommand) error {
	if window == "" {
		return errors.New("agent: register needs the window's name")
	}
	infos := map[string]CommandInfo{}
	timeouts := map[string]time.Duration{}
	for _, c := range cmds {
		if c.Name == "" {
			return errors.New("agent: a window command has no name")
		}
		schema := emptySchema
		if c.Input != nil {
			b, err := json.Marshal(c.Input)
			if err != nil {
				return fmt.Errorf("agent: %s input schema: %w", c.Name, err)
			}
			schema = b
		}
		infos[c.Name] = CommandInfo{Name: c.Name, Description: c.Description, Input: schema, Destructive: c.Destructive}
		if c.TimeoutSec > 0 {
			timeouts[c.Name] = min(time.Duration(c.TimeoutSec)*time.Second, maxWindowCallTimeout)
		}
	}
	s.mu.Lock()
	before := s.windowNamesLocked()
	s.windows[window] = infos
	s.timeouts[window] = timeouts
	s.touchLocked(window)
	after := s.windowNamesLocked()
	s.mu.Unlock()
	s.sync(before, after)
	return nil
}

// UnregisterWindowCommands drops every command window registered (it is
// closing).
func (s *Service) UnregisterWindowCommands(window string) {
	s.mu.Lock()
	before := s.windowNamesLocked()
	s.dropLocked(window)
	after := s.windowNamesLocked()
	s.mu.Unlock()
	s.sync(before, after)
}

// TouchWindow marks window as the most recently used, so its commands win
// when several windows offer the same one and the call names none.
func (s *Service) TouchWindow(window string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.windows[window]; ok {
		s.touchLocked(window)
	}
}

// Respond answers an "agent:call": the handler's result, or errMsg when it
// failed.
func (s *Service) Respond(id string, result any, errMsg string) {
	s.mu.Lock()
	ch, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if ok {
		ch <- windowReply{result: result, err: errMsg}
	}
}

// sync updates the registry after window commands changed: a command name
// is registered as a proxy while any window offers it.
func (s *Service) sync(before, after map[string]CommandInfo) {
	for name := range before {
		if _, ok := after[name]; !ok {
			s.reg.Unregister(name)
		}
	}
	for name, info := range after {
		if old, ok := before[name]; ok && bytes.Equal(old.Input, info.Input) && old.Description == info.Description && old.Destructive == info.Destructive {
			continue
		}
		s.reg.Register(Command{
			Name:        name,
			Description: info.Description,
			Input:       info.Input,
			Destructive: info.Destructive,
			Handler: func(ctx context.Context, input json.RawMessage) (any, error) {
				return s.callWindow(ctx, name, input)
			},
		})
	}
}

// callWindow runs name in a window that offers it: the one the input's
// "window" names, else the most recently used one.
func (s *Service) callWindow(ctx context.Context, name string, input json.RawMessage) (any, error) {
	var sel struct {
		Window string `json:"window"`
	}
	_ = json.Unmarshal(input, &sel)

	for {
		s.mu.Lock()
		target := s.pickLocked(name, sel.Window)
		s.mu.Unlock()
		if target == "" {
			if sel.Window != "" {
				return nil, fmt.Errorf("%s: no open window %q offers it", name, sel.Window)
			}
			return nil, fmt.Errorf("%s: no open window offers it", name)
		}
		// A window closed without unregistering: forget it and try the next.
		if s.windowAlive != nil && !s.windowAlive(target) {
			s.UnregisterWindowCommands(target)
			continue
		}
		return s.callIn(ctx, target, name, input)
	}
}

func (s *Service) callIn(ctx context.Context, window, name string, input json.RawMessage) (any, error) {
	id := strconv.FormatUint(s.seq.Add(1), 10)
	ch := make(chan windowReply, 1)
	s.mu.Lock()
	limit := s.callTimeout
	if d, ok := s.timeouts[window][name]; ok && d > 0 {
		limit = d
	}
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()

	s.emit(EventCall, WindowCall{ID: id, Window: window, Name: name, Input: input, Caller: Caller(ctx)})
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != "" {
			return nil, errors.New(r.err)
		}
		return r.result, nil
	case <-timer.C:
		return nil, fmt.Errorf("%s: the window didn't answer within %s", name, limit)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// pickLocked is the window to run name in ("" = none offers it).
func (s *Service) pickLocked(name, want string) string {
	if want != "" {
		if cmds, ok := s.windows[want]; ok {
			if _, ok := cmds[name]; ok {
				return want
			}
		}
		return ""
	}
	for i := len(s.order) - 1; i >= 0; i-- {
		if _, ok := s.windows[s.order[i]][name]; ok {
			return s.order[i]
		}
	}
	return ""
}

func (s *Service) touchLocked(window string) {
	s.order = slices.DeleteFunc(s.order, func(w string) bool { return w == window })
	s.order = append(s.order, window)
}

func (s *Service) dropLocked(window string) {
	delete(s.windows, window)
	delete(s.timeouts, window)
	s.order = slices.DeleteFunc(s.order, func(w string) bool { return w == window })
}

// windowNamesLocked is every command any window offers (the most recently
// used window's spec when several offer one name).
func (s *Service) windowNamesLocked() map[string]CommandInfo {
	out := map[string]CommandInfo{}
	for _, w := range s.order {
		for name, info := range s.windows[w] {
			out[name] = info
		}
	}
	return out
}
