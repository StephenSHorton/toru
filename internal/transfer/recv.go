package transfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/psanford/wormhole-william/wormhole"
)

// connectTimeout bounds the "connecting" phase (rendezvous, PAKE, offer). A
// sender in "waiting" has already posted its PAKE and offer to the mailbox, so a
// healthy connect finishes in seconds; the library itself never gives up, and
// for a mistyped nameplate (or a sender that already hit Stop) it would sit on
// an empty mailbox forever while the page shows "Connecting…".
const connectTimeout = 60 * time.Second

// runReceive is the receive goroutine for one generation. As with runSend,
// state writes are gen-gated and cleanup (spool/.part removal, half-extracted
// folder removal) is unconditional.
func (m *Manager) runReceive(ctx context.Context, cancel context.CancelFunc, gen uint64, code, dir string) {
	defer cancel()
	defer m.wg.Done()
	defer func() {
		if m.onRecvExit != nil {
			m.onRecvExit()
		}
	}()

	fail := func(err error) {
		log.Printf("transfer: receive %s: %v", code, err)
		msg := friendlyError(err).Error()
		m.updateRecv(gen, true, func(s *ReceiveState) {
			s.Active = false
			s.Phase = RecvError
			s.Error = msg
			s.Message = "Failed: " + msg
		})
	}

	// Receive keeps the ctx it is handed on the IncomingMessage and checks it at
	// the top of EVERY Read, so a plain context.WithTimeout would also kill the
	// transfer once it expired. Instead: a cancel-only child that a timer cancels
	// if Receive has not returned by connectTimeout. The timer is stopped the
	// moment Receive returns; the child itself is released only when this
	// goroutine ends (the deferred cancelConnect), i.e. after the transfer.
	connectCtx, cancelConnect := context.WithCancel(ctx)
	defer cancelConnect()
	timer := time.AfterFunc(connectTimeout, cancelConnect)

	client := m.newClient()
	msg, err := client.Receive(connectCtx, code)
	// Stop reports false when the timer already fired. Use that rather than
	// connectCtx.Err(): AfterFunc runs cancelConnect on its own goroutine, so
	// the ctx may not read as cancelled yet at this instant.
	timedOut := !timer.Stop()
	if ctx.Err() != nil {
		return // the user cancelled (or replaced this receive)
	}
	if err != nil {
		if timedOut {
			fail(errNobodyOffering)
			return
		}
		fail(friendlyReceiveError(err))
		return
	}
	if timedOut {
		// The timer won the race against a successful return: msg carries the
		// dead child ctx, so the first Read would fail anyway. Answer the sender
		// and report it as the timeout it is.
		_ = msg.Reject()
		fail(errNobodyOffering)
		return
	}

	var kind string
	switch msg.Type {
	case wormhole.TransferFile:
		kind = KindFile
	case wormhole.TransferDirectory:
		kind = KindFolder
	default:
		// A text message: the library has already acked and consumed it. This is a
		// file transfer, so say so instead of writing a stray file.
		fail(errors.New("the sender offered a text message, not a file"))
		return
	}
	name := safeName(msg.Name)
	// Emit as soon as the offer is known so the UI shows the name and size while
	// the transit connection is still being set up.
	m.updateRecv(gen, true, func(s *ReceiveState) {
		s.Name = name
		s.Kind = kind
		s.Total = msg.TransferBytes64
		s.FileCount = msg.FileCount
		s.Message = "Connecting…"
	})

	// From here until the first Read, every exit must answer the sender: the
	// library keeps the rendezvous websocket open after Receive returns and only
	// the file-ack or a Reject closes it, so bailing out silently would leave the
	// other side on "Waiting for the receiver…" and leak the socket. Reject is a
	// no-op (it returns an error we ignore) once a Read has happened, which makes
	// it safe to call on every failure path below, early or late.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		_ = msg.Reject()
		fail(err)
		return
	}
	if ctx.Err() != nil {
		_ = msg.Reject() // Cancel landed between the offer and the first Read
		return
	}

	// Count wire bytes as they come off the decrypting reader. The first Read is
	// what sends the file-ack and dials transit, so "receiving" is first seen here.
	// The first Read is the connecting→receiving phase change: force that emit so
	// the throttle (primed by the offer emit moments ago) cannot swallow it.
	// io.Copy serialises the Reads, so the flag needs no lock.
	first := true
	cr := &countingReader{r: msg, onRead: func(n int64) {
		force := first
		first = false
		m.updateRecv(gen, force, func(s *ReceiveState) {
			s.Phase = RecvReceiving
			s.Received = n
			s.Message = fmt.Sprintf("Receiving… %d%%", percent(n, s.Total))
		})
	}}

	var outPath string
	if kind == KindFile {
		outPath, err = receiveFile(ctx, dir, name, cr)
	} else {
		outPath, err = receiveFolder(ctx, dir, name, cr, msg.UncompressedBytes64)
	}
	if err != nil {
		_ = msg.Reject() // only does anything if the failure came before the first Read
		if ctx.Err() != nil {
			return
		}
		fail(err)
		return
	}
	m.updateRecv(gen, true, func(s *ReceiveState) {
		s.Active = false
		s.Phase = RecvDone
		s.OutPath = outPath
		s.Received = cr.n
		if s.Total == 0 {
			s.Total = cr.n
		}
		s.Message = "Saved " + filepath.Base(outPath) + " to " + dir
	})
}

