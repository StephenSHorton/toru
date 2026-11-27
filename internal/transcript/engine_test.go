package transcript

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWav(t *testing.T) {
	b := wav([]int16{0, 1, -1, 32767})
	if len(b) != 44+8 || string(b[0:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("header %q", b[:44])
	}
	if rate := binary.LittleEndian.Uint32(b[24:]); rate != SampleRate {
		t.Errorf("rate %d", rate)
	}
	if v := int16(binary.LittleEndian.Uint16(b[48:])); v != -1 {
		t.Errorf("sample 2 = %d", v)
	}
}

// fakeWhisperZip is an archive shaped like whisper.cpp's release.
func fakeWhisperZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"Release/whisper-server.exe", "Release/whisper.dll", "Release/ggml-cpu-haswell.dll",
		"Release/ggml.dll", "Release/SDL2.dll", "Release/whisper-cli.exe", "Release/llama.dll",
		"Release/ggml-cuda.dll", "Release/cudart64_12.dll", "Release/cublas64_12.dll", "Release/cublasLt64_12.dll",
		"Release/nvrtc64_120_0.dll", "Release/nvblas64_12.dll"} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(name))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchZipKeepsOnlyTheServer(t *testing.T) {
	data := fakeWhisperZip(t)
	sum := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer srv.Close()

	dir := filepath.Join(t.TempDir(), "whisper", "b1")
	if err := fetchZip(context.Background(), srv.URL+"/w.zip", "0000", int64(len(data)), dir, extractWhisper, nil); err == nil ||
		!strings.Contains(err.Error(), "checksum") {
		t.Fatalf("bad checksum accepted: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("a failed download left files behind")
	}
	var last int64
	if err := fetchZip(context.Background(), srv.URL+"/w.zip", hex.EncodeToString(sum[:]), int64(len(data)), dir, extractWhisper,
		func(done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if last != int64(len(data)) {
		t.Errorf("progress ended at %d of %d", last, len(data))
	}
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "cublas64_12.dll,cublasLt64_12.dll,cudart64_12.dll,ggml-cpu-haswell.dll,ggml-cuda.dll,ggml.dll,whisper-server.exe,whisper.dll" {
		t.Errorf("unpacked %v", names)
	}
}

func TestEnginePhases(t *testing.T) {
	root := t.TempDir()
	e := NewEngineAt(root, nil)
	if st := e.Status(); st.Phase != PhaseMissing || st.Total != DownloadBytes || st.Threads < 1 || st.Threads > 4 {
		t.Fatalf("status %+v", st)
	}
	if _, err := e.Transcribe(context.Background(), make([]int16, 16000)); err == nil {
		t.Error("transcribed with no model")
	}
	_ = os.MkdirAll(e.binDir(), 0o755)
	_ = os.MkdirAll(filepath.Dir(e.modelPath()), 0o755)
	_ = os.WriteFile(e.serverPath(), []byte("x"), 0o644)
	_ = os.WriteFile(e.modelPath(), []byte("x"), 0o644)
	if NewEngineAt(root, nil).Status().Phase != PhaseReady {
		t.Error("downloaded files not seen as ready")
	}
	if NewEngineAt("", nil).Status().Phase != PhaseUnsupported {
		t.Error("no root should be unsupported")
	}
}

// TestWhisperLive runs the real download and server on a spoken WAV.
//
//	TORU_WHISPER_LIVE=<16 kHz mono wav> go test ./internal/transcript -run WhisperLive -v
func TestWhisperLive(t *testing.T) {
	path := os.Getenv("TORU_WHISPER_LIVE")
	if path == "" {
		t.Skip("set TORU_WHISPER_LIVE=<16 kHz mono 16-bit wav> to run")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, (len(raw)-44)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[44+2*i:]))
	}
	done := make(chan struct{}, 1)
	e := NewEngine(func() {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	e.Download()
	for !e.Ready() {
		<-done
		if st := e.Status(); st.Phase == PhaseError {
			t.Fatal(st.Error)
		}
	}
	defer e.Stop()
	text, err := e.Transcribe(context.Background(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("text: %q", text)
	if strings.TrimSpace(text) == "" {
		t.Error("no text")
	}
}
