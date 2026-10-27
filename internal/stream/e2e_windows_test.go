//go:build windows

package stream

import (
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/webrtc/v4"
)

// TestE2EEndToEnd goes live for real (screen capture, hardware encoder,
// public relays) and watches it from a second room peer with a pion viewer,
// checking that video frames arrive at the configured rate after live
// settings changes. Opt-in: it needs a GPU, a desktop session and the
// internet. Media stays on 127.0.0.1 (see loopbackOnly).
//
//	TORU_STREAM_E2E=1 go test ./internal/stream -run E2E -v
func TestE2EEndToEnd(t *testing.T) {
	if os.Getenv("TORU_STREAM_E2E") == "" {
		t.Skip("set TORU_STREAM_E2E=1 to run")
	}
	loopbackOnly(t)
	t.Setenv("APPDATA", t.TempDir())

	host := NewService()
	defer host.Shutdown()
	cfg := host.GetSettings()
	cfg.FPS, cfg.AutoBitrate, cfg.BitrateKbps, cfg.Width, cfg.Height = 60, false, 8000, 1280, 720
	if c := os.Getenv("TORU_STREAM_CODEC"); c != "" {
		cfg.Codec = c
	}
	if _, err := host.SetSettings(cfg); err != nil {
		t.Fatal(err)
	}
	st, err := host.CreateRoom()
	if err != nil {
		t.Fatal(err)
	}
	code := st.Room.Code
	if _, err := host.JoinCall(); err != nil {
		t.Fatal(err)
	}
	if _, err := host.GoLive(); err != nil {
		t.Fatal(err)
	}

	// The viewer: a bare room peer plus a pion receiver. The room callback
	// only queues the streamer's messages; the test goroutine acts on them.
	self := randHex(8)
	events := make(chan Envelope, 64)
	var room *Room
	room, err = OpenRoom(code, self, func(env Envelope) {
		if env.From != host.self {
			return
		}
		switch env.Type {
		case msgOffer, msgRestart, msgEnded, msgNotLive:
			events <- env
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()

	api := viewerAPI(t)
	// Shared across reconnects; measure takes deltas.
	var frames, bytes, audioPkts atomic.Int64
	var pc *webrtc.PeerConnection
	defer func() {
		if pc != nil {
			_ = pc.Close()
		}
	}()

	// connect watches under session the way Watch.tsx does: ask until an
	// offer for it arrives, answer, and wait for the connection.
	connect := func(session string) *webrtc.PeerConnection {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		var offer offerBody
	ask:
		for {
			if time.Now().After(deadline) {
				t.Fatal("no offer from the streamer")
			}
			_ = room.Send(host.self, msgWatch, watchBody{Session: session, Name: "e2e"})
			resend := time.After(3 * time.Second)
			for {
				select {
				case env := <-events:
					if env.Type == msgOffer {
						if o, err := decodeBody[offerBody](env); err == nil && o.Session == session {
							offer = o
							break ask
						}
					}
					continue
				case <-resend:
				}
				break
			}
		}
		t.Logf("offer: %+v", offer.Info)
		requireLoopback(t, "offer", offer.SDP)

		p, err := api.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
		if err != nil {
			t.Fatal(err)
		}
		p.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			t.Logf("track %s %s", tr.Kind(), tr.Codec().MimeType)
			for {
				pkt, _, err := tr.ReadRTP()
				if err != nil {
					return
				}
				if tr.Kind() == webrtc.RTPCodecTypeAudio {
					audioPkts.Add(1)
					continue
				}
				bytes.Add(int64(len(pkt.Payload)))
				if pkt.Marker {
					frames.Add(1)
				}
			}
		})
		if err := p.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
			t.Fatal(err)
		}
		ans, err := p.CreateAnswer(nil)
		if err != nil {
			t.Fatal(err)
		}
		gathered := webrtc.GatheringCompletePromise(p)
		if err := p.SetLocalDescription(ans); err != nil {
			t.Fatal(err)
		}
		<-gathered
		requireLoopback(t, "answer", p.LocalDescription().SDP)
		if err := room.Send(host.self, msgAnswer, answerBody{Session: session, SDP: p.LocalDescription().SDP}); err != nil {
			t.Fatal(err)
		}
		waitConnected := time.Now().Add(15 * time.Second)
		for p.ConnectionState() != webrtc.PeerConnectionStateConnected {
			if time.Now().After(waitConnected) {
				t.Fatalf("never connected (state %s)", p.ConnectionState())
			}
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(time.Second)
		return p
	}

	// rewatch waits for the streamer's restart of session (an encoder restart
	// closes every viewer, on purpose: see superviseVideo), then watches again
	// under a fresh session like Watch.tsx's start.
	rewatch := func(session string) string {
		t.Helper()
		timeout := time.After(20 * time.Second)
		for waiting := true; waiting; {
			select {
			case env := <-events:
				r, err := decodeBody[restartBody](env)
				if err != nil || r.Session != session {
					continue
				}
				switch env.Type {
				case msgRestart:
					waiting = false
				case msgEnded, msgNotLive:
					t.Fatalf("streamer sent %s instead of a restart", env.Type)
				}
			case <-timeout:
				t.Fatalf("no restart after a live change (state %s, host %+v)", pc.ConnectionState(), host.GetState().Live)
			}
		}
		_ = pc.Close()
		_ = room.Send(host.self, msgUnwatch, restartBody{Session: session})
		next := randHex(4)
		pc = connect(next)
		return next
	}

	measure := func(label string) (fps float64, mbps float64) {
		f0, b0 := frames.Load(), bytes.Load()
		time.Sleep(4 * time.Second)
		fps = float64(frames.Load()-f0) / 4
		mbps = float64(bytes.Load()-b0) * 8 / 4 / 1e6
		t.Logf("%s: %.1f fps, %.2f Mbps, audio packets so far %d, host %+v", label, fps, mbps, audioPkts.Load(), host.GetState().Live)
		return fps, mbps
	}

	session := randHex(4)
	pc = connect(session)
	// A still desktop still produces frames at the set rate (ddagrab
	// duplicates), so the rate is checkable.
	if fps, _ := measure("720p60"); fps < 45 {
		t.Errorf("720p60: got %.1f fps", fps)
	}

	// A live change restarts the encoder; the viewer is asked to reconnect
	// and then gets the new rate.
	cfg = host.GetSettings()
	cfg.FPS, cfg.Width, cfg.Height = 120, 0, 0
	if _, err := host.SetSettings(cfg); err != nil {
		t.Fatal(err)
	}
	session = rewatch(session)
	if fps, _ := measure("native 120"); fps < 90 {
		t.Errorf("native 120: got %.1f fps", fps)
	}
	if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		t.Errorf("viewer dropped after reconnecting: %s", pc.ConnectionState())
	}

	// Auto bitrate live: the room is told the formula's rate for the real
	// output size, and the viewer reconnects and keeps watching.
	cfg = host.GetSettings()
	cfg.AutoBitrate, cfg.FPS = true, 60
	if _, err := host.SetSettings(cfg); err != nil {
		t.Fatal(err)
	}
	session = rewatch(session)
	info := host.GetState().Live.Info
	if want := AutoBitrateKbps(info.Width, info.Height, 60, cfg.Codec); info.BitrateKbps != want {
		t.Errorf("auto: room told %d kbps, want %d", info.BitrateKbps, want)
	}
	if fps, _ := measure("native 60 auto"); fps < 45 {
		t.Errorf("native 60 auto: got %.1f fps", fps)
	}
	if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		t.Errorf("viewer dropped after switching to Auto: %s", pc.ConnectionState())
	}
	if v := host.GetState().Live.Viewers; len(v) != 1 || !v[0].Connected {
		t.Errorf("host viewers after the changes: %+v", v)
	}
	_ = room.Send(host.self, msgUnwatch, restartBody{Session: session})
}

