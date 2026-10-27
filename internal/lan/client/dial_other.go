//go:build !windows

package client

import (
	"context"
	"errors"
	"net"
)

func dialPipe(context.Context) (net.Conn, error) {
	return nil, errors.New("LAN needs Windows")
}
