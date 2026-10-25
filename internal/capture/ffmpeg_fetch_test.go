package capture

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeFFmpegZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"ffmpeg-x/bin/" + ffmpegName(): "ff",
		"ffmpeg-x/bin/ffprobe.exe":     "probe",
		"ffmpeg-x/bin/avcodec-63.dll":  "dll",
		"ffmpeg-x/bin/ffplay.exe":      "skip",
		"ffmpeg-x/doc/ffmpeg.html":     "skip",
		"ffmpeg-x/lib/avcodec.lib":     "skip",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFetchFFmpegVerifiesAndUnpacksBin(t *testing.T) {
	archive := fakeFFmpegZip(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()
	sum := sha256.Sum256(archive)

	parent := t.TempDir()
	_ = os.MkdirAll(filepath.Join(parent, "old-build"), 0o755)
	dir := filepath.Join(parent, "new-build")
	var last int64
	if err := fetchFFmpeg(context.Background(), srv.URL, hex.EncodeToString(sum[:]), dir, func(done, _ int64) { last = done }); err != nil {
		t.Fatal(err)
	}
	if last != int64(len(archive)) {
		t.Errorf("progress ended at %d, want %d", last, len(archive))
	}
	for _, want := range []string{ffmpegName(), "ffprobe.exe", "avcodec-63.dll"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s not unpacked: %v", want, err)
		}
	}
	for _, skip := range []string{"ffplay.exe", "ffmpeg.html", "avcodec.lib"} {
		if _, err := os.Stat(filepath.Join(dir, skip)); err == nil {
			t.Errorf("%s should not be unpacked", skip)
		}
	}
	if _, err := os.Stat(filepath.Join(parent, "old-build")); !os.IsNotExist(err) {
		t.Error("an earlier build should be pruned")
	}

	// A tampered download is rejected and nothing is installed.
	bad := filepath.Join(parent, "bad-build")
	err := fetchFFmpeg(context.Background(), srv.URL, strings.Repeat("0", 64), bad, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("want a checksum error, got %v", err)
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Error("a rejected download must not leave a build behind")
	}
	if left, _ := filepath.Glob(filepath.Join(parent, "download-*")); len(left) != 0 {
		t.Errorf("temp download left behind: %v", left)
	}
}
