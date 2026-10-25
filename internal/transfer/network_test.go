package transfer

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestConnectTimeoutPublicRendezvous: a code nobody is offering must not leave
// Receive in "connecting" forever. The public rendezvous opens a mailbox for
// any nameplate and waits, so this takes the full connectTimeout; it is gated
// like the loopback and excluded from the `-run Loopback` gate by name.
//
//	TORU_TRANSFER_NETWORK_TEST=1 go test ./internal/transfer/ -run ConnectTimeout -v -count=1
func TestConnectTimeoutPublicRendezvous(t *testing.T) {
	if os.Getenv("TORU_TRANSFER_NETWORK_TEST") != "1" {
		t.Skip("set TORU_TRANSFER_NETWORK_TEST=1 to run against the public rendezvous")
	}
	m := NewManager()
	rec := &recorder{}
	m.SetEmitter(rec.emit)
	t0 := time.Now()
	// A nameplate far above anything the relay hands out, so it is unclaimed.
	if err := m.StartReceive("999999-foo-bar", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "receive to give up", connectTimeout+20*time.Second, func() bool {
		return !m.ReceiveStatus().Active
	})
	st := m.ReceiveStatus()
	t.Logf("gave up after %s: phase=%s error=%q", time.Since(t0).Round(time.Second), st.Phase, st.Error)
	if st.Phase != RecvError || st.Error != errNobodyOffering.Error() {
		t.Fatalf("receive ended in %q with %q, want error %q", st.Phase, st.Error, errNobodyOffering)
	}
	if el := time.Since(t0); el < connectTimeout-time.Second {
		t.Fatalf("gave up after %s, before connectTimeout %s", el, connectTimeout)
	}
}

// TestStopSendDuringFolderPrepare: StopSend while a folder is still being
// zipped must abort the zip (the Reader checks ctx) and must not leave the
// library's scratch zip in %TEMP%. The library returns with that handle open,
// so the sweep relies on the finalizer-nudging retry; allow it a few seconds.
// Gated with the network tests because, if the zip happens to finish before
// the cancel lands, the library dials the rendezvous (and fails fast on the
// cancelled ctx).
func TestStopSendDuringFolderPrepare(t *testing.T) {
	if os.Getenv("TORU_TRANSFER_NETWORK_TEST") != "1" {
		t.Skip("set TORU_TRANSFER_NETWORK_TEST=1 to run")
	}
	src := filepath.Join(t.TempDir(), "bulk")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// 24 x 1 MiB random files: incompressible, so deflate has real work to do.
	blob := make([]byte, 1<<20)
	for i := 0; i < 24; i++ {
		if _, err := rand.Read(blob); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "f"+strings.Repeat("x", i)+".bin"), blob, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := tempZipSnapshot()
	m := NewManager()
	exited := make(chan struct{}, 1)
	m.onSendExit = func() { exited <- struct{}{} }
	if err := m.StartSend(src); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond) // let the walk start and the zip begin
	t0 := time.Now()
	if err := m.StopSend(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
		t.Logf("runSend returned %s after StopSend", time.Since(t0).Round(time.Millisecond))
	case <-time.After(10 * time.Second):
		t.Fatal("runSend did not return within 10s of StopSend during folder prepare")
	}
	if st := m.SendStatus(); st.Phase != SendStopped || st.Active {
		t.Fatalf("state after stop: %+v", st)
	}
	waitFor(t, "scratch zip to be swept", 20*time.Second, func() bool {
		for p := range tempZipSnapshot() {
			if !before[p] {
				return false
			}
		}
		return true
	})
	t.Logf("scratch zip gone %s after StopSend", time.Since(t0).Round(time.Millisecond))
}
