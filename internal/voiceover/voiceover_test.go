package voiceover

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func samples(vals ...int16) string {
	b := make([]byte, 2*len(vals))
	for i, v := range vals {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestTakeWritesWav(t *testing.T) {
	root := t.TempDir()
	s := NewAt(root)
	id, err := s.Begin("proj_ab/c", 48000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, samples(1, -1, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(id, samples(-32768, 32767)); err != nil {
		t.Fatal(err)
	}
	path, err := s.Finish(id)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != filepath.Join(root, "proj_ab_c") || !strings.HasSuffix(path, ".wav") {
		t.Fatalf("path %q", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	le := binary.LittleEndian
	if len(b) != headerLen+10 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" || string(b[36:40]) != "data" {
		t.Fatalf("bad header/len %d", len(b))
	}
	if le.Uint32(b[4:]) != 36+10 || le.Uint32(b[40:]) != 10 || le.Uint32(b[24:]) != 48000 || le.Uint16(b[22:]) != 1 || le.Uint16(b[34:]) != 16 {
		t.Fatalf("sizes/format: riff %d data %d rate %d", le.Uint32(b[4:]), le.Uint32(b[40:]), le.Uint32(b[24:]))
	}
	if int16(le.Uint16(b[headerLen+6:])) != -32768 {
		t.Fatal("samples out of order")
	}
	if err := s.Append(id, samples(1)); err == nil {
		t.Fatal("append after finish should fail")
	}
}

func TestTakeEdges(t *testing.T) {
	s := NewAt(t.TempDir())
	if _, err := s.Begin("p", 100); err == nil {
		t.Fatal("rate 100 accepted")
	}
	id, _ := s.Begin("p", 44100)
	if err := s.Append(id, base64.StdEncoding.EncodeToString([]byte{1, 2, 3})); err == nil {
		t.Fatal("odd byte count accepted")
	}
	if _, err := s.Finish(id); err == nil {
		t.Fatal("empty take finished")
	}
	id2, _ := s.Begin("", 44100)
	_ = s.Append(id2, samples(5))
	s.mu.Lock()
	p := s.takes[id2].path
	s.mu.Unlock()
	if filepath.Base(filepath.Dir(p)) != "unsaved" {
		t.Fatalf("empty project dir: %q", p)
	}
	if err := s.Cancel(id2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("cancelled take kept its file")
	}
	if err := s.Cancel("nope"); err != nil {
		t.Fatal(err)
	}
	// Two takes in the same second get distinct files.
	a, _ := s.Begin("q", 44100)
	b, _ := s.Begin("q", 44100)
	_ = s.Append(a, samples(1))
	_ = s.Append(b, samples(1))
	pa, _ := s.Finish(a)
	pb, _ := s.Finish(b)
	if pa == pb || pa == "" || pb == "" {
		t.Fatalf("takes collide: %q %q", pa, pb)
	}
}
