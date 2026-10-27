package stream

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/pion/interceptor"
	"github.com/pion/interceptor/pkg/nack"
	"github.com/pion/webrtc/v4"
)

// broadcast.go is going live: the encoder ffmpegs (supervised and restarted
// when capture is lost), the shared tracks they feed, and one WebRTC peer
// connection per viewer.

// iceServers are public STUN servers (no account) for finding each PC's
// public address. Video never passes through them.
var iceServers = []webrtc.ICEServer{
	{URLs: []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"}},
	{URLs: []string{"stun:stun.cloudflare.com:3478"}},
}

// Payload types and codec lines offered to viewers. Every viewer is a Toru
// WebView2, which decodes all of them.
var (
	codecH264 = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeH264, ClockRate: 90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=64001f",
			RTCPFeedback: videoFeedback,
		},
		PayloadType: 102,
	}
	codecAV1 = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeAV1, ClockRate: 90000,
			RTCPFeedback: videoFeedback,
		},
		PayloadType: 45,
	}
	codecOpus = webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2,
			SDPFmtpLine: "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1",
		},
		PayloadType: 111,
	}
	videoFeedback = []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "ccm", Parameter: "fir"}}
)

// newAPI builds the pion API the streamer's peer connections use: our codecs,
// RTCP reports (A/V sync, round-trip time) and a deep NACK history so
// retransmits still work at very high bitrates.
func newAPI() (*webrtc.API, error) {
	m := &webrtc.MediaEngine{}
	for _, c := range []webrtc.RTPCodecParameters{codecH264, codecAV1} {
		if err := m.RegisterCodec(c, webrtc.RTPCodecTypeVideo); err != nil {
			return nil, err
		}
	}
	if err := m.RegisterCodec(codecOpus, webrtc.RTPCodecTypeAudio); err != nil {
		return nil, err
	}
	reg := &interceptor.Registry{}
	if err := webrtc.ConfigureRTCPReports(reg); err != nil {
		return nil, err
	}
	responder, err := nack.NewResponderInterceptor(nack.ResponderSize(16384))
	if err != nil {
		return nil, err
	}
	reg.Add(responder)
	se := webrtc.SettingEngine{}
	se.SetICETimeouts(5*time.Second, 15*time.Second, 2*time.Second)
	return webrtc.NewAPI(webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(reg), webrtc.WithSettingEngine(se)), nil
}

// LiveInfo describes a live stream to the room.
type LiveInfo struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	FPS         int    `json:"fps"`
	BitrateKbps int    `json:"bitrateKbps"`
	Codec       string `json:"codec"`
	Audio       bool   `json:"audio"`
}

