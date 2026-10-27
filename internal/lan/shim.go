package lan

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.zx2c4.com/wireguard/tun"
)

// shim.go is the tun.Device between the real adapter (Wintun, or netstack in
// tests) and wireguard-go. WireGuard picks a peer per packet by destination,
// so broadcast and multicast match nobody and are dropped; the shim copies
// them to every peer instead, each copy wrapped as unicast UDP to the peer's
// overlay IP on api.BroadcastPort behind the 4-byte api.BroadcastMarker. On
// the way in it unwraps those copies, checks the inner source against the
// sender (anti-spoofing), rate-limits each source and blocks the Windows
// services ports. See docs/lan.md, "Broadcast and LAN discovery".
//
// Direction names follow wireguard-go: Read is outgoing (the OS sent it, we
// hand it to WireGuard to encrypt), Write is incoming (WireGuard decrypted it,
// we hand it to the OS).

const (
	ipv4HeaderLen = 20
	udpHeaderLen  = 8
	// wrapOverhead is what the broadcast wrapper adds: an IPv4 and a UDP
	// header plus the marker.
	wrapOverhead = ipv4HeaderLen + udpHeaderLen + len(api.BroadcastMarker)

	protoICMP = 1
	protoIGMP = 2
	protoTCP  = 6
	protoUDP  = 17

	// Per-source limits on incoming broadcast (docs/lan.md, Filters).
	bcastPktsPerSec  = 200
	bcastBytesPerSec = 256 << 10
)

var (
	limitedBcast = netip.AddrFrom4([4]byte{255, 255, 255, 255})
	subnetBcast  = netip.MustParseAddr(api.OverlayBcast)
	overlay      = netip.MustParsePrefix(api.OverlayPrefix)
	multicast4   = netip.MustParsePrefix("224.0.0.0/4")
	mdnsGroup    = netip.MustParseAddr("224.0.0.251")
	llmnrGroup   = netip.MustParseAddr("224.0.0.252")
	ssdpGroup    = netip.MustParseAddr("239.255.255.250")
	markerBcast  = []byte(api.BroadcastMarker)
	markerFrame  = []byte(api.FrameMarker)
)

// servicePorts are the Windows services the shim closes between members unless
// Policy.AllowServices: SMB, RPC, NetBIOS, RDP and WinRM, TCP and UDP alike.
func servicePort(port uint16) bool {
	switch {
	case port == 445, port == 135, port >= 137 && port <= 139, port == 3389, port == 5985, port == 5986:
		return true
	}
	return false
}

// shimStats counts what the shim dropped, for logs and tests.
type shimStats struct {
	Fanout     atomic.Uint64 // broadcast copies sent (one per peer)
	Unwrapped  atomic.Uint64 // broadcast copies delivered to the OS
	TooBig     atomic.Uint64 // broadcast larger than MTU-32, not sent
	Filtered   atomic.Uint64 // IGMP, LLMNR, mDNS/SSDP with the policy off, IPv6, off-overlay
	Spoofed    atomic.Uint64 // inner source didn't match the sending peer
	Blocked    atomic.Uint64 // inbound to a Windows services port
	RateLimit  atomic.Uint64 // over the per-source broadcast budget
	Malformed  atomic.Uint64 // too short, bad header
	NotForUs   atomic.Uint64 // unicast from a peer to an address that isn't ours
	UnknownSrc atomic.Uint64 // from an address that isn't a peer
}

func (st *shimStats) String() string {
	return fmt.Sprintf("fanout=%d unwrapped=%d toobig=%d filtered=%d spoofed=%d blocked=%d ratelimit=%d malformed=%d notforus=%d unknownsrc=%d",
		st.Fanout.Load(), st.Unwrapped.Load(), st.TooBig.Load(), st.Filtered.Load(), st.Spoofed.Load(),
		st.Blocked.Load(), st.RateLimit.Load(), st.Malformed.Load(), st.NotForUs.Load(), st.UnknownSrc.Load())
}

