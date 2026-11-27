// Package boardexport turns a whiteboard into the files of docs/whiteboard.md
// section 3: an exported .toruboard (pictures inlined), a PNG at 2×, a vector
// PDF, one standalone HTML file that stays live, and a Markdown outline.
//
// HTML is the board viewer the front end builds as one file
// (frontend/dist/board-export.html, from src/board-export) with the board and
// its pictures put in its #toru-board script. PNG and PDF open that same page
// in print mode in a WebView2 nobody sees (internal/webprint): the page lays
// the whole board out at zoom 1, then either draws it into PNG tiles that Go
// joins, or is printed with WebView2's PrintToPdf, text and lines staying
// vector. Neither needs the board's window.
package boardexport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/webprint"
)

// Format is an export format.
type Format string

// The formats.
const (
	Toruboard Format = "toruboard"
	PNG       Format = "png"
	PDF       Format = "pdf"
	HTML      Format = "html"
	Markdown  Format = "markdown"
)

// Formats is every format, in menu order.
var Formats = []Format{Toruboard, PNG, PDF, HTML, Markdown}

// Ext is a format's file extension.
func (f Format) Ext() string {
	switch f {
	case Toruboard:
		return board.Ext
	case Markdown:
		return ".md"
	default:
		return "." + string(f)
	}
}

// ParseFormat reads a format's name (or its extension).
func ParseFormat(s string) (Format, error) {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
	switch s {
	case "md":
		return Markdown, nil
	case "htm":
		return HTML, nil
	}
	for _, f := range Formats {
		if string(f) == s {
			return f, nil
		}
	}
	return "", fmt.Errorf("no export format %q (use toruboard, png, pdf, html or markdown)", s)
}

// Scale is the PNG's pixels per board unit.
const Scale = 2

// MaxPNGPixels caps a PNG: the joined picture is held whole in memory (4
// bytes a pixel) while it's encoded. 2× of a board 6000 units square.
const MaxPNGPixels = 144_000_000

// maxPDFInches is the longest page side a PDF reader takes (PDF's own limit
// is 200 inches).
const maxPDFInches = 200.0

// viewerSlot is the viewer page's empty board, which HTML replaces.
const viewerSlot = `<script id="toru-board" type="application/json">null</script>`

// ErrNoViewer means the front end hasn't been built: dist has no
// board-export.html.
var ErrNoViewer = errors.New("the board viewer isn't built (frontend/dist/board-export.html); run `bun run build` in frontend")

// Exporter makes the export files.
type Exporter struct {
	// Viewer is the built board viewer page, frontend/dist/board-export.html.
	Viewer func() ([]byte, error)
	// ReadImage is a board picture's bytes by name (board.ReadImage).
	ReadImage func(asset string) ([]byte, error)
	// WebViewDir is the export WebView2's user data folder.
	WebViewDir string
	// Now is the export's time (time.Now if nil).
	Now func() time.Time

	mu sync.Mutex // one export WebView2 at a time
}

// Export makes b in format f.
func (e *Exporter) Export(ctx context.Context, b board.Board, f Format) ([]byte, error) {
	switch f {
	case Toruboard:
		return board.EncodePortable(b, e.readImage)
	case Markdown:
		return []byte(board.Markdown(b)), nil
	case HTML:
		return e.HTML(b)
	case PNG:
		return e.PNG(ctx, b)
	case PDF:
		return e.PDF(ctx, b)
	}
	return nil, fmt.Errorf("no export format %q", f)
}

func (e *Exporter) readImage(asset string) ([]byte, error) {
	if e.ReadImage == nil {
		return board.ReadImage(asset)
	}
	return e.ReadImage(asset)
}

func (e *Exporter) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// HTML is b as one standalone page: the board viewer with the board and its
// pictures (as data: URLs) inside.
func (e *Exporter) HTML(b board.Board) ([]byte, error) {
	if e.Viewer == nil {
		return nil, ErrNoViewer
	}
	page, err := e.Viewer()
	if err != nil || len(page) == 0 {
		return nil, ErrNoViewer
	}
	if n := bytes.Count(page, []byte(viewerSlot)); n != 1 {
		return nil, fmt.Errorf("the board viewer page has %d board slots, want 1", n)
	}
	images := map[string]string{}
	for _, a := range board.Assets(b) {
		data, err := e.readImage(a)
		if err != nil {
			continue
		}
		images[a] = "data:" + http.DetectContentType(data) + ";base64," + base64.StdEncoding.EncodeToString(data)
	}
	// json.Marshal escapes <, > and &, so nothing in the board (a note
	// saying "</script>") can end the script early.
	payload, err := json.Marshal(map[string]any{"board": b, "images": images, "exportedAt": e.now().UTC().Format(time.RFC3339)})
	if err != nil {
		return nil, err
	}
	slot := `<script id="toru-board" type="application/json">` + string(payload) + `</script>`
	page = bytes.Replace(page, []byte(viewerSlot), []byte(slot), 1)
	page = bytes.Replace(page, []byte("<title>Toru board</title>"), []byte("<title>"+html.EscapeString(b.Name)+"</title>"), 1)
	return page, nil
}

