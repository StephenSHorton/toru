package lan

import (
	"encoding/binary"
	"net/netip"
	"os"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.zx2c4.com/wireguard/tun"
	"gvisor.dev/gvisor/pkg/tcpip"
	gvsum "gvisor.dev/gvisor/pkg/tcpip/checksum"
	"gvisor.dev/gvisor/pkg/tcpip/header"
)

// fakeTUN is a channel-backed tun.Device: tests push "OS sent" packets into
// in and read "delivered to the OS" packets from out.
type fakeTUN struct {
	in     chan []byte
	out    chan []byte
	events chan tun.Event
}

func newFakeTUN() *fakeTUN {
	return &fakeTUN{in: make(chan []byte, 64), out: make(chan []byte, 512), events: make(chan tun.Event, 1)}
}

func (f *fakeTUN) File() *os.File           { return nil }
func (f *fakeTUN) MTU() (int, error)        { return api.MTU, nil }
func (f *fakeTUN) Name() (string, error)    { return "fake", nil }
func (f *fakeTUN) Events() <-chan tun.Event { return f.events }
func (f *fakeTUN) BatchSize() int           { return 1 }
func (f *fakeTUN) Close() error             { return nil }

func (f *fakeTUN) Read(bufs [][]byte, sizes []int, offset int) (int, error) {
	p, ok := <-f.in
	if !ok {
		return 0, os.ErrClosed
	}
	sizes[0] = copy(bufs[0][offset:], p)
	return 1, nil
}

func (f *fakeTUN) Write(bufs [][]byte, offset int) (int, error) {
	for _, b := range bufs {
		f.out <- append([]byte(nil), b[offset:]...)
	}
	return len(bufs), nil
}

var (
	tSelf  = netip.MustParseAddr("100.96.1.1")
	tPeer1 = netip.MustParseAddr("100.96.2.2")
	tPeer2 = netip.MustParseAddr("100.96.3.3")
	tPeer3 = netip.MustParseAddr("100.96.4.4")
)

const tOffset = 16 // wireguard-go's MessageTransportHeaderSize

func newTestShim(t *testing.T, p api.Policy, peers ...netip.Addr) (*shim, *fakeTUN) {
	t.Helper()
	f := newFakeTUN()
	s := newShim(f, tSelf, p)
	for _, ip := range peers {
		s.addPeer(ip)
	}
	return s, f
}

// readShim does one wireguard-go style Read and returns the packets.
func readShim(t *testing.T, s *shim, nbufs int) [][]byte {
	t.Helper()
	bufs := make([][]byte, nbufs)
	for i := range bufs {
		bufs[i] = make([]byte, 2048)
	}
	sizes := make([]int, nbufs)
	n, err := s.Read(bufs, sizes, tOffset)
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]byte, n)
	for i := range n {
		out[i] = bufs[i][tOffset : tOffset+sizes[i]]
	}
	return out
}

// writeShim hands the shim one decrypted packet the way wireguard-go does
// (behind tOffset bytes of headroom) and returns what reached the OS.
func writeShim(t *testing.T, s *shim, f *fakeTUN, pkt []byte) []byte {
	t.Helper()
	b := make([]byte, tOffset+len(pkt))
	copy(b[tOffset:], pkt)
	if _, err := s.Write([][]byte{b}, tOffset); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-f.out:
		return p
	default:
		return nil
	}
}

// udp4 builds an IPv4/UDP packet with valid checksums (via gvisor, so the
// shim's own checksum code is checked against an independent one).
func udp4(src, dst netip.Addr, sport, dport uint16, payload []byte) []byte {
	return ip4(src, dst, protoUDP, func(b []byte) {
		u := header.UDP(b)
		u.Encode(&header.UDPFields{SrcPort: sport, DstPort: dport, Length: uint16(len(b))})
		copy(b[udpHeaderLen:], payload)
		xsum := header.PseudoHeaderChecksum(header.UDPProtocolNumber, tcpip.AddrFrom4(src.As4()), tcpip.AddrFrom4(dst.As4()), uint16(len(b)))
		u.SetChecksum(^u.CalculateChecksum(gvsum.Checksum(payload, xsum)))
	}, udpHeaderLen+len(payload))
}

