package transfer

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// recorder captures emitted events so the test can assert the phase sequence
// instead of polling and hoping to catch intermediate states.
type recorder struct {
	mu     sync.Mutex
	sends  []SendState
	recvs  []ReceiveState
	events int
}

func (r *recorder) emit(name string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events++
	switch v := data.(type) {
	case SendState:
		if name == EventSend {
			r.sends = append(r.sends, v)
		}
	case ReceiveState:
		if name == EventReceive {
			r.recvs = append(r.recvs, v)
		}
	}
}

func (r *recorder) sendPhases() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.sends {
		if len(out) == 0 || out[len(out)-1] != s.Phase {
			out = append(out, s.Phase)
		}
	}
	return out
}

func (r *recorder) recvPhases() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, s := range r.recvs {
		if len(out) == 0 || out[len(out)-1] != s.Phase {
			out = append(out, s.Phase)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func waitForCode(t *testing.T, m *Manager) string {
	t.Helper()
	waitFor(t, "wormhole code", 30*time.Second, func() bool {
		st := m.SendStatus()
		return st.Code != "" || st.Phase == SendError
	})
	st := m.SendStatus()
	if st.Phase == SendError {
		t.Fatalf("send failed before a code was issued: %s", st.Error)
	}
	return st.Code
}

func waitForRecvDone(t *testing.T, m *Manager) ReceiveState {
	t.Helper()
	waitFor(t, "receive to finish", 90*time.Second, func() bool {
		return !m.ReceiveStatus().Active
	})
	st := m.ReceiveStatus()
	if st.Phase != RecvDone {
		t.Fatalf("receive ended in %q: %s", st.Phase, st.Error)
	}
	return st
}

func waitForSendDone(t *testing.T, m *Manager) SendState {
	t.Helper()
	waitFor(t, "send to finish", 30*time.Second, func() bool {
		return !m.SendStatus().Active
	})
	st := m.SendStatus()
	if st.Phase != SendDone {
		t.Fatalf("send ended in %q: %s", st.Phase, st.Error)
	}
	return st
}

func wormholeTempZips(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "wormhole-william-dir*"))
	for _, m := range matches {
		out[m] = true
	}
	return out
}

