package main

import (
	"encoding/json"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pion/turn/v5"
)

// creds.go is the credential endpoint. Toru (the GUI, not the LAN service)
// calls GET /turn-credentials?peer=<id> and gets a TURN REST credential that
// expires after ttl. The secret never leaves this box; static credentials in
// the client would be scraped and turn the relay into an open proxy.

// credResponse is the JSON body. uris go straight into an ICE server entry.
type credResponse struct {
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	TTL        int      `json:"ttl"` // seconds
	URIs       []string `json:"uris"`
}

// peerIDRe is what a peer id may look like: Toru's InstallID is 32 hex
// characters; anything with a ':' would break the "expiry:peerId" username.
var peerIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

type credHandler struct {
	secret       string
	ttl          time.Duration
	uris         []string
	limit        *ipLimiter
	trustForward bool // take the client IP from X-Forwarded-For (behind a reverse proxy)
}

func (h *credHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.limit.allow(clientIP(r, h.trustForward)) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	peer := r.URL.Query().Get("peer")
	if !peerIDRe.MatchString(peer) {
		http.Error(w, "peer must be 1-64 characters of A-Z a-z 0-9 _ -", http.StatusBadRequest)
		return
	}
	user, pass, err := turn.GenerateLongTermTURNRESTCredentials(h.secret, peer, h.ttl)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(credResponse{
		Username: user, Credential: pass, TTL: int(h.ttl / time.Second), URIs: h.uris,
	})
}

// clientIP is the address the rate limit keys on. Behind a reverse proxy the
// socket peer is the proxy, so with trustForward the right-most
// X-Forwarded-For entry (the one our proxy appended) is used instead.
func clientIP(r *http.Request, trustForward bool) string {
	if trustForward {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// iceURIs are the ICE server URLs handed out with each credential. host is
// the public DNS name (turns: needs it to match the certificate) or the
// public IP.
func iceURIs(host string, udpPort, tcpPort, tlsPort int) []string {
	var out []string
	if udpPort > 0 {
		out = append(out, "turn:"+net.JoinHostPort(host, strconv.Itoa(udpPort))+"?transport=udp")
	}
	if tcpPort > 0 {
		out = append(out, "turn:"+net.JoinHostPort(host, strconv.Itoa(tcpPort))+"?transport=tcp")
	}
	if tlsPort > 0 {
		out = append(out, "turns:"+net.JoinHostPort(host, strconv.Itoa(tlsPort))+"?transport=tcp")
	}
	return out
}

// ipLimiter is a token bucket per client IP: perMin requests a minute with a
// burst of burst. Idle entries are swept; maxEntries bounds memory if
// someone sprays source addresses (new IPs are refused until a sweep).
type ipLimiter struct {
	mu         sync.Mutex
	perMin     float64
	burst      float64
	maxEntries int
	m          map[string]*bucket
	now        func() time.Time
}

func newIPLimiter(perMin, burst int, now func() time.Time) *ipLimiter {
	return &ipLimiter{perMin: float64(perMin), burst: float64(burst), maxEntries: 100000,
		m: map[string]*bucket{}, now: now}
}

func (l *ipLimiter) allow(ip string) bool {
	if l.perMin <= 0 {
		return true
	}
	l.mu.Lock()
	b := l.m[ip]
	if b == nil {
		if len(l.m) >= l.maxEntries {
			l.mu.Unlock()
			return false
		}
		b = newBucket(l.perMin/60, l.burst, l.now)
		l.m[ip] = b
	}
	l.mu.Unlock()
	return b.take(1)
}

func (l *ipLimiter) sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, b := range l.m {
		if b.full() {
			delete(l.m, k)
		}
	}
}
