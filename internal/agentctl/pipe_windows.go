//go:build windows

package agentctl

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// PipeName is this user's agent pipe: \\.\pipe\toru-agent-<user SID>. The
// mcp/ctl entry points dial the same name.
func PipeName() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	return `\\.\pipe\toru-agent-` + sid, nil
}

// pipeSDDL lets only sid (and nobody else, not even admins) open the pipe.
func pipeSDDL(sid string) string { return "D:P(A;;GA;;;" + sid + ")" }

func currentUserSID() (string, error) {
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("agent pipe: current user: %w", err)
	}
	return u.User.Sid.String(), nil
}

// Server is the agent pipe: it accepts clients and answers their requests
// from the registry until Close.
type Server struct {
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Listen creates the pipe at name (PipeName() when empty), readable and
// writable only by the current user, and starts serving reg.
func Listen(reg *Registry, name string) (*Server, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = `\\.\pipe\toru-agent-` + sid
	}
	ln, err := winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL(sid),
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
	if err != nil {
		return nil, fmt.Errorf("agent pipe %s: %w", name, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{ln: ln, cancel: cancel}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, winio.ErrPipeListenerClosed) || errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
					return
				}
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				serveConn(ctx, reg, conn)
			}()
		}
	}()
	return s, nil
}

// Close removes the pipe, disconnects every client and waits for them.
func (s *Server) Close() error {
	s.cancel()
	err := s.ln.Close()
	s.wg.Wait()
	return err
}

// Dial connects to the agent pipe at name (PipeName() when empty).
func Dial(ctx context.Context, name string) (net.Conn, error) {
	if name == "" {
		var err error
		if name, err = PipeName(); err != nil {
			return nil, err
		}
	}
	return winio.DialPipeContext(ctx, name)
}
