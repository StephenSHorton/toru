package main

import (
	"net"
	"net/netip"
)

// filter.go decides which peers the relay will talk to. A TURN server that
// relays anywhere is an open proxy into whatever network the VPS sits on, so
// every CreatePermission and ChannelBind runs through allowedPeer, and every
// packet to one of the relay's own addresses runs through ownAddrs.

// blockedPrefixes are peer ranges the relay never sends to or accepts from:
// private, loopback, link-local, CGNAT (which also covers the 100.96.0.0/16
// LAN overlay), and the reserved, documentation and multicast blocks. IPv6
// transition prefixes are blocked whole because they can carry a private IPv4
// address inside them (NAT64 64:ff9b::/96 maps straight onto it).
var blockedPrefixes = mustPrefixes(
	"0.0.0.0/8",       // "this network"
	"10.0.0.0/8",      // RFC 1918
	"100.64.0.0/10",   // CGNAT, RFC 6598 (and the Toru LAN overlay)
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local
	"172.16.0.0/12",   // RFC 1918
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // 6to4 relay anycast (deprecated)
	"192.168.0.0/16",  // RFC 1918
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, and 255.255.255.255
	"::/128",          // unspecified
	"::1/128",         // loopback
	"64:ff9b::/96",    // NAT64, well-known
	"64:ff9b:1::/48",  // NAT64, local use
	"100::/64",        // discard
	"2001:db8::/32",   // documentation
	"2002::/16",       // 6to4
	"fc00::/7",        // unique local
	"fe80::/10",       // link-local
	"ff00::/8",        // multicast
)

func mustPrefixes(ss ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		out[i] = netip.MustParsePrefix(s)
	}
	return out
}

// allowedPeer reports whether the relay may open a permission to ip. IPv4
// addresses written as IPv4-mapped IPv6 are checked as IPv4.
func allowedPeer(ip net.IP) bool {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false
	}
	a = a.Unmap()
	for _, p := range blockedPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// ownAddrs is the set of addresses the relay host itself answers on: the
// public relay address plus every interface address at start-up. Permissions
// to them are allowed (two peers both relayed through this server talk to
// each other's relay address, which is our own public IP), but a packet to one
// of them must go to a port in the relay range. That keeps SSH, the
// credential endpoint and the TURN listeners themselves out of reach.
type ownAddrs struct {
	addrs            map[netip.Addr]bool
	minPort, maxPort int
}

func newOwnAddrs(public net.IP, ifaceAddrs []net.Addr, minPort, maxPort int) *ownAddrs {
	o := &ownAddrs{addrs: map[netip.Addr]bool{}, minPort: minPort, maxPort: maxPort}
	if a, ok := netip.AddrFromSlice(public); ok {
		o.addrs[a.Unmap()] = true
	}
	for _, ia := range ifaceAddrs {
		var ip net.IP
		switch v := ia.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if a, ok := netip.AddrFromSlice(ip); ok {
			o.addrs[a.Unmap()] = true
		}
	}
	return o
}

// allowSend reports whether a relayed packet may go to dst. Anything that is
// not one of our own addresses was already vetted by allowedPeer.
func (o *ownAddrs) allowSend(dst net.Addr) bool {
	u, ok := dst.(*net.UDPAddr)
	if !ok {
		return false
	}
	a, ok := netip.AddrFromSlice(u.IP)
	if !ok {
		return false
	}
	if !o.addrs[a.Unmap()] {
		return true
	}
	return u.Port >= o.minPort && u.Port <= o.maxPort
}