type shim struct {
	dev  tun.Device
	self netip.Addr
	mtu  int

	// peers maps each peer's overlay IP to whether its ICE path is up.
	// Copy-on-write: Read and Write only load. Broadcast fans out to up peers
	// only, so Windows chatter (a NetBIOS broadcast, say) to a peer still
	// connecting doesn't start a WireGuard handshake with no path, which
	// would hold the real one back by WireGuard's 5 s retry.
	peers  atomic.Pointer[map[netip.Addr]bool]
	peerMu sync.Mutex // serialises the copy-on-write updates
	policy atomic.Pointer[api.Policy]
	ipID   atomic.Uint32

	// Read side; wireguard-go calls Read from one goroutine only.
	pending [][]byte // packets owed to the caller (fan-out copies don't fit one Read)
	scratch [][]byte
	sizes   []int

	limMu sync.Mutex
	lim   map[netip.Addr]*bucket

	stats shimStats
}

func newShim(dev tun.Device, self netip.Addr, p api.Policy) *shim {
	s := &shim{dev: dev, self: self, mtu: api.MTU, lim: map[netip.Addr]*bucket{}}
	if m, err := dev.MTU(); err == nil && m > 0 {
		s.mtu = m
	}
	empty := map[netip.Addr]bool{}
	s.peers.Store(&empty)
	s.policy.Store(&p)
	return s
}

func (s *shim) setPolicy(p api.Policy) { s.policy.Store(&p) }

// addPeer adds ip with its path not up yet (a known peer keeps its state).
func (s *shim) addPeer(ip netip.Addr) {
	s.update(func(m map[netip.Addr]bool) { m[ip] = m[ip] })
}

// setUp records whether ip's ICE path is up (broadcast goes to up peers only).
func (s *shim) setUp(ip netip.Addr, up bool) {
	s.update(func(m map[netip.Addr]bool) {
		if _, ok := m[ip]; ok {
			m[ip] = up
		}
	})
}

func (s *shim) removePeer(ip netip.Addr) {
	s.update(func(m map[netip.Addr]bool) { delete(m, ip) })
	s.limMu.Lock()
	delete(s.lim, ip)
	s.limMu.Unlock()
}

func (s *shim) update(fn func(map[netip.Addr]bool)) {
	s.peerMu.Lock()
	defer s.peerMu.Unlock()
	old := *s.peers.Load()
	m := make(map[netip.Addr]bool, len(old)+1)
	for k, v := range old {
		m[k] = v
	}
	fn(m)
	s.peers.Store(&m)
}

func (s *shim) isPeer(ip netip.Addr) bool {
	_, ok := (*s.peers.Load())[ip]
	return ok
}

// tun.Device plumbing: everything but Read and Write is the real device's.

func (s *shim) File() *os.File           { return s.dev.File() }
func (s *shim) MTU() (int, error)        { return s.dev.MTU() }
func (s *shim) Name() (string, error)    { return s.dev.Name() }
func (s *shim) Events() <-chan tun.Event { return s.dev.Events() }
func (s *shim) Close() error             { return s.dev.Close() }

// BatchSize lets one Read carry several fan-out copies even when the real
// device reads one packet at a time (Wintun and netstack both do).
func (s *shim) BatchSize() int { return max(s.dev.BatchSize(), 16) }

