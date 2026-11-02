// Package voiceover writes the video editor's mic voiceover takes to disk
// (Irori #129). The edit window captures the mic itself (getUserMedia and
// an AudioWorklet, raw PCM, no echo cancellation, noise suppression or AGC)
// and streams the samples here in chunks. Each take becomes a mono 16-bit
// PCM WAV in %LOCALAPPDATA%/toru/voice/<project>/, a plain file the editor
// registers with MediaService and puts on a "Voice" audio track.
//
// Chunks are appended as they arrive, so a crash mid-take leaves a file
// whose header only needs fixing; Finish writes the real sizes.
package voiceover

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

// MinRate and MaxRate bound the sample rate a take may use (an
// AudioContext runs at 8..96 kHz in practice).
const (
	MinRate   = 8000
	MaxRate   = 192000
	headerLen = 44
)

// VoiceoverService is bound to the frontend.
type VoiceoverService struct {
	mu    sync.Mutex
	root  string // "" = %LOCALAPPDATA%/toru/voice
	takes map[string]*take
	seq   int
}

type take struct {
	f     *os.File
	path  string
	rate  int
	bytes int64 // PCM bytes written after the header
}

// New returns the service; takes go to %LOCALAPPDATA%/toru/voice.
func New() *VoiceoverService { return &VoiceoverService{takes: map[string]*take{}} }

// NewAt returns a service writing under root (tests).
func NewAt(root string) *VoiceoverService {
	return &VoiceoverService{root: root, takes: map[string]*take{}}
}

// dir is where the takes of a project live.
func (s *VoiceoverService) dir(project string) (string, error) {
	root := s.root
	if root == "" {
		base, err := os.UserCacheDir() // %LOCALAPPDATA% on Windows
		if err != nil {
			return "", err
		}
		root = filepath.Join(base, "toru", "voice")
	}
	return filepath.Join(root, safeName(project)), nil
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// safeName keeps a project id usable as a folder name ("" = "unsaved").
func safeName(s string) string {
	s = unsafeChars.ReplaceAllString(s, "_")
	if len(s) > 64 {
		s = s[:64]
	}
	if s == "" || s == "_" {
		return "unsaved"
	}
	return s
}

// Begin opens a new take for a project at sampleRate (mono, 16-bit) and
// returns its id for Append, Finish and Cancel.
func (s *VoiceoverService) Begin(project string, sampleRate int) (string, error) {
	if sampleRate < MinRate || sampleRate > MaxRate {
		return "", fmt.Errorf("voiceover: sample rate %d is out of range", sampleRate)
	}
	dir, err := s.dir(project)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	name := fmt.Sprintf("Voice %s.wav", time.Now().Format("2006-01-02 15-04-05"))
	path := filepath.Join(dir, name)
	for n := 2; exists(path); n++ {
		path = filepath.Join(dir, fmt.Sprintf("Voice %s (%d).wav", time.Now().Format("2006-01-02 15-04-05"), n))
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Write(wavHeader(sampleRate, 0)); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", err
	}
	id := fmt.Sprintf("take-%d-%d", time.Now().UnixNano(), s.seq)
	s.takes[id] = &take{f: f, path: path, rate: sampleRate}
	return id, nil
}

// Append adds base64 little-endian 16-bit mono samples to a take.
func (s *VoiceoverService) Append(id, pcm string) error {
	b, err := base64.StdEncoding.DecodeString(pcm)
	if err != nil {
		return fmt.Errorf("voiceover: bad chunk: %w", err)
	}
	if len(b)%2 != 0 {
		return errors.New("voiceover: a chunk must hold whole 16-bit samples")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.takes[id]
	if !ok {
		return errors.New("voiceover: no such take (it was finished or cancelled)")
	}
	n, err := t.f.Write(b)
	t.bytes += int64(n)
	return err
}

// Finish writes the WAV sizes, closes the take and returns its path. A take
// with no samples is deleted and Finish returns an error.
func (s *VoiceoverService) Finish(id string) (string, error) {
	s.mu.Lock()
	t, ok := s.takes[id]
	delete(s.takes, id)
	s.mu.Unlock()
	if !ok {
		return "", errors.New("voiceover: no such take (it was finished or cancelled)")
	}
	if t.bytes == 0 {
		_ = t.f.Close()
		_ = os.Remove(t.path)
		return "", errors.New("voiceover: the take is empty (no sound reached Toru)")
	}
	if _, err := t.f.Seek(0, io.SeekStart); err == nil {
		_, err = t.f.Write(wavHeader(t.rate, t.bytes))
		if err != nil {
			_ = t.f.Close()
			return "", err
		}
	}
	if err := t.f.Close(); err != nil {
		return "", err
	}
	return t.path, nil
}

// Cancel drops a take and deletes its file. Unknown ids are fine.
func (s *VoiceoverService) Cancel(id string) error {
	s.mu.Lock()
	t, ok := s.takes[id]
	delete(s.takes, id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	_ = t.f.Close()
	return os.Remove(t.path)
}

// ServiceShutdown finishes every open take when the app exits (Wails calls
// it; it isn't bound): what was recorded is kept.
func (s *VoiceoverService) ServiceShutdown() error {
	s.mu.Lock()
	ids := make([]string, 0, len(s.takes))
	for id := range s.takes {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		_, _ = s.Finish(id)
	}
	return nil
}

// wavHeader is a canonical 44-byte PCM WAV header for mono 16-bit samples.
func wavHeader(rate int, dataBytes int64) []byte {
	h := make([]byte, headerLen)
	le := binary.LittleEndian
	copy(h[0:], "RIFF")
	le.PutUint32(h[4:], uint32(36+dataBytes))
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	le.PutUint32(h[16:], 16)
	le.PutUint16(h[20:], 1) // PCM
	le.PutUint16(h[22:], 1) // mono
	le.PutUint32(h[24:], uint32(rate))
	le.PutUint32(h[28:], uint32(rate*2))
	le.PutUint16(h[32:], 2)
	le.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	le.PutUint32(h[40:], uint32(dataBytes))
	return h
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
