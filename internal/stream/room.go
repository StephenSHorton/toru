package stream

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"golang.org/x/crypto/nacl/secretbox"
	"golang.org/x/crypto/scrypt"
)

// room.go is how Toru apps in the same room find each other with no account
// and no server of our own. Every app connects to free public MQTT brokers
// and subscribes to one topic derived from the room code; everything sent
// there is sealed with a key derived from the same code, so the brokers (and
// anyone else on them) see only random bytes on a random-looking topic.
// Only small signaling messages (presence, WebRTC offers and answers) go this
// way; video and audio flow directly between the PCs.

// Room code alphabet: no 0/O, 1/I/L, so it reads back unambiguously.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

const codeLen = 16 // 16 symbols of 31 ≈ 79 bits

// brokerSets are independent public brokers; each entry is one client tried
// over raw TLS first and WebSockets-over-TLS second (some networks block
// 8883). Messages go out through all of them and are de-duplicated on
// arrival, so one broker being down or slow changes nothing.
var brokerSets = [][]string{
	{"tls://broker.emqx.io:8883", "wss://broker.emqx.io:8084/mqtt"},
	{"tls://broker.hivemq.com:8883", "wss://broker.hivemq.com:8884/mqtt"},
}

// maxMessageAge drops anything older than this (a broker replaying a backlog
// after a reconnect, or a clock far off).
const maxMessageAge = 2 * time.Minute

// NewCode returns a fresh random room code, formatted XXXX-XXXX-XXXX-XXXX.
func NewCode() string {
	b := make([]byte, codeLen)
	out := make([]byte, 0, codeLen)
	for len(out) < codeLen {
		if _, err := rand.Read(b); err != nil {
			panic(err) // crypto/rand never fails on supported platforms
		}
		for _, v := range b {
			// Rejection sampling keeps every symbol equally likely.
			if v < byte(256/len(codeAlphabet)*len(codeAlphabet)) && len(out) < codeLen {
				out = append(out, codeAlphabet[int(v)%len(codeAlphabet)])
			}
		}
	}
	return formatCode(string(out))
}

func formatCode(raw string) string {
	var sb strings.Builder
	for i := 0; i < len(raw); i++ {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(raw[i])
	}
	return sb.String()
}

// NormalizeCode accepts a code typed or pasted any way (lower case, spaces,
// missing dashes) and returns its canonical form.
func NormalizeCode(in string) (string, bool) {
	var raw strings.Builder
	for _, r := range strings.ToUpper(in) {
		switch {
		case r == '-' || r == ' ' || r == '\t':
			continue
		case r < 128 && strings.IndexByte(codeAlphabet, byte(r)) >= 0:
			raw.WriteRune(r)
		default:
			return "", false
		}
	}
	if raw.Len() != codeLen {
		return "", false
	}
	return formatCode(raw.String()), true
}

// roomSecrets derives the sealing key and the topic from a canonical code.
// scrypt makes guessing codes from a captured topic or message expensive on
// top of the ~79-bit code space.
func roomSecrets(code string) (key [32]byte, topic string, err error) {
	dk, err := scrypt.Key([]byte(code), []byte("toru-stream-room-v1"), 1<<15, 8, 1, 48)
	if err != nil {
		return key, "", err
	}
	copy(key[:], dk[:32])
	return key, "toru/v1/" + hex.EncodeToString(dk[32:]), nil
}

// Envelope is one room message. To == "" is for everyone.
type Envelope struct {
	V    int             `json:"v"`
	ID   string          `json:"id"`
	From string          `json:"from"`
	To   string          `json:"to,omitempty"`
	Type string          `json:"type"`
	At   int64           `json:"at"` // unix ms
	Body json.RawMessage `json:"body,omitempty"`
}

// Room is a live connection to one room. Build with OpenRoom.
type Room struct {
	code  string
	self  string
	key   [32]byte
	topic string
	onMsg func(Envelope)

	mu      sync.Mutex
	clients []mqtt.Client
	seen    map[string]time.Time
	closed  bool
	onConn  func()
}

