package httprange

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCap(t *testing.T) {
	cases := map[string]string{
		"bytes=0-":      fmt.Sprintf("bytes=0-%d", Chunk-1),
		"bytes=1000-":   fmt.Sprintf("bytes=1000-%d", 1000+Chunk-1),
		"bytes=0-99":    "bytes=0-99",
		"bytes=-500":    "bytes=-500",
		"bytes=0-1,5-9": "bytes=0-1,5-9",
		"":              "",
		"items=0-":      "items=0-",
		"bytes=x-":      "bytes=x-",
	}
	for in, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/f", nil)
		if in != "" {
			r.Header.Set("Range", in)
		}
		Cap(r)
		if got := r.Header.Get("Range"); got != want {
			t.Errorf("Cap(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCapServeContent: through http.ServeContent an open range comes back
// as a 206 of one chunk with the file's full size in Content-Range, and a
// range past the last chunk ends at the file's end.
func TestCapServeContent(t *testing.T) {
	size := int(Chunk*2 + 123)
	data := bytes.Repeat([]byte{7}, size)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Cap(r)
		http.ServeContent(w, r, "v.mp4", time.Time{}, bytes.NewReader(data))
	})
	for _, c := range []struct {
		rng       string
		wantLen   int
		wantRange string
	}{
		{"bytes=0-", int(Chunk), fmt.Sprintf("bytes 0-%d/%d", Chunk-1, size)},
		{fmt.Sprintf("bytes=%d-", 2*Chunk), 123, fmt.Sprintf("bytes %d-%d/%d", 2*Chunk, size-1, size)},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v.mp4", nil)
		req.Header.Set("Range", c.rng)
		h.ServeHTTP(rec, req)
		body, _ := io.ReadAll(rec.Body)
		if rec.Code != http.StatusPartialContent || len(body) != c.wantLen || rec.Header().Get("Content-Range") != c.wantRange {
			t.Errorf("%s: %d, %d bytes, Content-Range %q; want 206, %d, %q", c.rng, rec.Code, len(body), rec.Header().Get("Content-Range"), c.wantLen, c.wantRange)
		}
	}
}
