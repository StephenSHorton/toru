//go:build !windows

package agentctl

import (
	"context"
	"errors"
	"net"
)

var errNoPipe = errors.New("agent control needs Windows named pipes")

// PipeName has no meaning off Windows.
func PipeName() (string, error) { return "", errNoPipe }

// Server is a stub off Windows.
type Server struct{}

// Listen is not supported off Windows.
func Listen(*Registry, string) (*Server, error) { return nil, errNoPipe }

// Close does nothing.
func (s *Server) Close() error { return nil }

// Dial is not supported off Windows.
func Dial(context.Context, string) (net.Conn, error) { return nil, errNoPipe }
