package hato

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/psanford/wormhole-william/wormhole"
)

// runSend is the send goroutine for one generation. Shared-state writes go
// through updateSend (gen-gated); resource cleanup (the open file) is in defers
// and runs whether or not this generation is still live.
func (m *Manager) runSend(ctx context.Context, cancel context.CancelFunc, gen uint64, path, name string, isDir bool) {
	defer cancel()
	defer m.wg.Done()
	defer func() {
		if m.onSendExit != nil {
			m.onSendExit()
		}
	}()

	fail := func(err error) {
		log.Printf("hato: send %s: %v", name, err)
		msg := friendlyError(err).Error()
		m.updateSend(gen, true, func(s *SendState) {
			s.Active = false
			s.Phase = SendError
			s.Error = msg
			s.Message = "Failed: " + msg
		})
	}

	// The first callback is the waiting→sending phase change and must not be
	// throttled away (the "waiting" emit may be only milliseconds old). The
	// library invokes the callback sequentially from one goroutine, so a plain
	// closure flag is safe.
	first := true
	progress := wormhole.WithProgress(func(sent, total int64) {
		force := first
		first = false
		m.updateSend(gen, force, func(s *SendState) {
			s.Phase = SendSending
			s.Sent = sent
			s.Total = total
			s.Message = fmt.Sprintf("Sending… %d%%", percent(sent, total))
		})
	})

	client := m.newClient()
	var (
		code string
		ch   chan wormhole.SendResult
		err  error
	)
	// before is the %TEMP% snapshot taken around a folder send (nil for a file
	// send); see sweepTempZips for why every exit path below sweeps against it.
	var before map[string]bool
	if isDir {
		entries, werr := folderEntries(ctx, path, name)
		if werr != nil {
			if ctx.Err() != nil {
				return
			}
			fail(werr)
			return
		}
		// SendDirectory zips the whole tree into a %TEMP% file BEFORE returning the
		// code, so a big folder spends a while in "preparing". The library's own
		// cleanup (`defer os.Remove` on the still-open zip) silently fails on
		// Windows, so snapshot %TEMP% around the call and sweep what it left once
		// the handle is closed (see sweepTempZips).
		before = tempZipSnapshot()
		code, ch, err = client.SendDirectory(ctx, name, entries, progress)
		if err != nil {
			// The library returned with its zip handle still open (nothing closes
			// zipInfo.file on this path), so the delete can only succeed once the
			// *os.File finalizer has run: retry in the background, and leave the
			// rest to the startup sweep.
			go sweepTempZipsLater(before)
		}
	} else {
		f, oerr := os.Open(path)
		if oerr != nil {
			fail(oerr)
			return
		}
		// Closing the file on exit is also our lever on a stuck library goroutine:
		// if we return early (ctx cancelled) its next Read fails with ErrClosed and
		// it bails out instead of streaming from a handle nobody owns.
		defer func() { _ = f.Close() }()
		code, ch, err = client.SendFile(ctx, name, f, progress)
	}
	if err != nil {
		if ctx.Err() != nil {
			return // stopped before the code existed; state already says so
		}
		fail(err)
		return
	}

	m.updateSend(gen, true, func(s *SendState) {
		s.Phase = SendWaiting
		s.Code = code
		s.Message = "Waiting for the receiver…"
	})

	// Wait for the result OR our own cancellation. We select on ctx rather than
	// trusting the channel alone because the library has windows (between the
	// receiver's PAKE and its file-ack) where ctx is not consulted; StopSend has
	// already flipped the state, so this goroutine just needs to get out of the
	// way and release the file.
	var res wormhole.SendResult
	select {
	case res = <-ch:
		if isDir {
			// The library closes its zip handle before delivering the result, so
			// the delete succeeds now.
			sweepTempZips(before)
		}
	case <-ctx.Done():
		if isDir {
			// Our state already says "stopped"; the library goroutine is still
			// holding the zip open until it notices the cancel and delivers. Sweep
			// the moment it does, off this goroutine, so the plaintext copy of the
			// folder does not sit in %TEMP% until the next restart.
			go func() {
				<-ch
				sweepTempZips(before)
			}()
		}
		return
	}
	if ctx.Err() != nil {
		return
	}
	if !res.OK {
		err := res.Error
		if err == nil {
			err = errors.New("transfer failed")
		}
		fail(err)
		return
	}
	m.updateSend(gen, true, func(s *SendState) {
		s.Active = false
		s.Phase = SendDone
		if s.Total > 0 {
			s.Sent = s.Total
		}
		s.Message = "Sent " + s.Name
	})
}

