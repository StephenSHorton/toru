package main

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // TURN REST is defined on HMAC-SHA1
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pion/turn/v5"
)

const testSecret = "0123456789abcdef-test-secret"

// httptest.NewServer listens on 127.0.0.1 only.
func newCredServer(t *testing.T, perMin, burst int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(&credHandler{
		secret: testSecret, ttl: 10 * time.Minute,
		limit: newIPLimiter(perMin, burst, time.Now),
		uris:  iceURIs("relay.example.com", 3478, 3478, 443),
	})
	t.Cleanup(srv.Close)
	return srv
}

func TestCredentialEndpoint(t *testing.T) {
	srv := newCredServer(t, 0, 0)
	resp, err := http.Get(srv.URL + "/turn-credentials?peer=0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control %q", got)
	}
	var cr credResponse
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		t.Fatal(err)
	}

	// username is "expiry:peerId", expiry about ttl from now.
	exp, peer, ok := strings.Cut(cr.Username, ":")
	if !ok || peer != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("username %q", cr.Username)
	}
	ts, err := strconv.ParseInt(exp, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(time.Unix(ts, 0)); d < 9*time.Minute || d > 11*time.Minute {
		t.Errorf("expiry %v from now, want about 10m", d)
	}
	if cr.TTL != 600 {
		t.Errorf("ttl %d", cr.TTL)
	}

	// credential is base64(HMAC-SHA1(secret, username)), computed independently.
	mac := hmac.New(sha1.New, []byte(testSecret))
	mac.Write([]byte(cr.Username))
	if want := base64.StdEncoding.EncodeToString(mac.Sum(nil)); cr.Credential != want {
		t.Errorf("credential %q, want %q", cr.Credential, want)
	}

	want := []string{
		"turn:relay.example.com:3478?transport=udp",
		"turn:relay.example.com:3478?transport=tcp",
		"turns:relay.example.com:443?transport=tcp",
	}
	if strings.Join(cr.URIs, " ") != strings.Join(want, " ") {
		t.Errorf("uris %v", cr.URIs)
	}
}

func TestCredentialEndpointRejects(t *testing.T) {
	srv := newCredServer(t, 0, 0)
	for _, q := range []string{"", "?peer=", "?peer=a:b", "?peer=" + strings.Repeat("a", 65), "?peer=%20x"} {
		resp, err := http.Get(srv.URL + "/turn-credentials" + q)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%q: status %d, want 400", q, resp.StatusCode)
		}
	}
	resp, err := http.Post(srv.URL+"/turn-credentials?peer=abc", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", resp.StatusCode)
	}
}

func TestCredentialEndpointRateLimit(t *testing.T) {
	srv := newCredServer(t, 1, 3) // 1 a minute, burst 3
	codes := []int{}
	for range 5 {
		resp, err := http.Get(srv.URL + "/turn-credentials?peer=abc")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	want := []int{200, 200, 200, 429, 429}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("codes %v, want %v", codes, want)
		}
	}
}

func TestIPLimiterRefills(t *testing.T) {
	now := time.Unix(1000, 0)
	l := newIPLimiter(60, 2, func() time.Time { return now }) // 1/s, burst 2
	got := []bool{l.allow("a"), l.allow("a"), l.allow("a")}
	if !got[0] || !got[1] || got[2] {
		t.Fatalf("burst of 2 not enforced: %v", got)
	}
	if !l.allow("b") {
		t.Fatal("limit leaked across IPs")
	}
	now = now.Add(time.Second)
	if !l.allow("a") || l.allow("a") {
		t.Fatal("did not refill one token per second")
	}
	now = now.Add(time.Minute)
	l.sweep()
	if len(l.m) != 0 {
		t.Fatalf("sweep kept %d idle entries", len(l.m))
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "6.6.6.6, 9.9.9.9")
	if got := clientIP(r, false); got != "127.0.0.1" {
		t.Errorf("untrusted: %q", got)
	}
	if got := clientIP(r, true); got != "9.9.9.9" {
		t.Errorf("trusted: %q (want the right-most hop)", got)
	}
}

// TestAuthHandler checks the relay's auth (pion's TURN REST handler) against
// credentials from the endpoint: the key it derives must be the long-term key
// for exactly that username and password.
func TestAuthHandler(t *testing.T) {
	const realm = "toru"
	user, pass, err := turn.GenerateLongTermTURNRESTCredentials(testSecret, "peer1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h := turn.LongTermTURNRESTAuthHandler(testSecret, nil)

	id, key, ok := h(&turn.RequestAttributes{Username: user, Realm: realm})
	if !ok || id != "peer1" {
		t.Fatalf("valid credential: ok=%v id=%q", ok, id)
	}
	if string(key) != string(turn.GenerateAuthKey(user, realm, pass)) {
		t.Error("key does not match the issued password")
	}

	// Another secret derives another key, so the server's integrity check fails.
	_, other, _ := turn.LongTermTURNRESTAuthHandler("a-different-secret-entirely", nil)(
		&turn.RequestAttributes{Username: user, Realm: realm})
	if string(other) == string(key) {
		t.Error("a different secret produced the same key")
	}

	expired := strconv.FormatInt(time.Now().Add(-time.Second).Unix(), 10) + ":peer1"
	if _, _, ok := h(&turn.RequestAttributes{Username: expired, Realm: realm}); ok {
		t.Error("expired credential accepted")
	}
	if _, _, ok := h(&turn.RequestAttributes{Username: "notatime:peer1", Realm: realm}); ok {
		t.Error("malformed username accepted")
	}
}
