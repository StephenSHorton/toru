package main

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pion/turn/v5"
)

// Every socket in these tests is on 127.0.0.1: the relay, its relay ports,
// the TURN client and the echo peer. Nothing listens on another interface, so
// running them never raises a Windows Firewall prompt.

func testConfig() *config {
	return &config{
		Secret: testSecret, Realm: "toru", PublicIP: net.ParseIP("127.0.0.1").To4(),
		ListenIP: "127.0.0.1", RelayMinPort: 40000, RelayMaxPort: 40999,
		CredTTL: 10 * time.Minute, AllocLifetime: 10 * time.Minute, MaxPerUser: 4, MaxAllocs: 100,
	}
}

// startRelay runs the real server on loopback. permit is the peer filter;
// production's allowedPeer refuses 127.0.0.1, so the echo test passes its own.
func startRelay(t *testing.T, permit turn.PermissionHandler) (*config, string) {
	t.Helper()
	c := testConfig()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gen := newRelayGen(c, newOwnAddrs(nil, nil, c.RelayMinPort, c.RelayMaxPort))
	srv, err := newServer(c, gen, permit, []net.PacketConn{pc}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return c, pc.LocalAddr().String()
}

func newClient(t *testing.T, server, user, pass string) *turn.Client {
	t.Helper()
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cl, err := turn.NewClient(&turn.ClientConfig{
		STUNServerAddr: server, TURNServerAddr: server, Conn: conn,
		Username: user, Password: pass, Realm: "toru", RTO: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cl.Listen(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close(); _ = conn.Close() })
	return cl
}

func TestRelayEcho(t *testing.T) {
	loopbackOK := func(_ net.Addr, ip net.IP) bool { return ip.IsLoopback() }
	c, addr := startRelay(t, loopbackOK)

	user, pass, err := turn.GenerateLongTermTURNRESTCredentials(c.Secret, "peer1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	relayConn, err := newClient(t, addr, user, pass).Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	defer func() { _ = relayConn.Close() }()
	if p := relayConn.LocalAddr().(*net.UDPAddr).Port; p < c.RelayMinPort || p > c.RelayMaxPort {
		t.Errorf("relay port %d outside %d-%d", p, c.RelayMinPort, c.RelayMaxPort)
	}

	peer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = peer.Close() }()
	go func() { // echo
		buf := make([]byte, 1500)
		for {
			n, from, err := peer.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = peer.WriteTo(buf[:n], from)
		}
	}()

	msg := []byte("toru relay echo")
	if _, err := relayConn.WriteTo(msg, peer.LocalAddr()); err != nil {
		t.Fatalf("write via relay: %v", err)
	}
	_ = relayConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1500)
	n, _, err := relayConn.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read via relay: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Fatalf("echo %q", buf[:n])
	}
}

func TestRelayRejectsBadCredentials(t *testing.T) {
	c, addr := startRelay(t, permitPeers)
	user, _, err := turn.GenerateLongTermTURNRESTCredentials(c.Secret, "peer1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClient(t, addr, user, "d3JvbmcgcGFzc3dvcmQ=").Allocate(); err == nil {
		t.Fatal("allocate succeeded with a wrong password")
	}
	_, pass, err := turn.GenerateLongTermTURNRESTCredentials("some-other-secret-value", "peer1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newClient(t, addr, user, pass).Allocate(); err == nil {
		t.Fatal("allocate succeeded with a password from another secret")
	}
}

func TestRelayRefusesPrivatePeers(t *testing.T) {
	c, addr := startRelay(t, permitPeers)
	user, pass, err := turn.GenerateLongTermTURNRESTCredentials(c.Secret, "peer1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	cl := newClient(t, addr, user, pass)
	relayConn, err := cl.Allocate()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}
	defer func() { _ = relayConn.Close() }()
	for _, ip := range []string{"10.1.2.3", "192.168.0.10", "127.0.0.1", "169.254.169.254", "100.96.1.2"} {
		err := cl.CreatePermission(&net.UDPAddr{IP: net.ParseIP(ip), Port: 9})
		if err == nil {
			t.Errorf("permission to %s granted", ip)
		}
	}
}

