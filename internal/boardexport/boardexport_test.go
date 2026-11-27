package boardexport

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/webprint"
)

// The board viewer the front end builds. Without it (a fresh clone, before
// `bun run build`), the tests that need it skip.
var viewerPath = filepath.Join("..", "..", "frontend", "dist", "board-export.html")

const samplePicture = "334fac5220a72cdbb351ea83b8e4a5d48d7bb81d438ba78bf94cd863669c346b.png"

// sampleBoard is the window's sample board (one block of every type, a
// frame, links, a picture) with a stroke of ink, a note saying "</script>",
// and a note far to the right and below, so the board is bigger than any
// screen.
func sampleBoard(t *testing.T) board.Board {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "whiteboard", "sample-board.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := board.New("brd-export01", "Launch plan", time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	b.Name = "Launch plan"
	for i := range b.Links {
		b.Links[i].ID = strings.NewReplacer("->", "-to-").Replace(b.Links[i].ID)
	}
	b.Ink = append(b.Ink, board.Stroke{ID: "ink-1", Points: []float64{1060, 120, 0.5, 1120, 180, 0.6, 1180, 130, 0.5, 1240, 200, 0.5}, Color: "ink-blue", Width: 4, Meta: board.Meta{CreatedBy: board.Person}})
	b.Blocks = append(b.Blocks,
		board.Block{ID: "far-note", Type: "sticky", X: 2600, Y: 1500, W: 220, H: 200, Props: json.RawMessage(`{"text":"Far corner: the board is bigger than the screen","color":"blue"}`), Meta: board.Meta{CreatedBy: board.Person}},
		board.Block{ID: "tricky", Type: "sticky", X: 1060, Y: 360, W: 220, H: 200, Rotation: -12, Props: json.RawMessage(`{"text":"A note saying </script><b>not bold</b> & more","color":"orange"}`), Meta: board.Meta{CreatedBy: board.Person}},
	)
	// A styled, labelled line: the exports draw its label, arrow end, dash and colour.
	b.Links = append(b.Links, board.Link{ID: "lnk-ship-tricky", From: "card-ship", To: "tricky", FromPort: "right", ToPort: "top", Label: "watch out", Props: json.RawMessage(`{"end":"arrow","dash":"dashed","color":"pink"}`), Meta: board.Meta{CreatedBy: board.Person}})
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	return b
}

func exporter(t *testing.T) *Exporter {
	t.Helper()
	return &Exporter{
		Viewer: func() ([]byte, error) { return os.ReadFile(viewerPath) },
		ReadImage: func(asset string) ([]byte, error) {
			return os.ReadFile(filepath.Join("..", "board", "testdata", "images", asset))
		},
		WebViewDir: webViewDir(t),
		Now:        func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}
}

// webViewDir is a folder for a WebView2's data that outlives the test's own
// temp folder. WebView2's browser processes can hold a file in it (such as
// BrowserMetrics) for a while after the page closes, more so on a busy
// machine, and t.TempDir fails the test when it cannot remove its folder.
// The cleanup here retries for a while, then leaves the folder behind with a
// log line instead of failing.
func webViewDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "toru-webview-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(15 * time.Second)
		for {
			err := os.RemoveAll(dir)
			if err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Logf("left %s behind: WebView2 still holds a file in it: %v", dir, err)
				return
			}
			time.Sleep(250 * time.Millisecond)
		}
	})
	return dir
}

func needViewer(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(viewerPath); err != nil {
		// TORU_REQUIRE_VIEWER=1: a missing viewer fails, so a run that
		// means to prove the exports can't pass having skipped them.
		if os.Getenv("TORU_REQUIRE_VIEWER") != "" {
			t.Fatal("the board viewer isn't built (run `bun run build` in frontend)")
		}
		t.Skip("the board viewer isn't built (run `bun run build` in frontend)")
	}
}

