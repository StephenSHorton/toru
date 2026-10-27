package client

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
	"github.com/StephenSHorton/toru/internal/lan/api"
)

// dialPipe connects to the service's named pipe. The pipe's SDDL lets
// interactive users connect; api.PipeClientAccess is the narrowest access,
// so this needs no elevation.
func dialPipe(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeAccess(ctx, api.PipeName, api.PipeClientAccess)
}
