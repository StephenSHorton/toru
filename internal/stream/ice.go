package stream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"github.com/pion/webrtc/v4"
)

// ice.go is the one ICE server list every connection uses: the streamer's
// pion peer connections, the call window, watch windows and the LAN
// service. It is public STUN, plus our TURN relay when stream.json sets one
// up. TURN credentials are short-lived (the TURN REST scheme), fetched from
// CredentialURL here in the GUI (Call and Watch need them with LAN off) and
// refreshed before they expire.

// ICEServer is one STUN or TURN server, in the shape RTCPeerConnection takes.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}

// stunServers are public STUN servers (no account) for finding each PC's
// public address. Media never passes through them.
var stunServers = []ICEServer{
	{URLs: []string{"stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"}},
	{URLs: []string{"stun:stun.cloudflare.com:3478"}},
}

// iceServers is the STUN-only list as pion takes it (tests and test bots).
var iceServers = webrtcICE(stunServers)

func webrtcICE(list []ICEServer) []webrtc.ICEServer {
	out := make([]webrtc.ICEServer, 0, len(list))
	for _, s := range list {
		w := webrtc.ICEServer{URLs: append([]string(nil), s.URLs...)}
		if s.Username != "" || s.Credential != "" {
			w.Username, w.Credential = s.Username, s.Credential
		}
		out = append(out, w)
	}
	return out
}

func pipeICE(list []ICEServer) []api.ICEServer {
	out := make([]api.ICEServer, 0, len(list))
	for _, s := range list {
		out = append(out, api.ICEServer{URLs: append([]string(nil), s.URLs...), Username: s.Username, Credential: s.Credential})
	}
	return out
}

// Relay states, for the LAN card's "Direct only" note and diagnostics.
const (
	RelayNone    = "none"    // no relay set up: STUN only
	RelayPending = "pending" // fetching the first credentials
	RelayOK      = "ok"      // TURN is in the list
	RelayError   = "error"   // credentials couldn't be fetched (Detail says why)
)

// RelayStatus is how the TURN relay is doing.
type RelayStatus struct {
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
	Expires int64  `json:"expires,omitempty"` // unix ms the current credentials stop working
}

// turnCreds is one credential endpoint answer. "password" is the TURN REST
// draft's name for credential; either is accepted.
type turnCreds struct {
	Username   string   `json:"username"`
	Credential string   `json:"credential"`
	Password   string   `json:"password"`
	TTL        int      `json:"ttl"` // seconds
	URIs       []string `json:"uris"`
}

// Refresh timing. Credentials are renewed at 80% of their ttl, never sooner
// than minRefresh; failures retry with backoff from retryMin to retryMax.
// Variables so tests can run the loop fast.
var (
	minRefresh  = 15 * time.Second
	retryMin    = 5 * time.Second
	retryMax    = 2 * time.Minute
	defaultTTL  = 10 * time.Minute
	credTimeout = 10 * time.Second
)

// relay keeps the ICE list: STUN always, TURN while credentials are good.
type relay struct {
	turnURLs []string
	credURL  string
	peer     string
	client   *http.Client
	onChange func()
	now      func() time.Time

	mu       sync.Mutex
	user     string
	pass     string
	uris     []string
	expires  time.Time
	status   RelayStatus
	quit     chan struct{}
	started  bool
	stopped  bool
	loopDone chan struct{}
}

// newRelay reads the relay setup from cfg; peer is sent to the credential
// endpoint (the TURN REST username is usually "expiry:peer").
func newRelay(cfg Settings, peer string, onChange func()) *relay {
	r := &relay{
		turnURLs: cfg.TurnURLs,
		credURL:  cfg.CredentialURL,
		peer:     peer,
		client:   &http.Client{Timeout: credTimeout},
		onChange: onChange,
		now:      time.Now,
		quit:     make(chan struct{}),
		loopDone: make(chan struct{}),
	}
	switch {
	case r.credURL == "" && len(r.turnURLs) > 0:
		r.status = RelayStatus{State: RelayNone, Detail: "turnUrls needs a credentialUrl to get relay credentials"}
	case r.credURL == "":
		r.status = RelayStatus{State: RelayNone}
	default:
		r.status = RelayStatus{State: RelayPending}
	}
	return r
}