// samples writes each export to TORU_EXPORT_SAMPLES (a folder), when set.
func sample(t *testing.T, name string, data []byte) {
	dir := os.Getenv("TORU_EXPORT_SAMPLES")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"png": PNG, ".PDF": PDF, "md": Markdown, "markdown": Markdown, "html": HTML, "toruboard": Toruboard, ".toruboard": Toruboard} {
		if got, err := ParseFormat(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
	if _, err := ParseFormat("svg"); err == nil {
		t.Error("svg parsed")
	}
}

func TestNoViewer(t *testing.T) {
	e := &Exporter{Viewer: func() ([]byte, error) { return nil, os.ErrNotExist }}
	if _, err := e.HTML(board.New("brd-x", "X", time.Now())); err != ErrNoViewer {
		t.Fatalf("got %v, want ErrNoViewer", err)
	}
}

// The HTML export is one file: the viewer's script and styles inline, the
// board and its picture inside, nothing loaded from anywhere, and a note's
// "</script>" kept as text.
func TestHTML(t *testing.T) {
	needViewer(t)
	e := exporter(t)
	b := sampleBoard(t)
	page, err := e.HTML(b)
	if err != nil {
		t.Fatal(err)
	}
	sample(t, "launch-plan.html", page)
	s := string(page)
	if strings.Contains(s, `<script id="toru-board" type="application/json">null</script>`) {
		t.Fatal("the board wasn't put in")
	}
	if !strings.Contains(s, "<title>Launch plan</title>") {
		t.Error("no title")
	}
	if !strings.Contains(s, `"`+samplePicture+`":"data:image/png;base64,iVBOR`) {
		t.Error("the picture isn't inlined")
	}
	// Nothing to fetch: no script src, no stylesheet link, no http(s) address
	// in an attribute.
	if m := regexp.MustCompile(`<(script|link|img)\b[^>]*\b(src|href)=`).FindString(stripScripts(s)); m != "" {
		t.Errorf("the page loads a file: %s", m)
	}
	// Nor do its styles: every url() in them is a data: URL.
	for _, style := range regexp.MustCompile(`(?s)<style>(.*?)</style>`).FindAllStringSubmatch(s, -1) {
		for _, u := range regexp.MustCompile(`url\(\s*['"]?([^'")]*)`).FindAllStringSubmatch(style[1], -1) {
			if !strings.HasPrefix(u[1], "data:") && !strings.HasPrefix(u[1], "#") {
				t.Errorf("a style loads %q", u[1])
			}
		}
	}
	// The board's JSON is one script: its "</script>" note is escaped.
	start := strings.Index(s, `<script id="toru-board" type="application/json">`)
	end := strings.Index(s[start:], "</script>")
	var p struct {
		Board  board.Board       `json:"board"`
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal([]byte(s[start+len(`<script id="toru-board" type="application/json">`):start+end]), &p); err != nil {
		t.Fatal("the board's script doesn't hold the board:", err)
	}
	if len(p.Board.Blocks) != len(b.Blocks) || len(p.Images) != 1 {
		t.Errorf("%d blocks, %d images", len(p.Board.Blocks), len(p.Images))
	}
	// A line's label and style and a block's turn reach the page, which draws them.
	for _, l := range p.Board.Links {
		if l.ID == "lnk-ship-tricky" && (l.Label != "watch out" || !strings.Contains(string(l.Props), `"dash":"dashed"`)) {
			t.Errorf("the styled line came through as %+v", l)
		}
	}
	for _, x := range p.Board.Blocks {
		if x.ID == "tricky" && x.Rotation != -12 {
			t.Errorf("the turned block came through at %v degrees", x.Rotation)
		}
	}
}

// stripScripts drops inline scripts' bodies, which hold code, not tags.
func stripScripts(s string) string {
	return regexp.MustCompile(`(?s)<script\b[^>]*>.*?</script>`).ReplaceAllString(s, "<script></script>")
}

func TestToruboardAndMarkdown(t *testing.T) {
	e := exporter(t)
	b := sampleBoard(t)
	tb, err := e.Export(context.Background(), b, Toruboard)
	if err != nil {
		t.Fatal(err)
	}
	sample(t, "launch-plan.toruboard", tb)
	got, images, err := board.DecodePortable(tb)
	if err != nil || len(images) != 1 || len(got.Blocks) != len(b.Blocks) {
		t.Fatalf("%d blocks, %d images, %v", len(got.Blocks), len(images), err)
	}
	md, err := e.Export(context.Background(), b, Markdown)
	if err != nil {
		t.Fatal(err)
	}
	sample(t, "launch-plan.md", md)
	for _, want := range []string{"# Launch plan", "## Ideas", "- [x] Write the catalog", "| Task | Owner | Due |", "## Links", " → ", "→ A note saying </script><b>not bold</b> & more: watch out"} {
		if !bytes.Contains(md, []byte(want)) {
			t.Errorf("markdown lacks %q", want)
		}
	}
}

func TestPNG(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs WebView2")
	}
	needViewer(t)
	e := exporter(t)
	b := sampleBoard(t)
	data, err := e.PNG(context.Background(), b)
	if err != nil {
		t.Fatal(err)
	}
	sample(t, "launch-plan.png", data)
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// The whole board at 2×: from x -68 to 2868, y -48 to 1748 (blocks plus
	// 48 around), so 2936 × 1796 units, 5872 × 3592 pixels.
	if got := img.Bounds().Size(); got != (image.Point{X: 5872, Y: 3592}) {
		t.Fatalf("size %v, want 5872 × 3592", got)
	}
	bg := img.At(4, 4)
	// The far note, bottom right, is drawn: its middle isn't the background.
	far := img.At((2600+110+68)*2, (1500+100+48)*2)
	if sameColor(far, bg) {
		t.Error("the far note isn't in the picture")
	}
	// So is the first sticky, top left.
	if sameColor(img.At((0+100+68)*2, (100+150+48)*2), bg) {
		t.Error("the first sticky isn't in the picture")
	}
	t.Logf("%d bytes, %v", len(data), img.Bounds().Size())
}

func sameColor(a, b interface{ RGBA() (r, g, b, a uint32) }) bool {
	r1, g1, b1, _ := a.RGBA()
	r2, g2, b2, _ := b.RGBA()
	d := func(x, y uint32) uint32 {
		if x > y {
			return x - y
		}
		return y - x
	}
	return d(r1, r2)+d(g1, g2)+d(b1, b2) < 3*0x0800
}

// The PDF is printed by WebView2's PrintToPdf: one page the board's size,
// with embedded fonts, so its text is text and its lines are paths, not a
// picture of the board.
func TestPDF(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs WebView2")
	}
	needViewer(t)
	e := exporter(t)
	data, err := e.PDF(context.Background(), sampleBoard(t))
	if err != nil {
		t.Fatal(err)
	}
	sample(t, "launch-plan.pdf", data)
	if !bytes.HasPrefix(data, []byte("%PDF-")) {
		t.Fatal("not a PDF")
	}
	// 2936 × 1796 units at 96 an inch, in points (72 an inch).
	if !bytes.Contains(data, []byte("/MediaBox [0 0 2202 1347")) {
		t.Errorf("page size: %s", regexp.MustCompile(`/MediaBox \[[^\]]*\]`).Find(data))
	}
	if n := regexp.MustCompile(`/Type /Page\b[^s]`).FindAll(data, -1); len(n) != 1 {
		t.Errorf("%d pages, want 1", len(n))
	}
	if !bytes.Contains(data, []byte("/FontFile")) {
		t.Error("no embedded font: the text isn't text")
	}
	t.Logf("%d bytes", len(data))
}

