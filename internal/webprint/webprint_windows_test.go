//go:build windows

package webprint

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPrintPDF opens a dark page out of sight, waits for its message, and
// prints it to a PDF the page's size: the file has the page's text as text
// (a font and text operators), its background, and one page.
func TestPrintPDF(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	html := `<!doctype html><meta charset="utf-8"><style>
html,body{margin:0;background:#101014;color:#f5f5f5;font:24px sans-serif;-webkit-print-color-adjust:exact;print-color-adjust:exact}
</style><body><p>Hello from the board</p><svg width="200" height="40"><line x1="0" y1="20" x2="200" y2="20" stroke="#4af" stroke-width="3"/></svg>
<script>window.chrome.webview.postMessage("ready:" + document.querySelector("p").textContent)</script>`
	if err := os.WriteFile(page, []byte(html), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	p, err := Open(ctx, page, Options{DataDir: filepath.Join(dir, "webview")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got, err := p.Next(ctx, "ready:")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Hello from the board" {
		t.Fatalf("page said %q", got)
	}
	// Eval and an answer back.
	if err := p.Eval(ctx, `window.chrome.webview.postMessage("sum:" + (2 + 3))`); err != nil {
		t.Fatal(err)
	}
	if got, err := p.Next(ctx, "sum:"); err != nil || got != "5" {
		t.Fatalf("eval answered %q, %v", got, err)
	}

	out := filepath.Join(dir, "page.pdf")
	if err := p.PrintPDF(ctx, out, PDFOptions{PageWidth: 4, PageHeight: 2, Backgrounds: true}); err != nil {
		t.Fatal(err)
	}
	pdf, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", pdf[:min(len(pdf), 16)])
	}
	for _, want := range []string{"/Font", "/MediaBox [0 0 288 144]"} {
		if !bytes.Contains(pdf, []byte(want)) {
			t.Errorf("the PDF has no %q", want)
		}
	}
	if n := bytes.Count(pdf, []byte("/Type /Page\n")) + bytes.Count(pdf, []byte("/Type /Page ")) + bytes.Count(pdf, []byte("/Type /Page>")); n > 1 {
		t.Errorf("the PDF has %d pages, want 1", n)
	}
	t.Logf("%d bytes", len(pdf))
}

// A WebView2 that fails to start fails Open with its error; it doesn't end
// the process (go-webview2 calls os.Exit after its error callback).
func TestBrokenWebViewFailsThePage(t *testing.T) {
	dir := t.TempDir()
	page := filepath.Join(dir, "page.html")
	if err := os.WriteFile(page, []byte("<p>hi</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	p, err := Open(ctx, page, Options{DataDir: filepath.Join(dir, "webview"), browserPath: filepath.Join(dir, "no-such-runtime")})
	if err == nil {
		p.Close()
		t.Fatal("opened with no WebView2 runtime")
	}
	if !strings.Contains(err.Error(), "no-such-runtime") {
		t.Errorf("error %q doesn't say what failed", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("took %v to fail", d)
	}
}
