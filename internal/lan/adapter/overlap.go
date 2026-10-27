// Package adapter brings the Toru LAN Wintun adapter up and down in the
// service: the adapter itself (fixed GUID, tunnel type Toru), its address and
// routes, metric 1 while LAN is on, the Private network category, and the two
// firewall rules the installer creates. Design: docs/lan.md, "Adapter:
// Wintun, Layer 3" and "Firewall and network profile".
package adapter

import (
	"fmt"
	"net/netip"
)

// Overlay is the overlay /16 (api.OverlayPrefix) parsed once.
var Overlay = netip.MustParsePrefix("100.96.0.0/16")

// Route is one row of the routing table, reduced to what the overlap check
// needs.
type Route struct {
	Prefix netip.Prefix
	Alias  string // owning interface, for the card ("Tailscale", "Ethernet")
	Ours   bool   // on the Toru LAN adapter itself (a re-run, or a stale config)
}

// RangeInUseError is returned by Up when another adapter already routes part
// of the overlay range. The service turns it into api.ErrRangeInUse with
// Alias as the detail.
type RangeInUseError struct {
	Prefix netip.Prefix
	Alias  string
}

func (e *RangeInUseError) Error() string {
	return fmt.Sprintf("address range %s in use by %q (%s)", Overlay, e.Alias, e.Prefix)
}

// findOverlap returns the first route that shares addresses with overlay.
// Default routes contain everything and are skipped (every PC has one), as
// are routes on our own adapter. Host routes for broadcast and multicast
// never overlap 100.96/16, so they need no special case.
func findOverlap(overlay netip.Prefix, routes []Route) (Route, bool) {
	for _, r := range routes {
		if r.Ours || !r.Prefix.IsValid() || r.Prefix.Bits() == 0 || !r.Prefix.Addr().Is4() {
			continue
		}
		if r.Prefix.Overlaps(overlay) {
			return r, true
		}
	}
	return Route{}, false
}

// ValidAddr reports whether a is a usable node address: inside the overlay
// and not the network (.0.0), the reserved gateway (.0.1, used by the
// Unidentified-network fallback) or the subnet broadcast (.255.255).
func ValidAddr(a netip.Addr) bool {
	if !a.Is4() || !Overlay.Contains(a) {
		return false
	}
	b := a.As4()
	host := uint16(b[2])<<8 | uint16(b[3])
	return host != 0 && host != 1 && host != 0xffff
}
