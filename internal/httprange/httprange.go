// Package httprange keeps media requests to the app's asset server small.
//
// The editor and the clip players load video from Toru's own asset server
// (Wails middleware). A <video> element asks for "bytes=N-" (everything from
// N on) and, with a normal HTTP server, stops reading once it has enough.
// WebView2's custom-scheme handler doesn't stop: the whole response is
// produced, so opening a 500 MB recording read all 500 MB, and every seek
// read the rest of the file again (seconds of black preview on big clips).
//
// Cap rewrites an open-ended range to a bounded one. The response is then a
// normal 206 for that chunk (Content-Range: bytes N-M/size), and the media
// stack asks for the next chunk as it plays, as it does with any server that
// answers part of an open range.
package httprange

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// Chunk is how much one open-ended range request returns: about a second of
// a 65 Mbps recording.
const Chunk int64 = 8 << 20

// Cap bounds a single open-ended "bytes=N-" Range on r to Chunk bytes, in
// place. Other ranges (bounded, suffix "bytes=-N", several ranges) and
// requests without one are left alone.
func Cap(r *http.Request) {
	h := r.Header.Get("Range")
	spec, ok := strings.CutPrefix(h, "bytes=")
	if !ok || strings.Contains(spec, ",") {
		return
	}
	start, end, ok := strings.Cut(strings.TrimSpace(spec), "-")
	if !ok || start == "" || strings.TrimSpace(end) != "" {
		return
	}
	n, err := strconv.ParseInt(strings.TrimSpace(start), 10, 64)
	if err != nil || n < 0 {
		return
	}
	r.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", n, n+Chunk-1))
}
