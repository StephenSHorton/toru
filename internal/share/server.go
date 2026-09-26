package share

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/skip2/go-qrcode"
)

//go:embed viewer.html
var viewerHTML []byte

// hls.min.js is hls.js 1.6.13 (Apache-2.0). The license is
// third_party/hls.js.LICENSE. Browsers that cannot play HLS natively
// (Chrome, Edge, Firefox, most TV browsers) use it; Safari plays the
// playlist directly.
//
//go:embed third_party/hls.min.js
var hlsJS []byte

func init() {
	_ = mime.AddExtensionType(".m3u8", "application/vnd.apple.mpegurl")
	_ = mime.AddExtensionType(".ts", "video/mp2t")
}

// Info is what the share card shows: the address to open on another device,
// a QR code of that address, and which transport is running.
type Info struct {
	URL   string   `json:"url"`
	URLs  []string `json:"urls"`
	Port  int      `json:"port"`
	Kind  string   `json:"kind"`
	QRPng string   `json:"qrPng"`
	Hint  string   `json:"hint"`
}

// Manager serves one live share. Start hides nothing; the overlay hides
// itself, then calls Start, then opens the card.
type Manager struct {
	mu       sync.Mutex
	starting bool
	ln       net.Listener
	srv      *http.Server
	sess     *capture.ShareSession
	info     Info
}

// New returns an idle manager.
func New() *Manager { return &Manager{} }

// On reports whether a share is in flight (including the window between
// Start beginning and the session being published).
func (m *Manager) On() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sess != nil || m.starting
}

// Current returns the live share's card payload.
func (m *Manager) Current() (Info, bool) {
	if m == nil {
		return Info{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sess == nil {
		return Info{}, false
	}
	return m.info, true
}

// Start grabs req and serves it on the LAN. One share at a time.
func (m *Manager) Start(req capture.CaptureRequest, audio capture.AudioConfig) (Info, error) {
	m.mu.Lock()
	if m.sess != nil || m.starting {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("already sharing")
	}
	m.starting = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.starting = false
		m.mu.Unlock()
	}()

	dir := filepath.Join(os.TempDir(), "toru", fmt.Sprintf("share-%d", time.Now().UnixNano()))
	sess, err := capture.StartShare(req, audio, dir)
	if err != nil {
		return Info{}, err
	}

	ln, port, err := listenShare()
	if err != nil {
		_ = sess.Stop()
		return Info{}, err
	}
	info := cardInfo(port, sess.Kind())
	state := &live{kind: sess.Kind(), dir: sess.Dir(), frames: sess.Frame}
	srv := &http.Server{
		Handler:           state.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	m.mu.Lock()
	m.ln = ln
	m.srv = srv
	m.sess = sess
	m.info = info
	m.mu.Unlock()

	go func() { _ = srv.Serve(ln) }()
	return info, nil
}

// Stop ends the encode and the HTTP server. Idempotent.
func (m *Manager) Stop() error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	sess := m.sess
	srv := m.srv
	m.sess = nil
	m.srv = nil
	m.ln = nil
	m.info = Info{}
	m.mu.Unlock()

	if sess != nil {
		_ = sess.Stop()
	}
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	return nil
}

func listenShare() (net.Listener, int, error) {
	for port := 47321; port < 47341; port++ {
		ln, err := net.Listen("tcp", "0.0.0.0:"+strconv.Itoa(port))
		if err == nil {
			return ln, port, nil
		}
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, 0, fmt.Errorf("share listen: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return ln, port, nil
}

func cardInfo(port int, kind string) Info {
	ips := lanIPv4s()
	info := Info{Port: port, Kind: kind}
	if len(ips) == 0 {
		info.URL = fmt.Sprintf("http://127.0.0.1:%d/", port)
		info.URLs = []string{info.URL}
		info.Hint = "This computer has no network address. Connect it to Wi-Fi, then share again. You can still open the link on this PC."
	} else {
		info.URLs = make([]string, len(ips))
		for i, ip := range ips {
			info.URLs[i] = fmt.Sprintf("http://%s:%d/", ip, port)
		}
		info.URL = info.URLs[0]
		info.Hint = "Open this link in a browser on a phone, tablet, or TV on the same Wi-Fi. If it does not connect, allow Toru on private networks when Windows Firewall asks."
	}
	if kind == capture.ShareMJPEG {
		info.Hint += " This PC has no hardware H.264 encoder, so the stream is a picture feed: a little heavier on the network, and video only."
	} else {
		info.Hint += " Expect about a second or two of delay."
	}
	if png, err := qrcode.Encode(info.URL, qrcode.Medium, 256); err == nil {
		info.QRPng = base64.StdEncoding.EncodeToString(png)
	}
	return info
}

// live is the immutable view of one running share that the HTTP handler
// closes over. Stopping the manager shuts the server down; it does not
// mutate this value under in-flight requests.
type live struct {
	kind   string
	dir    string
	frames func() (uint64, []byte)
}

func (l *live) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(viewerHTML)
	})
	mux.HandleFunc("GET /hls.min.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(hlsJS)
	})
	mux.HandleFunc("GET /api/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"kind":     l.kind,
			"playlist": "/live/index.m3u8",
		})
	})
	mux.HandleFunc("GET /live.mjpg", l.serveMJPEG)
	if l.dir != "" {
		fileSrv := http.FileServer(http.Dir(l.dir))
		mux.Handle("GET /live/", http.StripPrefix("/live/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			fileSrv.ServeHTTP(w, r)
		})))
	}
	return mux
}

func (l *live) serveMJPEG(w http.ResponseWriter, r *http.Request) {
	if l.frames == nil {
		http.Error(w, "no picture stream", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-store")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	var last uint64
	tick := time.NewTicker(40 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			gen, jpeg := l.frames()
			if gen == 0 || gen == last || len(jpeg) == 0 {
				continue
			}
			last = gen
			_, err := io.WriteString(w, fmt.Sprintf("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(jpeg)))
			if err != nil {
				return
			}
			if _, err := w.Write(jpeg); err != nil {
				return
			}
			if _, err := io.WriteString(w, "\r\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