// printPage opens b's HTML in print mode in an export WebView2 and waits
// for the page to lay the board out. It returns the page's size in board
// units.
func (e *Exporter) printPage(ctx context.Context, b board.Board, work func(p *webprint.Page, width, height int) error) error {
	page, err := e.HTML(b)
	if err != nil {
		return err
	}
	if e.WebViewDir == "" {
		return errors.New("boardexport: no WebView2 folder")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	tmp, err := os.MkdirTemp("", "toru-board-export-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	path := filepath.Join(tmp, "board.html")
	if err := os.WriteFile(path, page, 0o600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	p, err := webprint.Open(ctx, path, webprint.Options{DataDir: e.WebViewDir, Fragment: "print"})
	if err != nil {
		return err
	}
	defer p.Close()
	msg, err := p.Next(ctx, "ready:")
	if err != nil {
		return fmt.Errorf("the board didn't lay out: %w", err)
	}
	var size struct{ Width, Height int }
	if err := json.Unmarshal([]byte(msg), &size); err != nil || size.Width <= 0 || size.Height <= 0 {
		return fmt.Errorf("the board page gave a bad size %q", msg)
	}
	return work(p, size.Width, size.Height)
}

// PDF is b printed through WebView2's PrintToPdf on one page the board's
// size (shrunk to fit when a side would pass 200 inches): text, lines and
// ink stay vector.
func (e *Exporter) PDF(ctx context.Context, b board.Board) ([]byte, error) {
	var out []byte
	err := e.printPage(ctx, b, func(p *webprint.Page, width, height int) error {
		w, h := float64(width)/96, float64(height)/96
		scale := 1.0
		if m := math.Max(w, h); m > maxPDFInches {
			scale = maxPDFInches / m
		}
		tmp, err := os.MkdirTemp("", "toru-board-pdf-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmp) }()
		path := filepath.Join(tmp, "board.pdf")
		if err := p.PrintPDF(ctx, path, webprint.PDFOptions{PageWidth: w * scale, PageHeight: h * scale, Scale: scale, Backgrounds: true}); err != nil {
			return err
		}
		out, err = os.ReadFile(path)
		return err
	})
	return out, err
}

// PNG is b drawn at 2×, whole: the page draws it in tiles, which are joined
// here.
func (e *Exporter) PNG(ctx context.Context, b board.Board) ([]byte, error) {
	var out []byte
	err := e.printPage(ctx, b, func(p *webprint.Page, width, height int) error {
		W, H := width*Scale, height*Scale
		if W*H > MaxPNGPixels {
			return fmt.Errorf("the board is too big for a 2× PNG (%d × %d pixels, %d million at most); export a PDF instead", W, H, MaxPNGPixels/1_000_000)
		}
		if err := p.Eval(ctx, fmt.Sprintf("window.toruExport.png(%d)", Scale)); err != nil {
			return err
		}
		canvas := image.NewNRGBA(image.Rect(0, 0, W, H))
		for {
			msg, err := p.Next(ctx, "")
			if err != nil {
				return err
			}
			switch {
			case strings.HasPrefix(msg, "tile:"):
				var t struct {
					X, Y, Width, Height int
					PNG                 string
				}
				if err := json.Unmarshal([]byte(msg[len("tile:"):]), &t); err != nil {
					return fmt.Errorf("a bad tile: %w", err)
				}
				raw, err := base64.StdEncoding.DecodeString(t.PNG)
				if err != nil {
					return fmt.Errorf("a bad tile: %w", err)
				}
				img, err := png.Decode(bytes.NewReader(raw))
				if err != nil {
					return fmt.Errorf("a bad tile: %w", err)
				}
				draw.Draw(canvas, image.Rect(t.X, t.Y, t.X+t.Width, t.Y+t.Height), img, img.Bounds().Min, draw.Src)
			case strings.HasPrefix(msg, "png:"):
				var done struct{ Width, Height int }
				if err := json.Unmarshal([]byte(msg[len("png:"):]), &done); err != nil {
					return err
				}
				if done.Width != W || done.Height != H {
					return fmt.Errorf("the page drew %d × %d, want %d × %d", done.Width, done.Height, W, H)
				}
				var buf bytes.Buffer
				if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, canvas); err != nil {
					return err
				}
				out = buf.Bytes()
				return nil
			case strings.HasPrefix(msg, "error:"):
				return errors.New(msg[len("error:"):])
			}
		}
	})
	return out, err
}