// friendlyReceiveError rewords the library's wrong-code failure. With a wrong
// passphrase SPAKE still "succeeds" but the first encrypted message fails to
// open, so the user sees "decrypt message failed" for what is really a typo.
func friendlyReceiveError(err error) error {
	if err != nil && err.Error() == "decrypt message failed" {
		return errors.New("wrong code, or the sender gave up — codes are single-use")
	}
	return err
}

// countingReader tallies bytes passed through r and reports the running total.
type countingReader struct {
	r      io.Reader
	n      int64
	onRead func(total int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.n += int64(n)
		if c.onRead != nil {
			c.onRead(c.n)
		}
	}
	return n, err
}

// spoolPath returns a fresh hidden-ish temp path in dir: ".transfer-<rand><suffix>".
// It lives IN the destination folder (not %TEMP%) so the final rename is a
// same-volume move — atomic and instant even for multi-GB downloads, where a
// cross-volume rename would silently become a second full copy.
func spoolPath(dir, suffix string) string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return filepath.Join(dir, ".transfer-"+hex.EncodeToString(b[:])+suffix)
}

// receiveFile claims a non-colliding final name FIRST (an empty placeholder,
// so an unwritable name — too long, a path past MAX_PATH — fails before a single
// byte is pulled rather than after the whole download), streams r into a .part
// file in dir, then renames the .part over the placeholder. Any failure
// (including ctx cancel, which the library surfaces as a Read error) removes
// both the .part and the placeholder.
func receiveFile(ctx context.Context, dir, name string, r io.Reader) (string, error) {
	final, err := claimFile(dir, name)
	if err != nil {
		return "", err
	}
	part := spoolPath(dir, ".part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		_ = os.Remove(final)
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
			_ = os.Remove(part)
			_ = os.Remove(final)
		}
	}()
	if _, err := io.Copy(f, r); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	// os.Rename on Windows replaces an existing target, which is exactly what
	// the placeholder is for: the claim was atomic, the swap is too.
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	ok = true
	return final, nil
}

// receiveFolder claims "<name>/" (with a collision suffix) first, spools the
// incoming zip to dir, extracts with zip-slip protection and a size budget
// derived from what the sender announced, and deletes the spool. On any
// failure the claimed folder — half-extracted or still empty — is removed too,
// so the user never finds a partial tree masquerading as the real thing.
func receiveFolder(ctx context.Context, dir, name string, r io.Reader, announcedBytes int64) (string, error) {
	root, err := claimDir(dir, name)
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(root)
		}
	}()
	spool := spoolPath(dir, ".zip.part")
	f, err := os.OpenFile(spool, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(spool) }()
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := extractZip(ctx, spool, root, extractBudget(announcedBytes)); err != nil {
		return "", err
	}
	ok = true
	return root, nil
}