// Read hands wireguard-go the OS's outgoing packets: unicast to a peer as is,
// broadcast and multicast as one wrapped copy per peer, everything else
// dropped. It never returns zero packets without an error.
func (s *shim) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	for len(s.pending) == 0 {
		inner := s.dev.BatchSize()
		if len(s.scratch) < inner {
			s.scratch = make([][]byte, inner)
			s.sizes = make([]int, inner)
		}
		for i := range inner {
			if cap(s.scratch[i]) < len(bufs[0]) {
				s.scratch[i] = make([]byte, len(bufs[0]))
			}
			s.scratch[i] = s.scratch[i][:len(bufs[0])]
		}
		n, err := s.dev.Read(s.scratch[:inner], s.sizes[:inner], offset)
		for i := range n {
			if s.sizes[i] > 0 {
				s.outgoing(s.scratch[i][offset : offset+s.sizes[i]])
			}
		}
		if err != nil {
			if len(s.pending) > 0 {
				break // deliver what we have; the error comes back on the next Read
			}
			return 0, err
		}
	}
	n := 0
	for n < len(bufs) && len(s.pending) > 0 {
		sizes[n] = copy(bufs[n][offset:], s.pending[0])
		s.pending[0] = nil
		s.pending = s.pending[1:]
		n++
	}
	if len(s.pending) == 0 {
		s.pending = s.pending[:0:0] // drop the backing array's stale head
	}
	return n, nil
}

// outgoing classifies one packet from the OS and queues what WireGuard should
// send. Unicast is queued as a view into scratch, which is safe because Read
// doesn't read the device again until pending is empty.
func (s *shim) outgoing(pkt []byte) {
	h, ok := parseIPv4(pkt)
	if !ok {
		if len(pkt) > 0 && pkt[0]>>4 == 6 {
			s.stats.Filtered.Add(1) // IPv6 is not on the overlay yet
		} else {
			s.stats.Malformed.Add(1)
		}
		return
	}
	if s.isPeer(h.dst) {
		s.pending = append(s.pending, pkt)
		return
	}
	if !s.fanoutAllowed(h) {
		s.stats.Filtered.Add(1)
		return
	}
	if h.src != s.self {
		// The receiving shim would drop it as spoofed anyway.
		s.stats.Filtered.Add(1)
		return
	}
	if len(pkt) > s.mtu-wrapOverhead {
		s.stats.TooBig.Add(1)
		return
	}
	for ip, up := range *s.peers.Load() {
		if !up {
			continue
		}
		s.pending = append(s.pending, s.wrap(ip, pkt))
		s.stats.Fanout.Add(1)
	}
}

// fanoutAllowed is the broadcast and multicast filter, used both ways: the
// destination must be a broadcast or multicast address, IGMP and LLMNR never
// cross, and mDNS and SSDP only with Policy.MDNS.
func (s *shim) fanoutAllowed(h ipv4Header) bool {
	switch {
	case h.dst == limitedBcast, h.dst == subnetBcast:
	case multicast4.Contains(h.dst):
		if h.proto == protoIGMP || h.dst == llmnrGroup {
			return false
		}
		if (h.dst == mdnsGroup || h.dst == ssdpGroup) && !s.policy.Load().MDNS {
			return false
		}
	default:
		return false
	}
	return true
}

// wrap builds IPv4(self → peer) / UDP(7787 → 7787) / "TBC1" / inner.
func (s *shim) wrap(peer netip.Addr, inner []byte) []byte {
	total := wrapOverhead + len(inner)
	p := make([]byte, total)
	src, dst := s.self.As4(), peer.As4()
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], uint16(total))
	binary.BigEndian.PutUint16(p[4:], uint16(s.ipID.Add(1)))
	binary.BigEndian.PutUint16(p[6:], 0x4000) // DF: never fragment a wrapper
	p[8] = 64
	p[9] = protoUDP
	copy(p[12:16], src[:])
	copy(p[16:20], dst[:])
	binary.BigEndian.PutUint16(p[10:], checksum(p[:ipv4HeaderLen], 0))

	u := p[ipv4HeaderLen:]
	binary.BigEndian.PutUint16(u[0:], api.BroadcastPort)
	binary.BigEndian.PutUint16(u[2:], api.BroadcastPort)
	binary.BigEndian.PutUint16(u[4:], uint16(len(u)))
	copy(u[udpHeaderLen:], markerBcast)
	copy(u[udpHeaderLen+len(markerBcast):], inner)
	binary.BigEndian.PutUint16(u[6:], udpChecksum(src, dst, u))
	return p
}

