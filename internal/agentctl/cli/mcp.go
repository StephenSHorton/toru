package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RunMCP serves MCP on env.Stdin / env.Stdout until the client hangs up.
// The tools are the running Toru's commands, re-read from the pipe on every
// tools/list (window commands come and go as editor windows open and
// close), plus app.start, which works while Toru isn't running.
func RunMCP(ctx context.Context, env Env) error {
	s := newMCPServer(env)
	defer func() { _ = s.client.Close() }()
	return s.srv.Run(ctx, &mcp.IOTransport{
		Reader:        io.NopCloser(env.Stdin),
		Writer:        nopWriteCloser{env.Stdout},
		MaxLineLength: maxLine,
	})
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

type mcpServer struct {
	env    Env
	client *Client
	srv    *mcp.Server

	mu    sync.Mutex
	tools map[string]CommandInfo // what the server lists now, besides app.start
}

func newMCPServer(env Env) *mcpServer {
	s := &mcpServer{env: env, client: NewClient(env.Dial), tools: map[string]CommandInfo{}}
	s.srv = mcp.NewServer(&mcp.Implementation{Name: "toru", Title: "Toru", Version: env.Version}, &mcp.ServerOptions{
		Instructions: "Controls the Toru screenshot, recording and video editor app running on this PC. " +
			"Editor changes go through the open editor window's undo history, so the person sees each step. " +
			"If tools are missing, Toru may not be running: call app.start.",
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{ListChanged: true}},
	})
	s.srv.AddTool(toolFor(appStartInfo), func(ctx context.Context, _ *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		res, err := appStart(ctx, s.client, s.env)
		if errors.Is(err, errUnreachable) {
			return errorResult(unreachableMessage(s.env, "start it from the Start menu")), nil
		}
		if err != nil {
			return errorResult(err.Error()), nil
		}
		s.sync(ctx)
		b, _ := json.Marshal(res)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}, nil
	})
	s.srv.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			switch method {
			case "tools/list":
				s.sync(ctx)
			case "tools/call":
				// A window command registered since the last list.
				if r, ok := req.(*mcp.CallToolRequest); ok && r.Params != nil && !s.known(r.Params.Name) {
					s.sync(ctx)
				}
			}
			return next(ctx, method, req)
		}
	})
	return s
}

func (s *mcpServer) known(name string) bool {
	if name == appStartName {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.tools[name]
	return ok
}

// sync makes the server's tools match the pipe's list. When Toru can't be
// reached the list shrinks to app.start, whose description says why.
func (s *mcpServer) sync(ctx context.Context) {
	cmds, err := s.client.List(ctx)
	if err != nil {
		cmds = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	want := map[string]CommandInfo{}
	for _, c := range cmds {
		if c.Name == appStartName || c.Name == "" {
			continue
		}
		want[c.Name] = c
	}
	var gone []string
	for name := range s.tools {
		if _, ok := want[name]; !ok {
			gone = append(gone, name)
		}
	}
	if len(gone) > 0 {
		s.srv.RemoveTools(gone...)
		for _, n := range gone {
			delete(s.tools, n)
		}
	}
	for name, c := range want {
		if old, ok := s.tools[name]; ok && sameCommand(old, c) {
			continue
		}
		s.srv.AddTool(toolFor(c), s.forward(name))
		s.tools[name] = c
	}
}

func sameCommand(a, b CommandInfo) bool {
	return a.Description == b.Description && a.Destructive == b.Destructive && bytes.Equal(a.Input, b.Input)
}

// toolFor is a command as an MCP tool. A schema that isn't a JSON object
// schema (AddTool would panic on it) becomes "any object".
func toolFor(c CommandInfo) *mcp.Tool {
	schema := json.RawMessage(`{"type":"object"}`)
	var m map[string]any
	if json.Unmarshal(c.Input, &m) == nil && m["type"] == "object" {
		schema = c.Input
	}
	t := &mcp.Tool{Name: c.Name, Description: c.Description, InputSchema: schema}
	if c.Destructive {
		yes := true
		t.Annotations = &mcp.ToolAnnotations{DestructiveHint: &yes}
	}
	return t
}

// forward is a tool handler that runs the command in Toru.
func (s *mcpServer) forward(name string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		input := json.RawMessage(`{}`)
		if req.Params != nil && len(bytes.TrimSpace(req.Params.Arguments)) > 0 && !bytes.Equal(bytes.TrimSpace(req.Params.Arguments), []byte("null")) {
			input = req.Params.Arguments
		}
		raw, err := s.client.Call(ctx, name, input)
		if err != nil {
			var cmdErr *CommandError
			if errors.As(err, &cmdErr) {
				return errorResult(cmdErr.Message), nil
			}
			if errors.Is(err, errUnreachable) {
				return errorResult(unreachableMessage(s.env, "call the app.start tool")), nil
			}
			return errorResult(err.Error()), nil
		}
		return toolResult(raw), nil
	}
}

// toolResult turns a command's JSON result into MCP content: an image
// result becomes image content (plus the rest of the result as text),
// anything else is its JSON as text.
func toolResult(raw json.RawMessage) *mcp.CallToolResult {
	if img, ok := parseImage(raw); ok {
		content := []mcp.Content{&mcp.ImageContent{MIMEType: img.MIME, Data: img.Data}}
		if len(img.Rest) > 0 {
			b, _ := json.Marshal(img.Rest)
			content = append(content, &mcp.TextContent{Text: string(b)})
		}
		return &mcp.CallToolResult{Content: content}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}
}

func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: msg}}}
}