func tcp4(src, dst netip.Addr, sport, dport uint16) []byte {
	return ip4(src, dst, protoTCP, func(b []byte) {
		header.TCP(b).Encode(&header.TCPFields{SrcPort: sport, DstPort: dport, DataOffset: header.TCPMinimumSize, Flags: header.TCPFlagSyn, WindowSize: 1024})
	}, header.TCPMinimumSize)
}

func ip4(src, dst netip.Addr, proto byte, body func([]byte), bodyLen int) []byte {
	p := make([]byte, ipv4HeaderLen+bodyLen)
	h := header.IPv4(p)
	h.Encode(&header.IPv4Fields{
		TotalLength: uint16(len(p)), TTL: 64, Protocol: proto,
		SrcAddr: tcpip.AddrFrom4(src.As4()), DstAddr: tcpip.AddrFrom4(dst.As4()),
	})
	h.SetChecksum(^h.CalculateChecksum())
	body(p[ipv4HeaderLen:])
	return p
}

func TestChecksumMatchesGvisor(t *testing.T) {
	for _, n := range []int{0, 1, 7, 64, 1001} {
		b := make([]byte, n)
		for i := range b {
			b[i] = byte(i*31 + n)
		}
		if got, want := checksum(b, 0), ^gvsum.Checksum(b, 0); got != want {
			t.Fatalf("n=%d: checksum %x, gvisor %x", n, got, want)
		}
		p := udp4(tSelf, tPeer1, 1000, 2000, b)
		s := &shim{self: tSelf}
		wrapped := s.wrap(tPeer1, p)
		ip := header.IPv4(wrapped)
		if !ip.IsChecksumValid() {
			t.Fatalf("n=%d: bad IPv4 header checksum", n)
		}
		u := header.UDP(ip.Payload())
		if !u.IsChecksumValid(ip.SourceAddress(), ip.DestinationAddress(), gvsum.Checksum(u.Payload(), 0)) {
			t.Fatalf("n=%d: bad UDP checksum", n)
		}
	}
}

func TestShimUnicastPasses(t *testing.T) {
	s, f := newTestShim(t, api.Policy{}, tPeer1)
	p := udp4(tSelf, tPeer1, 1234, 7777, []byte("hi"))
	f.in <- p
	got := readShim(t, s, 4)
	if len(got) != 1 || string(got[0]) != string(p) {
		t.Fatalf("got %d packets, want the unicast unchanged", len(got))
	}
}

func TestShimFanoutWrapsPerPeer(t *testing.T) {
	for _, dst := range []netip.Addr{limitedBcast, subnetBcast, netip.MustParseAddr("224.0.2.60")} {
		s, f := newTestShim(t, api.Policy{}, tPeer1, tPeer2, tPeer3)
		inner := udp4(tSelf, dst, 5555, 6112, []byte("game here"))
		f.in <- inner
		// One buffer per Read: the other copies wait in pending.
		var got [][]byte
		for len(got) < 3 {
			got = append(got, readShim(t, s, 1)...)
		}
		seen := map[netip.Addr]bool{}
		for _, w := range got {
			h, ok := parseIPv4(w)
			if !ok || h.src != tSelf || h.proto != protoUDP {
				t.Fatalf("%v: bad wrapper header", dst)
			}
			seen[h.dst] = true
			u := w[h.hlen:]
			if binary.BigEndian.Uint16(u[0:]) != api.BroadcastPort || binary.BigEndian.Uint16(u[2:]) != api.BroadcastPort {
				t.Fatalf("%v: wrapper ports %d→%d", dst, binary.BigEndian.Uint16(u[0:]), binary.BigEndian.Uint16(u[2:]))
			}
			if string(u[8:12]) != api.BroadcastMarker || string(u[12:]) != string(inner) {
				t.Fatalf("%v: wrapper payload isn't marker + inner", dst)
			}
			if len(w) != len(inner)+wrapOverhead {
				t.Fatalf("%v: wrapper adds %d bytes, want %d", dst, len(w)-len(inner), wrapOverhead)
			}
		}
		if !seen[tPeer1] || !seen[tPeer2] || !seen[tPeer3] {
			t.Fatalf("%v: copies went to %v", dst, seen)
		}
		if s.stats.Fanout.Load() != 3 {
			t.Fatalf("fanout count %d", s.stats.Fanout.Load())
		}
	}
}