// Write hands the OS what WireGuard decrypted, after the inbound checks.
// Unwrapped broadcasts are passed by re-slicing, so the inner packet sits at
// offset with no copy.
func (s *shim) Write(bufs [][]byte, offset int) (int, error) {
	out := bufs[:0:0]
	for _, b := range bufs {
		if len(b) <= offset {
			continue
		}
		if start, end, ok := s.incoming(b[offset:]); ok {
			out = append(out, b[start:offset+end])
		}
	}
	if len(out) > 0 {
		if _, err := s.dev.Write(out, offset); err != nil {
			return 0, err
		}
	}
	return len(bufs), nil
}

// incoming decides one decrypted packet: ok reports whether to deliver it,
// and pkt[start:end] is what to deliver (the inner packet, for broadcast).
func (s *shim) incoming(pkt []byte) (start, end int, ok bool) {
	h, ok := parseIPv4(pkt)
	if !ok {
		if len(pkt) > 0 && pkt[0]>>4 == 6 {
			s.stats.Filtered.Add(1)
		} else {
			s.stats.Malformed.Add(1)
		}
		return 0, 0, false
	}
	// WireGuard's AllowedIPs already pins the source to the peer that sent
	// it; this catches a peer table out of step with the device.
	if !s.isPeer(h.src) {
		s.stats.UnknownSrc.Add(1)
		return 0, 0, false
	}
	if h.dst != s.self {
		// Broadcast always arrives wrapped, so a peer can't skip the filters
		// and rate limit by sending it raw.
		s.stats.NotForUs.Add(1)
		return 0, 0, false
	}
	if inner, isWrap := s.unwrapPayload(h, pkt); isWrap {
		if inner == nil {
			return 0, 0, false
		}
		ih, ok := parseIPv4(inner)
		switch {
		case !ok:
			s.stats.Malformed.Add(1)
			return 0, 0, false
		case ih.src != h.src:
			s.stats.Spoofed.Add(1)
			return 0, 0, false
		case !s.fanoutAllowed(ih):
			s.stats.Filtered.Add(1)
			return 0, 0, false
		case s.blocked(ih, inner):
			s.stats.Blocked.Add(1)
			return 0, 0, false
		case !s.allow(h.src, len(inner)):
			s.stats.RateLimit.Add(1)
			return 0, 0, false
		}
		s.stats.Unwrapped.Add(1)
		start = h.hlen + udpHeaderLen + len(markerBcast)
		return start, start + ih.total, true
	}
	if s.blocked(h, pkt) {
		s.stats.Blocked.Add(1)
		return 0, 0, false
	}
	return 0, h.total, true
}

// unwrapPayload recognises a broadcast wrapper: UDP to BroadcastPort whose
// payload starts with a marker. isWrap with a nil inner means drop (the
// reserved frame marker, or a truncated wrapper).
func (s *shim) unwrapPayload(h ipv4Header, pkt []byte) (inner []byte, isWrap bool) {
	if h.proto != protoUDP || h.frag != 0 || len(pkt) < h.hlen+udpHeaderLen {
		return nil, false
	}
	u := pkt[h.hlen:h.total]
	if binary.BigEndian.Uint16(u[2:]) != api.BroadcastPort {
		return nil, false
	}
	ulen := int(binary.BigEndian.Uint16(u[4:]))
	if ulen < udpHeaderLen || ulen > len(u) {
		return nil, false
	}
	payload := u[udpHeaderLen:ulen]
	switch {
	case len(payload) >= len(markerBcast) && string(payload[:len(markerBcast)]) == api.BroadcastMarker:
		return payload[len(markerBcast):], true
	case len(payload) >= len(markerFrame) && string(payload[:len(markerFrame)]) == api.FrameMarker:
		s.stats.Filtered.Add(1) // reserved for an L2 backend
		return nil, true
	}
	return nil, false
}