// ViewerInfo is one person watching.
type ViewerInfo struct {
	Peer      string `json:"peer"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
}

// LiveStatus is the broadcaster's state for the UI.
type LiveStatus struct {
	Live      bool         `json:"live"`
	Encoding  bool         `json:"encoding"` // an encoder is running right now
	Encoder   string       `json:"encoder"`  // e.g. "h264_nvenc"
	ZeroCopy  bool         `json:"zeroCopy"`
	Info      LiveInfo     `json:"info"`
	Viewers   []ViewerInfo `json:"viewers"`
	SentKbps  int          `json:"sentKbps"` // encoder output, before per-viewer copies
	SentFPS   int          `json:"sentFps"`
	Error     string       `json:"error"`
	StartedAt int64        `json:"startedAt"` // unix ms
}

type viewer struct {
	key, peer, session, name string
	pc                       *webrtc.PeerConnection
	connected                atomic.Bool
}

// broadcaster is one go-live session. Build with startBroadcast; stop with
// stop. Settings changes go through update.
type broadcaster struct {
	api      *webrtc.API
	ffmpeg   string
	send     func(to, typ string, body any) error
	onChange func()

	mu      sync.Mutex
	cfg     Settings
	screenW int
	screenH int
	enc     string
	video   *feed
	audio   *feed // nil without audio
	viewers map[string]*viewer
	status  LiveStatus
	stopped bool

	vStop, aStop chan struct{} // closing one ends that supervisor
	vDone, aDone chan struct{}
	vKick, aKick chan struct{} // restart the encoder now (new settings)
	vRuns        int           // video encoder runs started on the current tracks
	quit         chan struct{}
	statsDone    chan struct{}
}

func startBroadcast(cfg Settings, send func(to, typ string, body any) error, onChange func()) (*broadcaster, error) {
	bin, err := capture.LocateFFmpeg()
	if err != nil {
		return nil, err
	}
	api, err := newAPI()
	if err != nil {
		return nil, err
	}
	b := &broadcaster{
		api: api, ffmpeg: bin, send: send, onChange: onChange,
		viewers: map[string]*viewer{}, quit: make(chan struct{}), statsDone: make(chan struct{}),
	}
	if err := b.setup(cfg); err != nil {
		return nil, err
	}
	b.status.Live = true
	b.status.StartedAt = time.Now().UnixMilli()
	go b.statsLoop()
	return b, nil
}

// setup resolves the encoder and monitor for cfg, builds fresh tracks and
// starts the supervisors. Caller holds no lock; b must not be serving.
func (b *broadcaster) setup(cfg Settings) error {
	codec := capture.ReplayCodecH264
	if cfg.Codec == CodecAV1 {
		codec = capture.ReplayCodecAV1
	}
	enc, err := capture.HardwareEncoderFor(codec)
	if err != nil {
		return fmt.Errorf("can't stream %s: %w", strings.ToUpper(cfg.Codec), err)
	}
	screen, _, err := capture.ScreenForCapture(cfg.MonitorID)
	if err != nil {
		return err
	}
	vc := codecH264
	if cfg.Codec == CodecAV1 {
		vc = codecAV1
	}
	vt, err := webrtc.NewTrackLocalStaticRTP(vc.RTPCodecCapability, "video", "toru")
	if err != nil {
		return err
	}
	vf, err := newFeed(vt, vc.ClockRate)
	if err != nil {
		return err
	}
	var af *feed
	if cfg.Audio {
		at, err := webrtc.NewTrackLocalStaticRTP(codecOpus.RTPCodecCapability, "audio", "toru")
		if err != nil {
			vf.close()
			return err
		}
		if af, err = newFeed(at, codecOpus.ClockRate); err != nil {
			vf.close()
			return err
		}
	}

	b.mu.Lock()
	b.cfg, b.enc, b.screenW, b.screenH = cfg, enc, screen.W, screen.H
	b.video, b.audio = vf, af
	b.status.Encoder = enc
	b.status.Info = b.infoLocked()
	b.vRuns = 0
	b.vStop, b.vDone, b.vKick = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	go b.superviseVideo(b.vStop, b.vDone, b.vKick)
	if af != nil {
		b.aStop, b.aDone, b.aKick = make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
		go b.superviseAudio(b.aStop, b.aDone, b.aKick)
	} else {
		b.aStop, b.aDone, b.aKick = nil, nil, nil
	}
	b.mu.Unlock()
	return nil
}

// teardown stops the supervisors and closes the feeds (not the viewers).
func (b *broadcaster) teardown() {
	b.mu.Lock()
	vStop, vDone, aStop, aDone := b.vStop, b.vDone, b.aStop, b.aDone
	vf, af := b.video, b.audio
	b.mu.Unlock()
	if vStop != nil {
		close(vStop)
		<-vDone
	}
	if aStop != nil {
		close(aStop)
		<-aDone
	}
	if vf != nil {
		vf.close()
	}
	if af != nil {
		af.close()
	}
}

func (b *broadcaster) infoLocked() LiveInfo {
	w, h := b.cfg.Width, b.cfg.Height
	if w == 0 || h == 0 {
		w, h = b.screenW, b.screenH
	}
	return LiveInfo{Width: w, Height: h, FPS: b.cfg.FPS, BitrateKbps: b.cfg.BitrateKbps, Codec: b.cfg.Codec, Audio: b.cfg.Audio}
}

// Info is what the room is told about this stream.
func (b *broadcaster) Info() LiveInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.infoLocked()
}

// Status is a snapshot for the UI.
func (b *broadcaster) Status() LiveStatus {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.status
	st.Viewers = make([]ViewerInfo, 0, len(b.viewers))
	for _, v := range b.viewers {
		st.Viewers = append(st.Viewers, ViewerInfo{Peer: v.peer, Name: v.name, Connected: v.connected.Load()})
	}
	return st
}

// update applies new settings live. Size, frame rate, bitrate, cursor,
// keyframe interval and audio bitrate only restart the encoder, so viewers
// keep watching; a new codec, monitor or audio on/off needs new tracks, so
// viewers are asked to reconnect.
func (b *broadcaster) update(cfg Settings) error {
	b.mu.Lock()
	old := b.cfg
	b.mu.Unlock()
	if cfg.Codec != old.Codec || cfg.Audio != old.Audio || cfg.MonitorID != old.MonitorID {
		b.teardown()
		viewers := b.dropViewers()
		err := b.setup(cfg)
		if err != nil {
			b.mu.Lock()
			b.status.Error = err.Error()
			b.mu.Unlock()
		}
		for _, v := range viewers {
			_ = b.send(v.peer, msgRestart, restartBody{Session: v.session})
		}
		b.changed()
		return err
	}
	b.mu.Lock()
	b.cfg = cfg
	b.status.Info = b.infoLocked()
	vKick, aKick := b.vKick, b.aKick
	b.mu.Unlock()
	kick(vKick)
	if cfg.AudioKbps != old.AudioKbps {
		kick(aKick)
	}
	b.changed()
	return nil
}

func kick(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// stop ends the stream for everyone.
func (b *broadcaster) stop() {
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return
	}
	b.stopped = true
	b.mu.Unlock()
	close(b.quit)
	<-b.statsDone
	for _, v := range b.dropViewers() {
		_ = b.send(v.peer, msgEnded, restartBody{Session: v.session})
	}
	b.teardown()
}

// reconnectViewers asks every viewer to reconnect (a fresh decoder).
func (b *broadcaster) reconnectViewers() {
	for _, v := range b.dropViewers() {
		_ = b.send(v.peer, msgRestart, restartBody{Session: v.session})
	}
	b.changed()
}

func (b *broadcaster) dropViewers() []*viewer {
	b.mu.Lock()
	vs := make([]*viewer, 0, len(b.viewers))
	for _, v := range b.viewers {
		vs = append(vs, v)
	}
	b.viewers = map[string]*viewer{}
	b.mu.Unlock()
	for _, v := range vs {
		_ = v.pc.Close()
	}
	return vs
}

func (b *broadcaster) changed() {
	if b.onChange != nil {
		b.onChange()
	}
}

// --- encoders ---------------------------------------------------------------

// startupGrace is how long an encoder must survive to count as started.
const startupGrace = 1500 * time.Millisecond

// superviseVideo keeps a video encoder running until stop: on a crash
// (capture lost to a UAC prompt, a game switching display mode) it restarts
// after a short pause; on kick it restarts at once with the current settings.
func (b *broadcaster) superviseVideo(stop, done, kickCh chan struct{}) {
	defer close(done)
	failures := 0
	for {
		b.mu.Lock()
		cfg, enc, vf := b.cfg, b.enc, b.video
		native := cfg.Width == 0 || (cfg.Width == b.screenW && cfg.Height == b.screenH)
		b.mu.Unlock()

		_, ddaIdx, err := capture.ScreenForCapture(cfg.MonitorID)
		var p *proc
		zero := false
		if err == nil {
			candidates := []bool{false}
			if native && (strings.HasSuffix(enc, "_nvenc") || strings.HasSuffix(enc, "_amf")) {
				candidates = []bool{true, false}
			}
			var errs []error
			for _, zc := range candidates {
				vf.newSource()
				p, err = b.spawn(videoArgs(ddaIdx, cfg, enc, zc, vf.url()))
				if err == nil {
					zero = zc
					break
				}
				errs = append(errs, err)
			}
			if p == nil {
				err = errors.Join(errs...)
			}
		}
		if err != nil {
			failures++
			b.setEncoding(false, false, "Video encoder: "+err.Error())
			if failures >= 5 {
				logf("video encoder gave up: %v", err)
				<-stop
				return
			}
			if wait(stop, kickCh, time.Duration(failures)*time.Second) {
				return
			}
			continue
		}
		failures = 0
		b.setEncoding(true, zero, "")
		b.mu.Lock()
		b.vRuns++
		restarted := b.vRuns > 1
		b.mu.Unlock()
		if restarted {
			// Packets carry on seamlessly, but WebView2's decoder doesn't fully
			// reset on a new encoder's stream headers mid-session and keeps
			// rejecting frames. A fresh connection (about a second) always works.
			b.reconnectViewers()
		}

		select {
		case <-stop:
			p.kill()
			return
		case <-kickCh:
			p.kill()
		case <-p.done:
			logf("video encoder exited: %v: %s", p.err, lastLine(p.tail.String()))
			b.setEncoding(false, false, "Capture was interrupted, restarting…")
			if wait(stop, kickCh, time.Second) {
				return
			}
		}
	}
}

// superviseAudio is superviseVideo for desktop audio: a loopback capture of
// everything except Toru itself (so a call's audio never echoes back into a
// stream) piped into an Opus encoder.
func (b *broadcaster) superviseAudio(stop, done, kickCh chan struct{}) {
	defer close(done)
	failures := 0
	for {
		b.mu.Lock()
		cfg, af := b.cfg, b.audio
		b.mu.Unlock()

		src, err := capture.StartDesktopAudio(uint32(os.Getpid()), fmt.Sprintf("toru-stream-audio-%d", time.Now().UnixNano()))
		var p *proc
		if err == nil {
			in := src.Input()
			af.newSource()
			p, err = b.spawn(audioArgs(in.SampleFmt, in.SampleRate, in.Channels, cfg.AudioKbps, in.PipePath, af.url()))
			if err != nil {
				src.Stop()
			}
		}
		if err != nil {
			failures++
			logf("audio encoder: %v", err)
			if failures >= 5 {
				<-stop
				return
			}
			if wait(stop, kickCh, time.Duration(failures)*time.Second) {
				return
			}
			continue
		}
		failures = 0

		select {
		case <-stop:
			p.kill()
			src.Stop()
			return
		case <-kickCh:
			p.kill()
			src.Stop()
		case <-p.done:
			src.Stop()
			logf("audio encoder exited: %v: %s", p.err, lastLine(p.tail.String()))
			if wait(stop, kickCh, time.Second) {
				return
			}
		}
	}
}

// wait sleeps d, returning true if stop closed meanwhile (a kick cuts it short).
func wait(stop, kickCh chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-stop:
		return true
	case <-kickCh:
		return false
	case <-t.C:
		return false
	}
}

// proc is one running ffmpeg. done closes when it exits (err is then set).
type proc struct {
	cmd  *exec.Cmd
	tail *tailBuf
	done chan struct{}
	err  error
}

func (p *proc) kill() {
	_ = p.cmd.Process.Kill()
	<-p.done
}

// spawn starts one ffmpeg and waits out startupGrace.
func (b *broadcaster) spawn(args []string) (*proc, error) {
	cmd := exec.Command(b.ffmpeg, args...)
	capture.PrepareFFmpegCmd(cmd)
	p := &proc{cmd: cmd, tail: &tailBuf{max: 4096}, done: make(chan struct{})}
	cmd.Stderr = p.tail
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	capture.TieFFmpegToToru(cmd)
	go func() {
		p.err = cmd.Wait()
		close(p.done)
	}()
	select {
	case <-p.done:
		if msg := lastLine(p.tail.String()); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, fmt.Errorf("ffmpeg exited during startup: %v", p.err)
	case <-time.After(startupGrace):
		return p, nil
	}
}

func (b *broadcaster) setEncoding(on, zeroCopy bool, errMsg string) {
	b.mu.Lock()
	changed := b.status.Encoding != on || b.status.Error != errMsg || b.status.ZeroCopy != zeroCopy
	b.status.Encoding, b.status.ZeroCopy, b.status.Error = on, zeroCopy, errMsg
	b.mu.Unlock()
	if changed {
		b.changed()
	}
}

// statsLoop measures what the encoder actually sends, once a second.
func (b *broadcaster) statsLoop() {
	defer close(b.statsDone)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var lastBytes, lastFrames uint64
	var lastFeed *feed
	for {
		select {
		case <-b.quit:
			return
		case <-t.C:
		}
		b.mu.Lock()
		vf := b.video
		b.mu.Unlock()
		if vf != lastFeed {
			lastFeed, lastBytes, lastFrames = vf, 0, 0
		}
		by, fr := vf.bytes.Load(), vf.frames.Load()
		b.mu.Lock()
		b.status.SentKbps = int((by - lastBytes) * 8 / 1000)
		b.status.SentFPS = int(fr - lastFrames)
		b.mu.Unlock()
		lastBytes, lastFrames = by, fr
		b.changed()
	}
}

// --- viewers ----------------------------------------------------------------

// addViewer answers a watch request: a peer connection carrying the shared
// tracks, offered to the viewer once its ICE candidates are gathered.
func (b *broadcaster) addViewer(peer, session, name string) error {
	key := peer + "/" + session
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return errors.New("not live")
	}
	if old := b.viewers[key]; old != nil {
		delete(b.viewers, key)
		go func() { _ = old.pc.Close() }()
	}
	vf, af := b.video, b.audio
	b.mu.Unlock()

	pc, err := b.api.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
	if err != nil {
		return err
	}
	v := &viewer{key: key, peer: peer, session: session, name: clipName(name), pc: pc}
	fail := func(err error) error {
		_ = pc.Close()
		return err
	}
	vs, err := pc.AddTrack(vf.track)
	if err != nil {
		return fail(err)
	}
	go drainRTCP(vs)
	if af != nil {
		as, err := pc.AddTrack(af.track)
		if err != nil {
			return fail(err)
		}
		go drainRTCP(as)
	}
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		switch s {
		case webrtc.PeerConnectionStateConnected:
			v.connected.Store(true)
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			v.connected.Store(false)
			b.mu.Lock()
			if b.viewers[key] == v {
				delete(b.viewers, key)
			}
			b.mu.Unlock()
			go func() { _ = pc.Close() }()
		default:
			v.connected.Store(false)
		}
		b.changed()
	})

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return fail(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-time.After(4 * time.Second): // send what we have; host candidates come first
	}

	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return fail(errors.New("not live"))
	}
	b.viewers[key] = v
	info := b.infoLocked()
	b.mu.Unlock()
	b.changed()
	return b.send(peer, msgOffer, offerBody{Session: session, SDP: pc.LocalDescription().SDP, Info: info})
}

func (b *broadcaster) answer(peer, session, sdp string) error {
	b.mu.Lock()
	v := b.viewers[peer+"/"+session]
	b.mu.Unlock()
	if v == nil {
		return errors.New("no such viewer")
	}
	return v.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp})
}

func (b *broadcaster) removeViewer(peer, session string) {
	b.mu.Lock()
	v := b.viewers[peer+"/"+session]
	delete(b.viewers, peer+"/"+session)
	b.mu.Unlock()
	if v != nil {
		_ = v.pc.Close()
		b.changed()
	}
}

// removePeer drops every session of a peer that left the room.
func (b *broadcaster) removePeer(peer string) {
	b.mu.Lock()
	var gone []*viewer
	for k, v := range b.viewers {
		if v.peer == peer {
			gone = append(gone, v)
			delete(b.viewers, k)
		}
	}
	b.mu.Unlock()
	for _, v := range gone {
		_ = v.pc.Close()
	}
	if len(gone) > 0 {
		b.changed()
	}
}

// drainRTCP reads a sender's incoming RTCP; the interceptors (NACK
// retransmits, receiver reports) only run while someone reads.
func drainRTCP(s *webrtc.RTPSender) {
	buf := make([]byte, 1500)
	for {
		if _, _, err := s.Read(buf); err != nil {
			return
		}
	}
}

// --- helpers ----------------------------------------------------------------

// tailBuf keeps the last max bytes written (ffmpeg's stderr).
type tailBuf struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tailBuf) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuf) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