// The HTML export stays live with no Toru behind it: in a browser (here a
// WebView2 with nothing but the file), ticking a checklist item and voting
// in the poll change the board on screen.
func TestHTMLStaysLive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs WebView2")
	}
	needViewer(t)
	e := exporter(t)
	page, err := e.HTML(sampleBoard(t))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "board.html")
	if err := os.WriteFile(path, page, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := webprint.Open(ctx, path, webprint.Options{DataDir: e.WebViewDir})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// What the checklist and poll show, once the board has rendered.
	state := `(() => {
		const q = (s) => document.querySelector(s);
		const ticked = q('[data-block="checklist"] [data-slot="block-count"]')?.textContent;
		const votes = q('[data-block="poll"] [data-slot="block-count"]')?.textContent;
		return JSON.stringify({ ticked, votes });
	})()`
	read := func(prefix string) string {
		t.Helper()
		js := `(async () => { for (let i = 0; i < 100 && !document.querySelector('[data-block="poll"]'); i++) await new Promise(r => setTimeout(r, 50));
			window.chrome.webview.postMessage("` + prefix + `" + ` + state + `) })()`
		if err := p.Eval(ctx, js); err != nil {
			t.Fatal(err)
		}
		got, err := p.Next(ctx, prefix)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read("before:"); got != `{"ticked":"2/3","votes":"6 votes"}` {
		t.Fatalf("before: %s", got)
	}
	click := `(() => {
		const boxes = document.querySelectorAll('[data-block="checklist"] [data-slot="checkbox"]');
		boxes[boxes.length - 1].click();
		const radios = document.querySelectorAll('[data-block="poll"] [data-slot="radio"]');
		radios[1].click();
	})()`
	if err := p.Eval(ctx, click); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if got := read("after:"); got != `{"ticked":"3/3","votes":"7 votes"}` {
		t.Fatalf("after ticking and voting: %s", got)
	}
}

// Custom blocks (whiteboard#27) are live in a real browser too: in the HTML
// export, in a WebView2, a person's clicks on a segmented choice's label,
// a counter's Vote button and a checkbox change the blocks on screen.
func TestCustomBlocksClickInWebView2(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("needs WebView2")
	}
	needViewer(t)
	data, err := os.ReadFile(filepath.Join("..", "board", "testdata", "custom-blocks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Blocks []board.BlockDef `json:"blocks"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	b := board.New("brd-custom", "Custom blocks", time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC))
	for i := range f.Blocks {
		f.Blocks[i].Version, f.Blocks[i].Origin = 1, "blt-click"
	}
	b.Types = f.Blocks
	b.Blocks = []board.Block{
		{ID: "idea", Type: "feature-idea", X: 0, Y: 0, W: 340, H: 500, Props: json.RawMessage(`{"title":"Dark mode","votes":2,"status":"idea","effort":3,"tags":[]}`)},
		{ID: "bug", Type: "bug-report", X: 380, Y: 0, W: 380, H: 480, Props: json.RawMessage(`{"title":"Crash","severity":"low","steps":[],"fixed":false}`)},
	}
	e := exporter(t)
	page, err := e.HTML(b)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "custom.html")
	if err := os.WriteFile(path, page, 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	p, err := webprint.Open(ctx, path, webprint.Options{DataDir: e.WebViewDir})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	state := `(() => {
		const q = (s) => document.querySelector(s);
		const votes = q('[data-block="idea"] [data-slot="custom-counter"] [data-slot="block-count"]')?.textContent;
		const status = q('[data-block="idea"] [data-slot="custom-badge"]')?.textContent;
		const severity = q('[data-block="bug"] [data-slot="custom-badge"]')?.textContent;
		const fixed = q('[data-block="bug"] [data-slot="custom-checkbox"] [data-slot="checkbox"]')?.hasAttribute("data-checked");
		return JSON.stringify({ votes, status, severity, fixed });
	})()`
	read := func(prefix string) string {
		t.Helper()
		js := `(async () => { for (let i = 0; i < 100 && !document.querySelector('[data-block="bug"] [data-slot="custom-badge"]'); i++) await new Promise(r => setTimeout(r, 50));
			window.chrome.webview.postMessage("` + prefix + `" + ` + state + `) })()`
		if err := p.Eval(ctx, js); err != nil {
			t.Fatal(err)
		}
		got, err := p.Next(ctx, prefix)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := read("before:"); got != `{"votes":"2","status":"idea","severity":"low","fixed":false}` {
		t.Fatalf("before: %s", got)
	}
	// What a person clicks: the word "building" in the segmented row (its
	// radio is hidden), Vote, the word "critical", and Fixed's box.
	click := `(() => {
		const label = (block, text) => [...document.querySelectorAll('[data-block="' + block + '"] [data-slot="custom-choice"] label')].find((l) => l.textContent.trim() === text);
		label("idea", "building").click();
		document.querySelector('[data-block="idea"] [data-slot="custom-counter"] button').click();
		label("bug", "critical").click();
		document.querySelector('[data-block="bug"] [data-slot="custom-checkbox"] [data-slot="checkbox"]').click();
	})()`
	if err := p.Eval(ctx, click); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	if got := read("after:"); got != `{"votes":"3","status":"building","severity":"critical","fixed":true}` {
		t.Fatalf("after the clicks: %s", got)
	}
}