func TestShimOutgoingFilters(t *testing.T) {
	big := make([]byte, api.MTU-wrapOverhead-ipv4HeaderLen-udpHeaderLen+1)
	cases := []struct {
		name string
		pkt  []byte
		mdns bool
		pass bool
	}{
		{"igmp", ip4(tSelf, netip.MustParseAddr("224.0.0.22"), protoIGMP, func([]byte) {}, 8), false, false},
		{"llmnr", udp4(tSelf, llmnrGroup, 5355, 5355, []byte("q")), true, false},
		{"mdns off", udp4(tSelf, mdnsGroup, 5353, 5353, []byte("q")), false, false},
		{"mdns on", udp4(tSelf, mdnsGroup, 5353, 5353, []byte("q")), true, true},
		{"ssdp off", udp4(tSelf, ssdpGroup, 1900, 1900, []byte("q")), false, false},
		{"ssdp on", udp4(tSelf, ssdpGroup, 1900, 1900, []byte("q")), true, true},
		{"not a peer", udp4(tSelf, netip.MustParseAddr("100.96.9.9"), 1, 2, nil), false, false},
		{"internet", udp4(tSelf, netip.MustParseAddr("8.8.8.8"), 1, 53, nil), false, false},
		{"ipv6", append([]byte{0x60}, make([]byte, 39)...), false, false},
		{"bcast from another source", udp4(netip.MustParseAddr("100.96.7.7"), limitedBcast, 1, 2, nil), false, false},
		{"bcast too big", udp4(tSelf, limitedBcast, 1, 2, big), false, false},
		{"bcast at the limit", udp4(tSelf, limitedBcast, 1, 2, big[1:]), false, true},
	}
	for _, c := range cases {
		s, f := newTestShim(t, api.Policy{MDNS: c.mdns}, tPeer1)
		f.in <- c.pkt
		// A marker unicast after it: Read only returns once something passes.
		marker := udp4(tSelf, tPeer1, 9, 9, []byte("marker"))
		f.in <- marker
		got := readShim(t, s, 1)
		passed := string(got[0]) != string(marker)
		if passed != c.pass {
			t.Errorf("%s: passed=%v, want %v", c.name, passed, c.pass)
		}
	}
	s, f := newTestShim(t, api.Policy{}, tPeer1)
	f.in <- udp4(tSelf, limitedBcast, 1, 2, big)
	f.in <- udp4(tSelf, tPeer1, 9, 9, nil)
	readShim(t, s, 1)
	if s.stats.TooBig.Load() != 1 {
		t.Fatalf("TooBig = %d", s.stats.TooBig.Load())
	}
}

func wrapFrom(src netip.Addr, inner []byte) []byte {
	s := &shim{self: src}
	return s.wrap(tSelf, inner)
}

