//go:build windows

package stream

import (
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// TestEndToEnd goes live for real (screen capture, hardware encoder, public
// relays) and watches it from a second room peer with a pion viewer, checking
// that video frames arrive at the configured rate after a live settings change.
// Opt-in: it needs a GPU, a desktop session and the internet.
//
//	TORU_STREAM_E2E=1 go test ./internal/stream -run EndToEnd -v
func TestEndToEnd(t *testing.T) {
	if os.Getenv("TORU_STREAM_E2E") == "" {
		t.Skip("set TORU_STREAM_E2E=1 to run")
	}
	t.Setenv("APPDATA", t.TempDir())

	host := NewService()
	defer host.Shutdown()
	cfg := host.GetSettings()
	cfg.FPS, cfg.BitrateKbps, cfg.Width, cfg.Height = 60, 8000, 1280, 720
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
	if _, err := host.GoLive(); err != nil {
		t.Fatal(err)
	}

	// The viewer: a bare room peer plus a pion receiver.
	self := randHex(8)
	offers := make(chan offerBody, 4)
	var room *Room
	room, err = OpenRoom(code, self, func(env Envelope) {
		if env.Type == msgOffer && env.From == host.self {
			if o, err := decodeBody[offerBody](env); err == nil {
				offers <- o
			}
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()

	session := randHex(4)
	deadline := time.Now().Add(20 * time.Second)
	var offer offerBody
	for got := false; !got; {
		if time.Now().After(deadline) {
			t.Fatal("no offer from the streamer")
		}
		_ = room.Send(host.self, msgWatch, watchBody{Session: session, Name: "e2e"})
		select {
		case offer = <-offers:
			got = true
		case <-time.After(3 * time.Second):
		}
	}
	t.Logf("offer: %+v", offer.Info)

	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	pc, err := webrtc.NewAPI(webrtc.WithMediaEngine(m)).NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	var frames, bytes, audioPkts atomic.Int64
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		t.Logf("track %s %s", tr.Kind(), tr.Codec().MimeType)
		for {
			p, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			if tr.Kind() == webrtc.RTPCodecTypeAudio {
				audioPkts.Add(1)
				continue
			}
			bytes.Add(int64(len(p.Payload)))
			if p.Marker {
				frames.Add(1)
			}
		}
	})
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		t.Fatal(err)
	}
	ans, err := pc.CreateAnswer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(ans); err != nil {
		t.Fatal(err)
	}
	<-gathered
	if err := room.Send(host.self, msgAnswer, answerBody{Session: session, SDP: pc.LocalDescription().SDP}); err != nil {
		t.Fatal(err)
	}

	measure := func(label string) (fps float64, mbps float64) {
		f0, b0 := frames.Load(), bytes.Load()
		time.Sleep(4 * time.Second)
		fps = float64(frames.Load()-f0) / 4
		mbps = float64(bytes.Load()-b0) * 8 / 4 / 1e6
		t.Logf("%s: %.1f fps, %.2f Mbps, audio packets so far %d, host %+v", label, fps, mbps, audioPkts.Load(), host.GetState().Live)
		return fps, mbps
	}

	waitConnected := time.Now().Add(15 * time.Second)
	for pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		if time.Now().After(waitConnected) {
			t.Fatalf("never connected (state %s)", pc.ConnectionState())
		}
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(time.Second)
	// A still desktop still produces frames at the set rate (ddagrab
	// duplicates), so the rate is checkable.
	if fps, _ := measure("720p60"); fps < 45 {
		t.Errorf("720p60: got %.1f fps", fps)
	}

	// A live change keeps the viewer connected and changes the rate.
	cfg = host.GetSettings()
	cfg.FPS, cfg.Width, cfg.Height = 120, 0, 0
	if _, err := host.SetSettings(cfg); err != nil {
		t.Fatal(err)
	}
	time.Sleep(3 * time.Second)
	if fps, _ := measure("native 120"); fps < 90 {
		t.Errorf("native 120: got %.1f fps", fps)
	}
	if pc.ConnectionState() != webrtc.PeerConnectionStateConnected {
		t.Errorf("viewer dropped after a live change: %s", pc.ConnectionState())
	}
}