// start begins fetching and refreshing credentials; a no-op with no
// CredentialURL. It never blocks.
func (r *relay) start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.credURL == "" || r.started || r.stopped {
		return
	}
	r.started = true
	go r.loop()
}

func (r *relay) stop() {
	r.mu.Lock()
	started := r.started
	if !r.stopped {
		r.stopped = true
		close(r.quit)
	}
	r.mu.Unlock()
	if started {
		<-r.loopDone
	}
}

// Status returns the relay's state.
func (r *relay) Status() RelayStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status
}

// servers is the ICE list right now: STUN, then TURN while the credentials
// haven't expired.
func (r *relay) servers() []ICEServer {
	out := append([]ICEServer(nil), stunServers...)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.user == "" || !r.now().Before(r.expires) {
		return out
	}
	urls := r.turnURLs
	if len(urls) == 0 {
		urls = r.uris
	}
	var turn []string
	for _, u := range urls {
		if l := strings.ToLower(u); strings.HasPrefix(l, "turn:") || strings.HasPrefix(l, "turns:") {
			turn = append(turn, u)
		}
	}
	if len(turn) > 0 {
		out = append(out, ICEServer{URLs: turn, Username: r.user, Credential: r.pass})
	}
	return out
}

func (r *relay) loop() {
	defer close(r.loopDone)
	backoff := retryMin
	for {
		c, err := r.fetch()
		var wait time.Duration
		r.mu.Lock()
		if err == nil {
			ttl := time.Duration(c.TTL) * time.Second
			if ttl <= 0 {
				ttl = defaultTTL
			}
			r.user, r.pass, r.uris = c.Username, c.Credential, cleanTurnURLs(c.URIs)
			r.expires = r.now().Add(ttl)
			r.status = RelayStatus{State: RelayOK, Expires: r.expires.UnixMilli()}
			wait = max(ttl*8/10, minRefresh)
			backoff = retryMin
		} else {
			// Keep using credentials that still work; past expiry they are
			// dropped from the list (servers checks).
			r.status = RelayStatus{State: RelayError, Detail: err.Error()}
			if r.user != "" && r.now().Before(r.expires) {
				r.status.Expires = r.expires.UnixMilli()
			}
			wait = backoff
			backoff = min(backoff*2, retryMax)
			if r.user != "" && r.now().Before(r.expires) {
				// Retry before the working credentials run out.
				wait = min(wait, max(r.expires.Sub(r.now())/2, retryMin))
			}
		}
		r.mu.Unlock()
		if err != nil {
			logf("relay credentials: %v", err)
		}
		if r.onChange != nil {
			r.onChange()
		}
		select {
		case <-r.quit:
			return
		case <-time.After(wait):
		}
	}
}

// fetch asks the credential endpoint for fresh TURN credentials.
func (r *relay) fetch() (turnCreds, error) {
	var c turnCreds
	u, err := url.Parse(r.credURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return c, errors.New("credentialUrl isn't an http(s) URL")
	}
	if u.Scheme == "http" && !loopbackHost(u.Hostname()) {
		return c, errors.New("credentialUrl must use https")
	}
	q := u.Query()
	q.Set("peer", r.peer)
	u.RawQuery = q.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), credTimeout)
	defer cancel()
	go func() {
		select {
		case <-r.quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return c, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.client.Do(req)
	if err != nil {
		return c, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return c, fmt.Errorf("credential server: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&c); err != nil {
		return c, fmt.Errorf("credential server: %w", err)
	}
	if c.Credential == "" {
		c.Credential = c.Password
	}
	if c.Username == "" || c.Credential == "" {
		return c, errors.New("credential server: no username or credential")
	}
	return c, nil
}

func loopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}
