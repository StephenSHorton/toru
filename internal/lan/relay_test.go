package lan

import (
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/pion/ice/v4"
	"github.com/pion/turn/v5"
)

// TestNodeRelayUpgrade: the main agents may only use relay candidates (a
// pion/turn server on 127.0.0.1), so the first path is relayed; the upgrade
// agent (host candidates only) must then take over, make-before-break, while
// a UDP stream keeps flowing.
func TestNodeRelayUpgrade(t *testing.T) {
	// The TURN server listens on loopback only: no firewall prompt.
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const realm, user, pass = "toru", "user", "pass"
	key := turn.GenerateAuthKey(user, realm, pass)
	srv, err := turn.NewServer(turn.ServerConfig{
		Realm: realm,
		AuthHandler: func(ra *turn.RequestAttributes) (string, []byte, bool) {
			return ra.Username, key, ra.Username == user
		},
		PacketConnConfigs: []turn.PacketConnConfig{{
			PacketConn:            pc,
			RelayAddressGenerator: &turn.RelayAddressGeneratorStatic{RelayAddress: net.IPv4(127, 0, 0, 1), Address: "127.0.0.1"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	old := upgradeBackoffMin
	upgradeBackoffMin = 2 * time.Second
	t.Cleanup(func() { upgradeBackoffMin = old })
	loopbackICE(t, ice.CandidateTypeRelay)

	servers := []api.ICEServer{
		{URLs: []string{fmt.Sprintf("turn:%s?transport=udp", pc.LocalAddr())}, Username: user, Credential: pass},
	}
	if got := iceURLs(servers, true, t.Logf); len(got) != 0 {
		t.Fatalf("direct agents got TURN URLs %v", got)
	}
	if got := iceURLs(servers, false, t.Logf); len(got) != 1 || got[0].Username != user || got[0].Password != pass {
		t.Fatalf("main agents got %v", got)
	}

	var mu sync.Mutex
	var paths []string
	a, b := pairWith(t, servers)
	a.OnChange(func() {
		if ps := a.Peers(); len(ps) == 1 {
			mu.Lock()
			if len(paths) == 0 || paths[len(paths)-1] != ps[0].Path {
				paths = append(paths, ps[0].Path)
			}
			mu.Unlock()
		}
	})
	if p := a.Peers()[0].Path; p != api.PathRelay {
		t.Fatalf("first path %q, want relay", p)
	}

	// A steady stream B → A across the switch.
	l, err := a.net.ListenUDPAddrPort(netip.AddrPortFrom(a.addr, 9100))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := b.net.DialUDPAddrPort(netip.AddrPort{}, netip.AddrPortFrom(a.addr, 9100))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	stop := make(chan struct{})
	sent := 0
	go func() {
		tk := time.NewTicker(10 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stop:
				return
			case <-tk.C:
				c.Write([]byte("tick"))
				mu.Lock()
				sent++
				mu.Unlock()
			}
		}
	}()
	got := 0
	go func() {
		buf := make([]byte, 64)
		for {
			if _, err := l.Read(buf); err != nil {
				return
			}
			mu.Lock()
			got++
			mu.Unlock()
		}
	}()

	waitFor(t, 30*time.Second, "the upgrade to direct", func() bool {
		return a.Peers()[0].Path == api.PathDirect && b.Peers()[0].Path == api.PathDirect
	})
	time.Sleep(time.Second) // past the old agent's close
	close(stop)
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	t.Logf("paths seen %v; %d of %d packets arrived", paths, got, sent)
	if lost := sent - got; lost > sent/20 {
		t.Fatalf("lost %d of %d packets across the switch", lost, sent)
	}
	if !connected(a.Peers(), "b") || !connected(b.Peers(), "a") {
		t.Fatal("not connected after the upgrade")
	}
}
