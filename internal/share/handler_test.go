package share

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandlerServesViewerAndPlaylist(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.m3u8"), []byte("#EXTM3U\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := (&live{kind: "hls", dir: dir, frames: func() (uint64, []byte) { return 0, nil }}).handler()

	res := get(t, h, "/")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "Waiting for the stream") {
		t.Fatalf("viewer status %d body %q", res.Code, res.Body.String())
	}
	if !strings.Contains(string(hlsJS), "Hls") {
		t.Fatal("embedded hls.js does not look like the player")
	}

	res = get(t, h, "/api/info")
	var info map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["kind"] != "hls" || info["playlist"] != "/live/index.m3u8" {
		t.Fatalf("info = %#v", info)
	}

	res = get(t, h, "/live/index.m3u8")
	if res.Code != 200 || !strings.Contains(res.Body.String(), "#EXTM3U") {
		t.Fatalf("playlist status %d body %q type %q", res.Code, res.Body.String(), res.Header().Get("Content-Type"))
	}
	if cc := res.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("playlist cache-control = %q", cc)
	}
}

func TestCardInfoUsesPort(t *testing.T) {
	info := cardInfo(47321, "hls")
	if info.Port != 47321 || info.URL == "" || !strings.Contains(info.URL, ":47321") {
		t.Fatalf("info = %+v", info)
	}
	if info.QRPng == "" {
		t.Fatal("expected a QR code")
	}
	if info.Kind != "hls" {
		t.Fatal(info.Kind)
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}
