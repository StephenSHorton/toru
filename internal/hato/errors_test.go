package hato

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFriendlyError(t *testing.T) {
	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connectex: refused")}
	read := &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
	dns := &net.DNSError{Err: "no such host", Name: "relay.magic-wormhole.io"}
	cases := []struct {
		in   error
		want error
	}{
		{nil, nil},
		{dial, errRelayUnreachable},
		{fmt.Errorf("dial ws://relay:4000/v1: %w", dial), errRelayUnreachable},
		{fmt.Errorf("websocket: %w", dns), errRelayUnreachable},
		// The websocket library's wording without a wrapped cause.
		{errors.New(`dial ws://relay.magic-wormhole.io:4000/v1: failed to WebSocket dial: failed to send handshake request`), errRelayUnreachable},
		// A mid-transfer reset is not a "can't reach the relay".
		{read, read},
		{errors.New("decrypt message failed"), nil},
	}
	for _, c := range cases {
		got := friendlyError(c.in)
		switch {
		case c.want == nil && c.in == nil:
			if got != nil {
				t.Errorf("friendlyError(nil) = %v", got)
			}
		case c.want == nil:
			if got != c.in {
				t.Errorf("friendlyError(%v) = %v, want passthrough", c.in, got)
			}
		default:
			if got != c.want {
				t.Errorf("friendlyError(%v) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

// TestFailIdleSendDoesNotClobberActive: a rejected drop publishes an error
// state only while no send is running.
func TestFailIdleSendDoesNotClobberActive(t *testing.T) {
	m := NewManager()
	rec := &recorder{}
	m.SetEmitter(rec.emit)

	m.FailIdleSend(`C:\nowhere\gone.png`, errors.New("hato: open C:\\nowhere\\gone.png: file does not exist"))
	st := m.SendStatus()
	if st.Active || st.Phase != SendError || st.Name != "gone.png" || st.Error == "" {
		t.Fatalf("after FailIdleSend: %+v", st)
	}
	if len(rec.sends) != 1 {
		t.Fatalf("emitted %d send events, want 1", len(rec.sends))
	}

	m.mu.Lock()
	m.send = SendState{Active: true, Phase: SendWaiting, Name: "live.bin", Code: "7-a-b"}
	m.mu.Unlock()
	m.FailIdleSend(`C:\nowhere\other.png`, errors.New("hato: nope"))
	if st := m.SendStatus(); !st.Active || st.Phase != SendWaiting || st.Code != "7-a-b" {
		t.Fatalf("live send clobbered: %+v", st)
	}
	if len(rec.sends) != 1 {
		t.Fatalf("emitted %d send events, want still 1", len(rec.sends))
	}
}

// TestUnreachableRelayIsFriendly: with the rendezvous pointed at a closed local
// port, both sides fail with the one-sentence relay error, not a dial chain.
func TestUnreachableRelayIsFriendly(t *testing.T) {
	t.Setenv("TORU_WORMHOLE_RENDEZVOUS", "ws://127.0.0.1:9/v1")
	dir := t.TempDir()
	src := filepath.Join(dir, "x.bin")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	if err := m.StartSend(src); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "send to fail", 15*time.Second, func() bool { return !m.SendStatus().Active })
	if st := m.SendStatus(); st.Phase != SendError || st.Error != errRelayUnreachable.Error() {
		t.Fatalf("send ended in %q with %q", st.Phase, st.Error)
	}
	if err := m.StartReceive("7-guitarist-revenge", dir); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "receive to fail", 15*time.Second, func() bool { return !m.ReceiveStatus().Active })
	if st := m.ReceiveStatus(); st.Phase != RecvError || st.Error != errRelayUnreachable.Error() {
		t.Fatalf("receive ended in %q with %q", st.Phase, st.Error)
	}
}
