//go:build windows

package stream

import (
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/pion/webrtc/v4"
)

// TestCallBot joins a running Toru's room call as "Bot": it answers the call
// window's offer, plays a tone (so Toru shows it speaking) and counts the
// audio Toru sends. For checking the call by hand, against a Toru on this
// PC: the bot's media stays on 127.0.0.1 (see loopbackOnly).
//
//	TORU_CALL_BOT=<room code> go test ./internal/stream -run CallBot -v
func TestCallBot(t *testing.T) {
	code := os.Getenv("TORU_CALL_BOT")
	if code == "" {
		t.Skip("set TORU_CALL_BOT=<room code> to run")
	}
	secs, _ := strconv.Atoi(os.Getenv("TORU_CALL_BOT_SECS"))
	if secs <= 0 {
		secs = 30
	}
	const self = "ffffffffffffffff" // sorts last, so Toru makes the offer
	loopbackOnly(t)

	api, err := newAPI()
	if err != nil {
		t.Fatal(err)
	}
	track, err := webrtc.NewTrackLocalStaticRTP(codecOpus.RTPCodecCapability, "audio", "bot")
	if err != nil {
		t.Fatal(err)
	}
	f, err := newFeed(track, 48000)
	if err != nil {
		t.Fatal(err)
	}
	defer f.close()
	bin, err := capture.LocateFFmpeg()
	if err != nil {
		t.Fatal(err)
	}
	// A beeping tone: on half a second, off half a second.
	tone := exec.Command(bin, "-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi",
		"-i", "sine=frequency=330:sample_rate=48000", "-af", "volume='if(lt(mod(t,1),0.5),0.5,0)':eval=frame",
		"-ac", "2", "-c:a", "libopus", "-b:a", "64k", "-f", "rtp", "-pkt_size", "1200", f.url())
	if err := tone.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tone.Process.Kill() }()

	var got atomic.Int64
	var pc *webrtc.PeerConnection
	var room *Room
	room, err = OpenRoom(code, self, func(env Envelope) {
		if env.Type != msgCall {
			return
		}
		b, err := decodeBody[callBody](env)
		if err != nil || b.Kind != "offer" {
			return
		}
		if pc != nil {
			_ = pc.Close()
		}
		pc, err = api.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := pc.AddTrack(track); err != nil {
			t.Error(err)
			return
		}
		pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			for {
				if _, _, err := tr.ReadRTP(); err != nil {
					return
				}
				got.Add(1)
			}
		})
		pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) { t.Logf("bot ↔ toru: %s", s) })
		if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: b.SDP}); err != nil {
			t.Error(err)
			return
		}
		ans, _ := pc.CreateAnswer(nil)
		gathered := webrtc.GatheringCompletePromise(pc)
		_ = pc.SetLocalDescription(ans)
		<-gathered
		_ = room.Send(env.From, msgCall, callBody{Kind: "answer", SDP: pc.LocalDescription().SDP})
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer room.Close()

	end := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(end) {
		_ = room.Send("", msgPresence, presenceBody{Name: "Bot", InCall: true})
		time.Sleep(2 * time.Second)
		t.Logf("audio packets from Toru: %d", got.Load())
	}
	_ = room.Send("", msgLeave, struct{}{})
	if got.Load() == 0 {
		t.Error("no audio from Toru")
	}
}