func TestShimIncoming(t *testing.T) {
	game := udp4(tPeer1, subnetBcast, 5555, 6112, []byte("server here"))
	cases := []struct {
		name   string
		policy api.Policy
		pkt    []byte
		want   []byte // nil = dropped
	}{
		{"unicast", api.Policy{}, udp4(tPeer1, tSelf, 1, 7777, []byte("x")), udp4(tPeer1, tSelf, 1, 7777, []byte("x"))},
		{"wrapped broadcast", api.Policy{}, wrapFrom(tPeer1, game), game},
		{"wrapped limited broadcast", api.Policy{}, wrapFrom(tPeer1, udp4(tPeer1, limitedBcast, 1, 6112, nil)), udp4(tPeer1, limitedBcast, 1, 6112, nil)},
		{"spoofed inner source", api.Policy{}, wrapFrom(tPeer1, udp4(tPeer2, subnetBcast, 1, 6112, nil)), nil},
		{"wrapped unicast", api.Policy{}, wrapFrom(tPeer1, udp4(tPeer1, tSelf, 1, 6112, nil)), nil},
		{"wrapped mdns, policy off", api.Policy{}, wrapFrom(tPeer1, udp4(tPeer1, mdnsGroup, 5353, 5353, nil)), nil},
		{"raw broadcast", api.Policy{}, udp4(tPeer1, subnetBcast, 1, 6112, nil), nil},
		{"from a non-peer", api.Policy{}, udp4(tPeer3, tSelf, 1, 7777, nil), nil},
		{"frame marker", api.Policy{}, udp4(tPeer1, tSelf, api.BroadcastPort, api.BroadcastPort, []byte(api.FrameMarker+"xxxx")), nil},
		{"udp 7787 without a marker", api.Policy{}, udp4(tPeer1, tSelf, 1, api.BroadcastPort, []byte("plain")), udp4(tPeer1, tSelf, 1, api.BroadcastPort, []byte("plain"))},
		{"smb", api.Policy{}, tcp4(tPeer1, tSelf, 50000, 445), nil},
		{"smb allowed", api.Policy{AllowServices: true}, tcp4(tPeer1, tSelf, 50000, 445), tcp4(tPeer1, tSelf, 50000, 445)},
		{"rdp udp", api.Policy{}, udp4(tPeer1, tSelf, 1, 3389, nil), nil},
		{"winrm", api.Policy{}, tcp4(tPeer1, tSelf, 1, 5986), nil},
		{"netbios broadcast", api.Policy{}, wrapFrom(tPeer1, udp4(tPeer1, subnetBcast, 137, 137, nil)), nil},
		{"netbios broadcast allowed", api.Policy{AllowServices: true}, wrapFrom(tPeer1, udp4(tPeer1, subnetBcast, 137, 137, nil)), udp4(tPeer1, subnetBcast, 137, 137, nil)},
		{"tcp 446", api.Policy{}, tcp4(tPeer1, tSelf, 1, 446), tcp4(tPeer1, tSelf, 1, 446)},
	}
	for _, c := range cases {
		s, f := newTestShim(t, c.policy, tPeer1, tPeer2)
		got := writeShim(t, s, f, c.pkt)
		if string(got) != string(c.want) {
			t.Errorf("%s: delivered %d bytes, want %d", c.name, len(got), len(c.want))
		}
	}
}

func TestShimIncomingTrailingBytes(t *testing.T) {
	// A wrapper with padding after it: only the inner packet is delivered.
	s, f := newTestShim(t, api.Policy{}, tPeer1)
	game := udp4(tPeer1, limitedBcast, 1, 6112, []byte("abc"))
	got := writeShim(t, s, f, append(wrapFrom(tPeer1, game), 0, 0, 0, 0))
	if string(got) != string(game) {
		t.Fatalf("delivered %d bytes, want %d", len(got), len(game))
	}
}

func TestShimRateLimit(t *testing.T) {
	s, f := newTestShim(t, api.Policy{}, tPeer1, tPeer2)
	w1 := wrapFrom(tPeer1, udp4(tPeer1, limitedBcast, 1, 6112, []byte("x")))
	w2 := wrapFrom(tPeer2, udp4(tPeer2, limitedBcast, 1, 6112, []byte("x")))
	delivered := 0
	for range 300 {
		if writeShim(t, s, f, w1) != nil {
			delivered++
		}
	}
	if delivered < bcastPktsPerSec || delivered > bcastPktsPerSec+5 {
		t.Fatalf("delivered %d of 300 in a burst, want about %d", delivered, bcastPktsPerSec)
	}
	// Per source: peer 2 has its own budget.
	if writeShim(t, s, f, w2) == nil {
		t.Fatal("peer 2 was limited by peer 1's traffic")
	}
	// The byte budget binds with big broadcasts.
	s, f = newTestShim(t, api.Policy{}, tPeer1)
	inner := udp4(tPeer1, limitedBcast, 1, 6112, make([]byte, 4000))
	bigW := wrapFrom(tPeer1, inner)
	delivered = 0
	for range 300 {
		if writeShim(t, s, f, bigW) != nil {
			delivered++
		}
	}
	if want := bcastBytesPerSec / len(inner); delivered < want-1 || delivered > want+1 {
		t.Fatalf("delivered %d big broadcasts in a burst, want about %d", delivered, want)
	}
	// Exhaust, then refill: 50 ms buys about 10 packets.
	for writeShim(t, s, f, w1) != nil {
	}
	time.Sleep(50 * time.Millisecond)
	if writeShim(t, s, f, w1) == nil {
		t.Fatal("budget never refilled")
	}
}

func TestShimRemovePeer(t *testing.T) {
	s, f := newTestShim(t, api.Policy{}, tPeer1)
	s.removePeer(tPeer1)
	if writeShim(t, s, f, udp4(tPeer1, tSelf, 1, 7777, nil)) != nil {
		t.Fatal("packet from a removed peer was delivered")
	}
}
