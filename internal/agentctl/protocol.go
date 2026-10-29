package agentctl

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// The pipe protocol (docs/agent-control.md): one JSON request per line, one
// JSON response per line, answered in order.
//
//	{"id":1,"method":"list"}
//	{"id":2,"method":"call","name":"editor.split","input":{"t":12.5}}
//	-> {"id":2,"result":{...}}  or  {"id":2,"error":{"message":"..."}}

// Request is one line a client sends.
type Request struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method"` // "list" | "call"
	Name   string          `json:"name,omitempty"`
	Input  json.RawMessage `json:"input,omitempty"`
}

// Response is one line Toru sends back.
type Response struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Result any             `json:"result,omitempty"`
	Error  *ErrorBody      `json:"error,omitempty"`
}

// ErrorBody is a failed request's error.
type ErrorBody struct {
	Message string `json:"message"`
}

// MaxLine bounds one request line (a call's input can carry a project).
const MaxLine = 16 << 20

// callTimeout bounds one call so a stuck handler can't hold a client forever.
const callTimeout = 5 * time.Minute

// serveConn answers requests on conn until it closes or ctx ends.
func serveConn(ctx context.Context, reg *Registry, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), MaxLine)
	enc := json.NewEncoder(conn)
	var wmu sync.Mutex
	write := func(r Response) error {
		wmu.Lock()
		defer wmu.Unlock()
		return enc.Encode(r)
	}
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var req Request
		if err := json.Unmarshal(line, &req); err != nil {
			if write(Response{Error: &ErrorBody{Message: "bad request: " + err.Error()}}) != nil {
				return
			}
			continue
		}
		if write(handle(ctx, reg, req)) != nil {
			return
		}
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		_ = write(Response{Error: &ErrorBody{Message: "read: " + err.Error()}})
	}
}

// handle answers one request.
func handle(ctx context.Context, reg *Registry, req Request) Response {
	resp := Response{ID: req.ID}
	switch req.Method {
	case "list":
		resp.Result = reg.Infos()
	case "call":
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		defer cancel()
		res, err := reg.Call(cctx, req.Name, req.Input)
		if err != nil {
			resp.Error = &ErrorBody{Message: err.Error()}
			break
		}
		if res == nil {
			res = struct{}{}
		}
		resp.Result = res
	default:
		resp.Error = &ErrorBody{Message: `unknown method "` + req.Method + `" (use "list" or "call")`}
	}
	return resp
}
