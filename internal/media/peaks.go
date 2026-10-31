package media

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// Waveform peaks for the timeline: the loudest sample of every 1/PeaksRate s
// of a file's sound, as one byte on a decibel scale (0 = -PeaksFloorDB or
// quieter, 255 = full scale). The timeline draws them and moves them by a
// clip's volume in dB, so a byte per bin is plenty and a 3-hour recording
// is about 1 MB.
const (
	PeaksRate    = 100 // bins per second of source
	PeaksFloorDB = 60  // dB below full scale that maps to 0
	peaksPrefix  = "/__media/peaks/"
	// The rate ffmpeg resamples to before taking peaks: high enough to keep
	// transients, low enough to read hours of sound quickly.
	peaksSampleRate = 8000
)

// peaksDir is where peaks are cached, next to the clip thumbnails; a var so
// tests can move it.
var peaksDir = func() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "toru", "peaks")
	}
	return filepath.Join(os.TempDir(), "toru", "peaks")
}

var (
	peaksLocks sync.Map                    // cache path -> *sync.Mutex
	peaksSem   = make(chan struct{}, 2)    // decodes at once
	errNoSound = errors.New("no sound")
)

// servePeaks answers /__media/peaks/<id>: the file's peaks (see PeaksRate),
// made on the first request. 204 when the file has no sound.
func (s *Service) servePeaks(w http.ResponseWriter, r *http.Request, id string) {
	m, ok := s.get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := ensurePeaks(r.Context(), m)
	if errors.Is(err, errNoSound) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Peaks-Rate", fmt.Sprint(PeaksRate))
	http.ServeFile(w, r, f)
}

// peaksStream is the audio stream the waveform shows: the mixed track when
// a recording has separate ones, else the first.
func peaksStream(m Media) int {
	for _, t := range m.Tracks {
		if t.IsMix {
			return t.Index
		}
	}
	if len(m.Tracks) > 0 {
		return m.Tracks[0].Index
	}
	return 0
}

// ensurePeaks returns the cached peaks file for m, decoding its sound on the
// first call. The key has the file's size and mtime, so a replaced file gets
// new peaks.
func ensurePeaks(ctx context.Context, m Media) (string, error) {
	if !m.HasAudio {
		return "", errNoSound
	}
	fi, err := os.Stat(m.Path)
	if err != nil {
		return "", err
	}
	out := filepath.Join(peaksDir(), fmt.Sprintf("%s-%d-%x-%x.peaks", m.ID, peaksStream(m), fi.Size(), fi.ModTime().UnixNano()))
	lk, _ := peaksLocks.LoadOrStore(out, &sync.Mutex{})
	mu := lk.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	select {
	case peaksSem <- struct{}{}:
		defer func() { <-peaksSem }()
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	// Not tied to the request: a timeline that scrolls away and back still
	// gets the finished file, and an hour of sound takes a while.
	dctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	peaks, err := decodePeaks(dctx, m.Path, peaksStream(m))
	if err != nil {
		return "", err
	}
	tmp := out + ".part"
	if err := os.WriteFile(tmp, peaks, 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return out, nil
}

// decodePeaks runs ffmpeg over the stream's sound (mono, resampled) and
// reduces it to peaks as it streams, so memory stays flat for any length.
func decodePeaks(ctx context.Context, path string, stream int) ([]byte, error) {
	bin, err := capture.LocateFFmpeg()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "-v", "error", "-nostdin", "-i", path,
		"-map", fmt.Sprintf("0:a:%d", stream), "-vn", "-sn", "-dn",
		"-ac", "1", "-ar", fmt.Sprint(peaksSampleRate), "-f", "s16le", "-")
	hideConsole(cmd)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	peaks, readErr := peaksFromPCM(bufio.NewReaderSize(stdout, 64<<10), peaksSampleRate/PeaksRate)
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("peaks: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	if len(peaks) == 0 {
		return nil, errNoSound
	}
	return peaks, nil
}

// peaksFromPCM reads s16le mono samples and returns one byte per `per`
// samples (the last bin may be short).
func peaksFromPCM(r io.Reader, per int) ([]byte, error) {
	var out []byte
	buf := make([]byte, 32<<10)
	maxAbs, n := 0, 0
	var carry []byte
	for {
		k, err := r.Read(buf)
		data := buf[:k]
		if len(carry) > 0 && k > 0 {
			data = append(carry, data...)
			carry = nil
		}
		even := len(data) &^ 1
		for i := 0; i < even; i += 2 {
			v := int(int16(binary.LittleEndian.Uint16(data[i:])))
			if v < 0 {
				v = -v
			}
			if v > maxAbs {
				maxAbs = v
			}
			if n++; n == per {
				out = append(out, peakByte(maxAbs))
				maxAbs, n = 0, 0
			}
		}
		if even < len(data) {
			carry = []byte{data[even]}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	if n > 0 {
		out = append(out, peakByte(maxAbs))
	}
	return out, nil
}

// peakByte maps a sample magnitude (0..32768) to the dB byte scale.
func peakByte(v int) byte {
	if v <= 0 {
		return 0
	}
	db := 20 * math.Log10(float64(v)/32768)
	x := (db + PeaksFloorDB) / PeaksFloorDB * 255
	if x <= 0 {
		return 0
	}
	if x >= 255 {
		return 255
	}
	return byte(math.Round(x))
}