// blocked reports inbound TCP or UDP to a Windows services port while
// Policy.AllowServices is off. A non-first fragment has no ports; its first
// fragment was already dropped, so reassembly can't complete.
func (s *shim) blocked(h ipv4Header, pkt []byte) bool {
	if s.policy.Load().AllowServices || (h.proto != protoTCP && h.proto != protoUDP) || h.frag != 0 {
		return false
	}
	if len(pkt) < h.hlen+4 {
		return true // can't tell which port: don't let it through
	}
	return servicePort(binary.BigEndian.Uint16(pkt[h.hlen+2:]))
}

// allow charges one broadcast of n bytes to src's token buckets.
func (s *shim) allow(src netip.Addr, n int) bool {
	now := time.Now()
	s.limMu.Lock()
	defer s.limMu.Unlock()
	b := s.lim[src]
	if b == nil {
		b = &bucket{pkts: bcastPktsPerSec, bytes: bcastBytesPerSec, at: now}
		s.lim[src] = b
	}
	return b.take(now, n)
}

// bucket is a pair of token buckets (packets and bytes) refilled per second
// and capped at one second's worth.
type bucket struct {
	pkts, bytes float64
	at          time.Time
}

func (b *bucket) take(now time.Time, n int) bool {
	dt := now.Sub(b.at).Seconds()
	b.at = now
	b.pkts = min(b.pkts+dt*bcastPktsPerSec, bcastPktsPerSec)
	b.bytes = min(b.bytes+dt*bcastBytesPerSec, bcastBytesPerSec)
	if b.pkts < 1 || b.bytes < float64(n) {
		return false
	}
	b.pkts--
	b.bytes -= float64(n)
	return true
}

// ipv4Header is the part of an IPv4 header the shim looks at.
type ipv4Header struct {
	hlen, total int
	proto       byte
	frag        uint16 // fragment offset in 8-byte units
	src, dst    netip.Addr
}

// parseIPv4 validates pkt as one IPv4 packet: version, header length and a
// total length that fits pkt (trailing padding is allowed).
func parseIPv4(pkt []byte) (ipv4Header, bool) {
	if len(pkt) < ipv4HeaderLen || pkt[0]>>4 != 4 {
		return ipv4Header{}, false
	}
	h := ipv4Header{
		hlen:  int(pkt[0]&0x0f) * 4,
		total: int(binary.BigEndian.Uint16(pkt[2:])),
		proto: pkt[9],
		frag:  binary.BigEndian.Uint16(pkt[6:]) & 0x1fff,
		src:   netip.AddrFrom4([4]byte(pkt[12:16])),
		dst:   netip.AddrFrom4([4]byte(pkt[16:20])),
	}
	if h.hlen < ipv4HeaderLen || h.total < h.hlen || h.total > len(pkt) {
		return ipv4Header{}, false
	}
	return h, true
}

// checksum is the Internet checksum (RFC 1071) of b, starting from sum.
func checksum(b []byte, sum uint32) uint16 {
	for len(b) >= 2 {
		sum += uint32(b[0])<<8 | uint32(b[1])
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = sum&0xffff + sum>>16
	}
	return ^uint16(sum)
}

// udpChecksum is the UDP checksum over the IPv4 pseudo-header and u (header
// with a zero checksum field, plus payload). Zero means "none" in UDP, so a
// computed zero is sent as 0xffff.
func udpChecksum(src, dst [4]byte, u []byte) uint16 {
	var sum uint32
	sum += uint32(src[0])<<8 | uint32(src[1])
	sum += uint32(src[2])<<8 | uint32(src[3])
	sum += uint32(dst[0])<<8 | uint32(dst[1])
	sum += uint32(dst[2])<<8 | uint32(dst[3])
	sum += protoUDP
	sum += uint32(len(u))
	c := checksum(u, sum)
	if c == 0 {
		c = 0xffff
	}
	return c
}