func TestRelayGenQuotasAndTCP(t *testing.T) {
	c := testConfig()
	c.MaxPerUser, c.MaxAllocs = 2, 3
	gen := newRelayGen(c, newOwnAddrs(nil, nil, c.RelayMinPort, c.RelayMaxPort))
	if err := gen.Validate(); err != nil {
		t.Fatal(err)
	}
	alloc := func(user string) (net.PacketConn, error) {
		pc, _, err := gen.AllocatePacketConn(turn.AllocateListenerConfig{Network: "udp4", UserID: user})
		return pc, err
	}
	a1, err := alloc("a")
	if err != nil {
		t.Fatal(err)
	}
	a2, err := alloc("a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alloc("a"); !errors.Is(err, errUserQuota) {
		t.Fatalf("third allocation for one user: %v, want errUserQuota", err)
	}
	b1, err := alloc("b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alloc("c"); !errors.Is(err, errServerQuota) {
		t.Fatalf("allocation past the total: %v, want errServerQuota", err)
	}
	_ = a1.Close()
	_ = a1.Close() // double close must not release twice
	if gen.total != 2 {
		t.Fatalf("total %d after one close, want 2", gen.total)
	}
	a3, err := alloc("a")
	if err != nil {
		t.Fatalf("allocation after a close: %v", err)
	}
	for _, pc := range []net.PacketConn{a2, a3, b1} {
		_ = pc.Close()
	}

	if _, _, err := gen.AllocatePacketConn(turn.AllocateListenerConfig{Network: "udp4", UserID: "a", RequestedPort: 22}); !errors.Is(err, errPortOutOfRange) {
		t.Errorf("requested port 22: %v", err)
	}
	if _, _, err := gen.AllocateListener(turn.AllocateListenerConfig{Network: "tcp4"}); !errors.Is(err, errTCPRelay) {
		t.Errorf("tcp allocation: %v", err)
	}
	if _, err := gen.AllocateConn(turn.AllocateConnConfig{Network: "tcp4"}); !errors.Is(err, errTCPRelay) {
		t.Errorf("tcp connect: %v", err)
	}
}

func TestBucket(t *testing.T) {
	now := time.Unix(1000, 0)
	b := newBucket(1000, 1500, func() time.Time { return now }) // 1000 B/s, 1500 burst
	if !b.take(1200) || b.take(1200) {
		t.Fatal("burst not enforced")
	}
	now = now.Add(time.Second) // 300 + 1000
	if !b.take(1200) || b.take(200) {
		t.Fatal("refill wrong")
	}
	now = now.Add(time.Hour)
	if !b.full() {
		t.Fatal("not full after an hour")
	}
	if !newBucket(0, 0, time.Now).take(1 << 30) {
		t.Fatal("zero rate must be unlimited")
	}
}

// TestLimitedConnDrops checks the wrapper on a real loopback socket: past the
// budget, WriteTo pretends to send and nothing arrives.
func TestLimitedConnDrops(t *testing.T) {
	inner, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	sink, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close() }()
	now := time.Unix(1000, 0)
	gen := &relayGen{own: newOwnAddrs(nil, nil, 1, 65535), users: map[string]*userState{}}
	lc := &limitedConn{PacketConn: inner, gen: gen, user: "u",
		bw: newBucket(100, 1000, func() time.Time { return now })}
	defer func() { _ = lc.Close() }()

	pkt := make([]byte, 600)
	if n, err := lc.WriteTo(pkt, sink.LocalAddr()); err != nil || n != 600 {
		t.Fatal(n, err)
	}
	if n, err := lc.WriteTo(pkt, sink.LocalAddr()); err != nil || n != 600 { // over budget: dropped
		t.Fatal(n, err)
	}
	buf := make([]byte, 1500)
	_ = sink.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := sink.ReadFrom(buf); err != nil {
		t.Fatalf("first packet: %v", err)
	}
	_ = sink.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, _, err := sink.ReadFrom(buf); err == nil {
		t.Fatal("second packet arrived; the cap did not drop it")
	}
}

func TestParseConfig(t *testing.T) {
	env := map[string]string{
		"TORU_RELAY_SECRET":    testSecret,
		"TORU_RELAY_PUBLIC_IP": "203.0.113.10",
		"TORU_RELAY_UDP_PORT":  "3479",
	}
	getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	c, err := parseConfig([]string{"-udp-port", "3480"}, getenv)
	if err != nil {
		t.Fatal(err)
	}
	if c.Secret != testSecret || c.PublicIP.String() != "203.0.113.10" {
		t.Errorf("env not applied: %+v", c)
	}
	if c.UDPPort != 3480 {
		t.Errorf("flag should beat env: udp %d", c.UDPPort)
	}
	if c.TLSPort != 0 || c.HTTPSAddr != "" {
		t.Errorf("no cert, but tls %d https %q", c.TLSPort, c.HTTPSAddr)
	}
	if c.PublicHost != "203.0.113.10" {
		t.Errorf("public host %q", c.PublicHost)
	}

	env["TORU_RELAY_SECRET"] = "short"
	if _, err := parseConfig(nil, getenv); !errors.Is(err, errShortSecret) {
		t.Errorf("short secret: %v", err)
	}
	env["TORU_RELAY_SECRET"] = testSecret
	if _, err := parseConfig([]string{"-alloc-lifetime", "1h"}, getenv); err == nil || !strings.Contains(err.Error(), "lifetime") {
		t.Errorf("1h lifetime: %v", err)
	}
	if _, err := parseConfig([]string{"-public-ip", "::1"}, getenv); err == nil {
		t.Error("IPv6 public IP accepted")
	}
}
