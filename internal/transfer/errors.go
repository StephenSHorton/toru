package transfer

import (
	"errors"
	"net"
	"strings"
)

// errRelayUnreachable replaces the ~200-character Go dial chain ("dial ws://…:
// failed to WebSocket dial: … lookup relay.magic-wormhole.io: no such host")
// that an offline machine produces. The raw error still goes to the log.
var errRelayUnreachable = errors.New("couldn’t reach the wormhole relay — check your connection")

// errNobodyOffering is the receive-side connect timeout: the rendezvous happily
// opens a mailbox for any nameplate and then waits for a peer that, for a
// mistyped number or a sender that already stopped, never comes.
var errNobodyOffering = errors.New("nobody is offering that code — check the number and ask the sender if it is still waiting")

// friendlyError maps transport-level failures to one readable sentence for the
// page; anything it does not recognise passes through unchanged. Only DIAL
// failures are mapped (a reset mid-transfer is a different story and keeps
// its own text): a *net.DNSError, a *net.OpError whose Op is "dial", or the
// websocket library's handshake wording when it did not wrap the cause.
func friendlyError(err error) error {
	if err == nil {
		return nil
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return errRelayUnreachable
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return errRelayUnreachable
	}
	msg := err.Error()
	if strings.Contains(msg, "failed to WebSocket dial") || strings.Contains(msg, "dial tcp") {
		return errRelayUnreachable
	}
	return err
}
