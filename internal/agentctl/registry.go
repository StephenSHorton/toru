// Package agentctl lets AI agents and scripts drive Toru (docs/agent-control.md):
// one registry of commands, reached over a named pipe only the current user
// can open. Go services register commands directly; windows register theirs
// from TypeScript (frontend/src/lib/agent.ts) and Go proxies calls to them.
//
// JS binding name: AgentService.*
package agentctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// Command is one thing an agent can do.
type Command struct {
	Name        string          // "editor.split", "capture.screenshot"
	Description string          // one or two sentences; becomes the MCP tool description
	Input       json.RawMessage // JSON Schema for the input object; nil = no input
	Destructive bool            // needs "confirm": true in its input
	Handler     func(ctx context.Context, input json.RawMessage) (any, error)
}

// CommandInfo is a command as list shows it (no handler).
type CommandInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Destructive bool            `json:"destructive,omitempty"`
}

// ErrUnknownCommand is returned by Call for a name nothing registered.
var ErrUnknownCommand = errors.New("unknown command")

// emptySchema is the input schema of a command that takes nothing.
var emptySchema = json.RawMessage(`{"type":"object","properties":{}}`)

// Registry holds every command by name. Safe for concurrent use.
type Registry struct {
	mu   sync.RWMutex
	cmds map[string]Command
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{cmds: map[string]Command{}} }

// Register adds c, replacing a command of the same name. It panics on a
// command with no name or handler: that is a programming error, caught the
// first time the app starts.
func (r *Registry) Register(c Command) {
	if c.Name == "" || c.Handler == nil {
		panic(fmt.Sprintf("agentctl: command %q needs a name and a handler", c.Name))
	}
	if len(c.Input) == 0 {
		c.Input = emptySchema
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds[c.Name] = c
}

// Unregister removes the named command, if present.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cmds, name)
}

// List returns every command, sorted by name.
func (r *Registry) List() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Command, 0, len(r.cmds))
	for _, c := range r.cmds {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Infos is List without the handlers, as the pipe's list method returns it.
func (r *Registry) Infos() []CommandInfo {
	cmds := r.List()
	out := make([]CommandInfo, len(cmds))
	for i, c := range cmds {
		out[i] = CommandInfo{Name: c.Name, Description: c.Description, Input: c.Input, Destructive: c.Destructive}
	}
	return out
}

// Call runs the named command with input (a JSON object; empty means {}).
// A destructive command refuses to run without "confirm": true. A handler
// that panics returns an error instead of taking Toru down.
func (r *Registry) Call(ctx context.Context, name string, input json.RawMessage) (result any, err error) {
	r.mu.RLock()
	c, ok := r.cmds[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w %q (list shows what Toru can do right now)", ErrUnknownCommand, name)
	}
	if len(strings.TrimSpace(string(input))) == 0 || string(input) == "null" {
		input = json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(input, &obj); err != nil {
		return nil, fmt.Errorf("%s: input must be a JSON object: %w", name, err)
	}
	if c.Destructive && string(obj["confirm"]) != "true" {
		return nil, fmt.Errorf(`%s changes or deletes things you can't get back; send "confirm": true to run it`, name)
	}
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%s failed: %v\n%s", name, p, debug.Stack())
		}
	}()
	return c.Handler(ctx, input)
}
