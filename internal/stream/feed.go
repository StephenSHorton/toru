package stream

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// feed is where an encoder ffmpeg's RTP lands: a loopback UDP socket whose
// packets are renumbered onto one continuous timeline (see rewriter) and
// written to a WebRTC track shared by every viewer. The encoder behind it
// can restart (new settings, a lost capture) without viewers renegotiating.
type feed struct {
	track *webrtc.TrackLocalStaticRTP
	conn  *net.UDPConn
	sink  *net.UDPConn // swallows ffmpeg's RTCP sender reports

	mu sync.Mutex
	rw rewriter

	bytes  atomic.Uint64
	frames atomic.Uint64 // marker bits = ends of video frames
	done   chan struct{}
}

func newFeed(track *webrtc.TrackLocalStaticRTP, clockRate uint32) (*feed, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	sink, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	// A 4K keyframe arrives as one burst of hundreds of packets; a deep buffer
	// keeps loopback from dropping the tail of it.
	_ = conn.SetReadBuffer(32 << 20)
	f := &feed{track: track, conn: conn, sink: sink, rw: rewriter{clock: clockRate}, done: make(chan struct{})}
	go f.readLoop()
	go f.drainSink()
	return f, nil
}

// url is the ffmpeg RTP output URL for this feed.
func (f *feed) url() string {
	return fmt.Sprintf("rtp://127.0.0.1:%d?rtcpport=%d&pkt_size=%d",
		f.conn.LocalAddr().(*net.UDPAddr).Port, f.sink.LocalAddr().(*net.UDPAddr).Port, rtpPacketSize)
}

// newSource tells the feed a new encoder run is starting, so its first
// packet is spliced onto the end of the previous run.
func (f *feed) newSource() {
	f.mu.Lock()
	f.rw.reset()
	f.mu.Unlock()
}

func (f *feed) readLoop() {
	defer close(f.done)
	buf := make([]byte, 1600)
	for {
		n, err := f.conn.Read(buf)
		if err != nil {
			return // closed
		}
		var pkt rtp.Packet
		if pkt.Unmarshal(buf[:n]) != nil {
			continue
		}
		f.mu.Lock()
		f.rw.rewrite(&pkt.Header, time.Now())
		f.mu.Unlock()
		f.bytes.Add(uint64(n))
		if pkt.Marker {
			f.frames.Add(1)
		}
		_ = f.track.WriteRTP(&pkt) // per-viewer failures surface as connection state changes
	}
}

func (f *feed) drainSink() {
	buf := make([]byte, 1600)
	for {
		if _, err := f.sink.Read(buf); err != nil {
			return
		}
	}
}

func (f *feed) close() {
	_ = f.conn.Close()
	_ = f.sink.Close()
	<-f.done
}

// rewriter maps each encoder run's own random sequence numbers and
// timestamps onto one continuous stream: after a restart the next packet
// follows the last one in sequence, and its timestamp advances by the wall
// time that passed. Gaps inside a run are kept, so receivers still see (and
// NACK) real loss.
type rewriter struct {
	clock     uint32
	started   bool
	fresh     bool // next packet starts a new run
	seqOffset uint16
	tsOffset  uint32
	lastSeq   uint16
	lastTS    uint32
	lastWall  time.Time
}

func (r *rewriter) reset() { r.fresh = true }

func (r *rewriter) rewrite(h *rtp.Header, now time.Time) {
	switch {
	case !r.started:
		r.started = true
		r.fresh = false
	case r.fresh:
		r.fresh = false
		r.seqOffset = r.lastSeq + 1 - h.SequenceNumber
		elapsed := now.Sub(r.lastWall)
		if elapsed < 0 {
			elapsed = 0
		}
		r.tsOffset = r.lastTS + uint32(elapsed.Seconds()*float64(r.clock)) + 1 - h.Timestamp
	}
	h.SequenceNumber += r.seqOffset
	h.Timestamp += r.tsOffset
	r.lastSeq = h.SequenceNumber
	r.lastTS = h.Timestamp
	r.lastWall = now
}