// loopbackOnly keeps every pion peer connection in the test exe (the
// streamer's via newAPI, and the test's own) on 127.0.0.1: no LAN host
// candidates, no mDNS socket, no STUN (whose gathering socket can bind every
// interface). Binding a LAN address makes Windows Firewall ask about each
// freshly built test exe. Outbound relay traffic (MQTT) is unaffected.
func loopbackOnly(t *testing.T) {
	oldTune, oldServers := tuneICE, iceServers
	tuneICE = func(se *webrtc.SettingEngine) {
		se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
		se.SetIncludeLoopbackCandidate(true)
		se.SetIPFilter(func(ip net.IP) bool { return ip.IsLoopback() })
		se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	}
	iceServers = nil
	t.Cleanup(func() { tuneICE, iceServers = oldTune, oldServers })
}

// viewerAPI is newAPI (loopback only) receiving on one 127.0.0.1 socket
// with a deep buffer: at native resolution a keyframe lands as one burst of
// hundreds of packets, which a default socket buffer drops, and the bare
// viewer neither NACKs nor reassembles, so a lost marker packet is a lost
// frame. WebView2 has its own deep receive path.
func viewerAPI(t *testing.T) *webrtc.API {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadBuffer(32 << 20)
	mux := ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: conn})
	t.Cleanup(func() { _ = mux.Close() })
	loopback := tuneICE
	tuneICE = func(se *webrtc.SettingEngine) {
		loopback(se)
		se.SetICEUDPMux(mux)
	}
	defer func() { tuneICE = loopback }()
	api, err := newAPI()
	if err != nil {
		t.Fatal(err)
	}
	return api
}

// requireLoopback fails the test if sdp offers any non-loopback candidate.
func requireLoopback(t *testing.T, label, sdp string) {
	t.Helper()
	n := 0
	for _, line := range strings.Split(sdp, "\n") {
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			t.Fatalf("%s: bad candidate %q", label, line)
		}
		if ip := net.ParseIP(f[4]); ip == nil || !ip.IsLoopback() {
			t.Fatalf("%s: non-loopback candidate %q", label, strings.TrimSpace(line))
		}
		n++
	}
	if n == 0 {
		t.Fatalf("%s: no candidates", label)
	}
}