// tempZipPattern matches the scratch zips wormhole-william's makeTmpZip creates
// (ioutil.TempFile("", "wormhole-william-dir")).
var tempZipPattern = filepath.Join(os.TempDir(), "wormhole-william-dir*")

// tempZipSnapshot lists the library scratch zips present right now, so the
// sweep after a folder send removes only what THAT send created and never a
// concurrent CLI's in-flight zip.
func tempZipSnapshot() map[string]bool {
	out := map[string]bool{}
	matches, _ := filepath.Glob(tempZipPattern)
	for _, m := range matches {
		out[m] = true
	}
	return out
}

// sweepTempZips removes scratch zips that appeared since before. Windows
// refuses to delete a file somebody still has open, so callers run this only
// once the library has closed its handle (after the result channel delivers).
// Returns how many it could not remove.
func sweepTempZips(before map[string]bool) int {
	left := 0
	matches, _ := filepath.Glob(tempZipPattern)
	for _, m := range matches {
		if before[m] {
			continue
		}
		if err := os.Remove(m); err != nil && !os.IsNotExist(err) {
			left++
		}
	}
	return left
}

// sweepTempZipsLater handles the paths where the library returned with its
// zip still open and never closes it (SendDirectory failing after makeTmpZip,
// or makeTmpZip itself failing — including our own ctx cancel mid-zip). The
// only thing that closes that handle is the *os.File finalizer, so nudge the
// GC and retry a few times; whatever survives is caught by sweepStaleTempZips
// at the next launch.
func sweepTempZipsLater(before map[string]bool) {
	for _, wait := range []time.Duration{0, 2 * time.Second, 10 * time.Second, time.Minute} {
		time.Sleep(wait)
		runtime.GC()
		if sweepTempZips(before) == 0 {
			return
		}
	}
}

// staleTempZipAge is how old a wormhole-william scratch zip must be before the
// startup sweep treats it as abandoned. A concurrent `wormhole send` CLI zips
// into the same %TEMP% pattern and is in "preparing" for at most a few minutes,
// so anything older belongs to a Toru that quit (or a leaked handle, now freed).
const staleTempZipAge = 10 * time.Minute

// sweepStaleTempZips removes abandoned scratch zips from earlier runs. Called
// once from NewService (not NewManager: tests build Managers and must not touch
// the user's real %TEMP%). Every handle from a previous process is closed by
// definition, so this is where the leaks the in-process sweeps could not reach
// finally go.
func sweepStaleTempZips(olderThan time.Duration) int {
	removed := 0
	matches, _ := filepath.Glob(tempZipPattern)
	cutoff := time.Now().Add(-olderThan)
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil || info.IsDir() || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(m) == nil {
			removed++
		}
	}
	return removed
}

// folderEntries walks root and builds the library's directory manifest. Paths
// are "<base>/<rel-with-forward-slashes>" because the library prefix-checks
// against base + "/" and filepath.Rel hands back backslashes on Windows.
// Symlinks are skipped rather than followed: a link out of the folder would
// quietly ship files the user never pointed at. Readers open lazily; the
// library consumes them while zipping, synchronously inside SendDirectory and
// without ever looking at ctx — so ctx is checked here, in the walk and in
// every Reader, which is the only hook Toru has to make StopSend (or a
// replacing StartSend) abort a long "preparing" phase instead of deflating the
// whole folder for nothing. makeTmpZip returns at the first Reader error.
func folderEntries(ctx context.Context, root, base string) ([]wormhole.DirectoryEntry, error) {
	var entries []wormhole.DirectoryEntry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		file := p
		entries = append(entries, wormhole.DirectoryEntry{
			Path: base + "/" + filepath.ToSlash(rel),
			Mode: info.Mode().Perm(),
			Reader: func() (io.ReadCloser, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return os.Open(file)
			},
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("folder is empty")
	}
	return entries, nil
}
