// Package cli is Toru's agent-control entry points (docs/agent-control.md):
// `toru.exe mcp`, an MCP stdio server, and `toru.exe ctl`, one call printed
// as JSON. Both are thin clients of the running Toru's agent pipe; they never
// open a window or touch the app's state themselves.
package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
)

// The pipe protocol, client side: one JSON request per line, one JSON
// response per line, answered in order (internal/agentctl/protocol.go).

type request struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
}

type response struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// CommandInfo is one command as the pipe's "list" returns it.
type CommandInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Destructive bool            `json:"destructive,omitempty"`
}

// CommandError is a command that ran and failed (as opposed to the pipe
// being unreachable).
type CommandError struct{ Message string }

func (e *CommandError) Error() string { return e.Message }

// errUnreachable wraps a failure to reach the pipe at all.
var errUnreachable = errors.New("can't reach Toru")

// ErrNotToru is a pipe that answered but isn't served by Toru running as
// this user; nothing is sent to it.
var ErrNotToru = errors.New("refusing to use the agent pipe")

// maxLine bounds one response line (screenshots come back base64).
const maxLine = 64 << 20

// Client talks to the agent pipe over one connection, redialled when it
// breaks (Toru restarted, or the toggle went off and on).
type Client struct {
	dial func(context.Context) (net.Conn, error)

	mu   sync.Mutex
	conn net.Conn
	rd   *bufio.Reader
	next int
}

// NewClient returns a client that connects with dial on first use.
func NewClient(dial func(context.Context) (net.Conn, error)) *Client {
	return &Client{dial: dial}
}

// Close drops the connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropLocked()
}

// List returns every command Toru offers right now.
func (c *Client) List(ctx context.Context) ([]CommandInfo, error) {
	raw, err := c.do(ctx, request{Method: "list"})
	if err != nil {
		return nil, err
	}
	var cmds []CommandInfo
	if err := json.Unmarshal(raw, &cmds); err != nil {
		return nil, fmt.Errorf("list: %w", err)
	}
	return cmds, nil
}

// Call runs one command and returns its raw JSON result.
func (c *Client) Call(ctx context.Context, name string, input json.RawMessage) (json.RawMessage, error) {
	return c.do(ctx, request{Method: "call", Name: name, Input: input})
}

func (c *Client) do(ctx context.Context, req request) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// A connection Toru has since closed fails on first use; retry once on
	// a fresh one so a restarted Toru doesn't need a restarted client.
	for attempt := 0; ; attempt++ {
		fresh := c.conn == nil
		if fresh {
			conn, err := c.dial(ctx)
			if errors.Is(err, ErrNotToru) {
				return nil, err
			}
			if err != nil {
				return nil, fmt.Errorf("%w: %v", errUnreachable, err)
			}
			c.conn, c.rd = conn, bufio.NewReaderSize(conn, 64<<10)
		}
		res, err := c.roundTripLocked(ctx, req)
		var cmdErr *CommandError
		if err == nil || errors.As(err, &cmdErr) {
			return res, err
		}
		_ = c.dropLocked()
		if fresh || attempt > 0 {
			return nil, fmt.Errorf("%w: %v", errUnreachable, err)
		}
	}
}

func (c *Client) roundTripLocked(ctx context.Context, req request) (json.RawMessage, error) {
	c.next++
	req.ID = c.next
	if dl, ok := ctx.Deadline(); ok {
		_ = c.conn.SetDeadline(dl)
		defer func() { _ = c.conn.SetDeadline(timeZero) }()
	}
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetDeadline(timePast) })
	defer stop()

	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	for {
		data, err := readLine(c.rd)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, err
		}
		var resp response
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("bad response: %w", err)
		}
		if resp.ID != req.ID {
			continue // a late answer to a request we gave up on
		}
		if resp.Error != nil {
			return nil, &CommandError{Message: resp.Error.Message}
		}
		if len(resp.Result) == 0 {
			return json.RawMessage("null"), nil
		}
		return resp.Result, nil
	}
}

func (c *Client) dropLocked() error {
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn, c.rd = nil, nil
	return err
}

// readLine reads one '\n'-terminated line of at most maxLine bytes.
func readLine(rd *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		chunk, err := rd.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return nil, fmt.Errorf("response over %d bytes", maxLine)
		}
		if err == nil {
			return buf, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return nil, err
		}
	}
}