// TestLoopbackPublicRendezvous drives two Managers in one process through the
// REAL public rendezvous + transit relay: a 1 MiB file, a small nested folder,
// and StopSend while waiting for a receiver. It needs the network, so it only
// runs with TORU_TRANSFER_NETWORK_TEST=1:
//
//	TORU_TRANSFER_NETWORK_TEST=1 go test ./internal/transfer/ -run Loopback -v -count=1
func TestLoopbackPublicRendezvous(t *testing.T) {
	if os.Getenv("TORU_TRANSFER_NETWORK_TEST") != "1" {
		t.Skip("set TORU_TRANSFER_NETWORK_TEST=1 to run the public-rendezvous loopback")
	}

	sendRec, recvRec := &recorder{}, &recorder{}
	sender, receiver := NewManager(), NewManager()
	sender.SetEmitter(sendRec.emit)
	receiver.SetEmitter(recvRec.emit)
	// Installed ONCE, before any send: the hook fires from the goroutine's defer,
	// which runs after the final state write that waitForSendDone observes, so
	// assigning it later would race with (and could be fired by) an earlier send.
	exits := make(chan struct{}, 8)
	sender.onSendExit = func() { exits <- struct{}{} }

	// ---- 1 MiB file -------------------------------------------------------
	srcDir := t.TempDir()
	blob := make([]byte, 1<<20)
	if _, err := rand.Read(blob); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(srcDir, "blob.bin")
	if err := os.WriteFile(src, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()

	t0 := time.Now()
	if err := sender.StartSend(src); err != nil {
		t.Fatal(err)
	}
	code := waitForCode(t, sender)
	tCode := time.Since(t0)
	if st := sender.SendStatus(); st.Phase != SendWaiting || st.Kind != KindFile || st.Name != "blob.bin" {
		t.Fatalf("unexpected send state after code: %+v", st)
	}
	if err := receiver.StartReceive(code, outDir); err != nil {
		t.Fatal(err)
	}
	rst := waitForRecvDone(t, receiver)
	sst := waitForSendDone(t, sender)
	tFile := time.Since(t0)
	t.Logf("file: code after %s, transfer done after %s (%d events sender / %d receiver)",
		tCode.Round(time.Millisecond), tFile.Round(time.Millisecond), sendRec.events, recvRec.events)

	if rst.Kind != KindFile || rst.Name != "blob.bin" || rst.FileCount != 1 || rst.Total != int64(len(blob)) {
		t.Fatalf("unexpected receive state: %+v", rst)
	}
	if filepath.Base(rst.OutPath) != "blob.bin" || filepath.Dir(rst.OutPath) != outDir {
		t.Fatalf("OutPath = %q", rst.OutPath)
	}
	got, err := os.ReadFile(rst.OutPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, blob) {
		t.Fatalf("received bytes differ (len %d vs %d)", len(got), len(blob))
	}
	if sst.Sent != sst.Total || sst.Total != int64(len(blob)) {
		t.Fatalf("send counters: sent %d total %d", sst.Sent, sst.Total)
	}
	wantSend := []string{SendPreparing, SendWaiting, SendSending, SendDone}
	if ph := sendRec.sendPhases(); !equalStrings(ph, wantSend) {
		t.Fatalf("send phases = %v, want %v", ph, wantSend)
	}
	wantRecv := []string{RecvConnecting, RecvReceiving, RecvDone}
	if ph := recvRec.recvPhases(); !equalStrings(ph, wantRecv) {
		t.Fatalf("receive phases = %v, want %v", ph, wantRecv)
	}

	// ---- folder: 3 files, one nested ---------------------------------------
	folder := filepath.Join(srcDir, "pics")
	files := map[string][]byte{
		"a.txt":            []byte("alpha"),
		"b.bin":            blob[:4096],
		"nested/deep/c.md": []byte("# gamma"),
	}
	for rel, content := range files {
		p := filepath.Join(folder, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A pre-existing "pics" in the destination must trigger the collision suffix.
	if err := os.Mkdir(filepath.Join(outDir, "pics"), 0o755); err != nil {
		t.Fatal(err)
	}
	zipsBefore := wormholeTempZips(t)

	t1 := time.Now()
	if err := sender.StartSend(folder); err != nil {
		t.Fatal(err)
	}
	code = waitForCode(t, sender)
	if st := sender.SendStatus(); st.Kind != KindFolder || st.Name != "pics" {
		t.Fatalf("unexpected folder send state: %+v", st)
	}
	if err := receiver.StartReceive(code, outDir); err != nil {
		t.Fatal(err)
	}
	rst = waitForRecvDone(t, receiver)
	waitForSendDone(t, sender)
	t.Logf("folder: done after %s", time.Since(t1).Round(time.Millisecond))

	if rst.Kind != KindFolder || rst.FileCount != 3 {
		t.Fatalf("unexpected folder receive state: %+v", rst)
	}
	if filepath.Base(rst.OutPath) != "pics (2)" {
		t.Fatalf("folder OutPath = %q, want …\\pics (2)", rst.OutPath)
	}
	for rel, content := range files {
		got, err := os.ReadFile(filepath.Join(rst.OutPath, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("%s: content differs", rel)
		}
	}
	// No spool files left in the destination.
	entries, _ := os.ReadDir(outDir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".part" {
			t.Fatalf("stray spool %s in destination", e.Name())
		}
	}
	// Library quirk check (informational): makeTmpZip defers os.Remove on a
	// still-open file, which fails on Windows. Report, do not fail.
	for p := range wormholeTempZips(t) {
		if !zipsBefore[p] {
			t.Logf("NOTE: library left temp zip behind: %s", p)
			_ = os.Remove(p)
		}
	}

	// ---- StopSend while waiting --------------------------------------------
	// Drain exits from the two completed sends (their defers may land slightly
	// after waitForSendDone returned) so the wait below sees only THIS send.
	waitFor(t, "earlier send goroutines to exit", 5*time.Second, func() bool { return len(exits) >= 2 })
	for len(exits) > 0 {
		<-exits
	}
	if err := sender.StartSend(src); err != nil {
		t.Fatal(err)
	}
	_ = waitForCode(t, sender)
	t2 := time.Now()
	if err := sender.StopSend(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "phase stopped", 5*time.Second, func() bool {
		st := sender.SendStatus()
		return st.Phase == SendStopped && !st.Active
	})
	tStopped := time.Since(t2)
	// This observes Toru's runSend wrapper (it selects on ctx itself), not the
	// library goroutine behind it; whether THAT unblocked is not visible here.
	select {
	case <-exits:
		t.Logf("stop: phase stopped after %s, send wrapper goroutine exited after %s",
			tStopped.Round(time.Millisecond), time.Since(t2).Round(time.Millisecond))
	case <-time.After(5 * time.Second):
		t.Fatalf("runSend did not return within 5s of StopSend")
	}
	// The late library result must not have overwritten "stopped".
	time.Sleep(200 * time.Millisecond)
	if st := sender.SendStatus(); st.Phase != SendStopped {
		t.Fatalf("stale result overwrote stopped state: %+v", st)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