// OpenRoom joins the room for code as peer self. It returns at once; brokers
// connect (and reconnect) in the background, and onMsg gets every message
// from other peers addressed to everyone or to self. onConn fires whenever a
// broker (re)connects, so callers can announce themselves.
func OpenRoom(code, self string, onMsg func(Envelope), onConn func()) (*Room, error) {
	canon, ok := NormalizeCode(code)
	if !ok {
		return nil, errors.New("that isn't a room code (16 letters and numbers, like ABCD-EFGH-JKMN-PQRS)")
	}
	key, topic, err := roomSecrets(canon)
	if err != nil {
		return nil, err
	}
	r := &Room{code: canon, self: self, key: key, topic: topic, onMsg: onMsg, onConn: onConn, seen: map[string]time.Time{}}
	for _, set := range brokerSets {
		opts := mqtt.NewClientOptions().
			SetClientID("toru-" + self + "-" + randHex(4)).
			SetCleanSession(true).
			SetAutoReconnect(true).
			SetConnectRetry(true).
			SetConnectRetryInterval(3 * time.Second).
			SetMaxReconnectInterval(15 * time.Second).
			SetConnectTimeout(8 * time.Second).
			SetKeepAlive(30 * time.Second).
			SetOrderMatters(false)
		for _, b := range set {
			opts.AddBroker(b)
		}
		opts.SetOnConnectHandler(func(c mqtt.Client) {
			// Clean sessions forget subscriptions, so (re)subscribe on every connect.
			c.Subscribe(r.topic, 1, func(_ mqtt.Client, m mqtt.Message) { r.receive(m.Payload()) })
			if r.onConn != nil {
				go r.onConn()
			}
		})
		c := mqtt.NewClient(opts)
		c.Connect() // with ConnectRetry this keeps trying in the background
		r.clients = append(r.clients, c)
	}
	return r, nil
}

// Code is the room's canonical code.
func (r *Room) Code() string { return r.code }

// Connected reports how many brokers are connected right now.
func (r *Room) Connected() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.clients {
		if c.IsConnectionOpen() {
			n++
		}
	}
	return n
}

// Send seals and publishes one message to peer to ("" = everyone).
func (r *Room) Send(to, typ string, body any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	env := Envelope{V: 1, ID: randHex(8), From: r.self, To: to, Type: typ, At: time.Now().UnixMilli(), Body: raw}
	plain, err := json.Marshal(env)
	if err != nil {
		return err
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	sealed := secretbox.Seal(nonce[:], plain, &nonce, &r.key)

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errors.New("room closed")
	}
	clients := append([]mqtt.Client(nil), r.clients...)
	r.mu.Unlock()
	sent := 0
	for _, c := range clients {
		if !c.IsConnectionOpen() {
			continue
		}
		c.Publish(r.topic, 1, false, sealed)
		sent++
	}
	if sent == 0 {
		return errors.New("not connected to the room yet")
	}
	return nil
}

// receive opens, checks and de-duplicates one broker message.
func (r *Room) receive(payload []byte) {
	if len(payload) < 24+secretbox.Overhead {
		return
	}
	var nonce [24]byte
	copy(nonce[:], payload[:24])
	plain, ok := secretbox.Open(nil, payload[24:], &nonce, &r.key)
	if !ok {
		return // not ours, or tampered with
	}
	var env Envelope
	if json.Unmarshal(plain, &env) != nil || env.V != 1 || env.ID == "" {
		return
	}
	if env.From == r.self || (env.To != "" && env.To != r.self) {
		return
	}
	now := time.Now()
	if age := now.Sub(time.UnixMilli(env.At)); age > maxMessageAge || age < -maxMessageAge {
		return
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	if _, dup := r.seen[env.ID]; dup {
		r.mu.Unlock()
		return
	}
	r.seen[env.ID] = now
	if len(r.seen) > 2048 {
		for id, t := range r.seen {
			if now.Sub(t) > 2*maxMessageAge {
				delete(r.seen, id)
			}
		}
	}
	r.mu.Unlock()
	r.onMsg(env)
}

// Close leaves the room. Pending messages get a moment to flush.
func (r *Room) Close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	clients := r.clients
	r.mu.Unlock()
	var wg sync.WaitGroup
	for _, c := range clients {
		wg.Add(1)
		go func(c mqtt.Client) {
			defer wg.Done()
			c.Disconnect(250)
		}(c)
	}
	wg.Wait()
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func decodeBody[T any](env Envelope) (T, error) {
	var v T
	if err := json.Unmarshal(env.Body, &v); err != nil {
		return v, fmt.Errorf("bad %s message: %w", env.Type, err)
	}
	return v, nil
}

func logf(format string, args ...any) { log.Printf("stream: "+format, args...) }
