//go:build !windows

package cli

import (
	"context"
	"net"

	"github.com/StephenSHorton/toru/internal/agentctl"
)

// DialToru connects to the agent pipe (Windows only; elsewhere it fails).
func DialToru(ctx context.Context) (net.Conn, error) { return agentctl.Dial(ctx, "") }

// AttachConsole is a no-op outside Windows.
func AttachConsole() {}
