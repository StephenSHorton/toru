//go:build windows

package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/board"
	"github.com/StephenSHorton/toru/internal/boardexport"
	"github.com/StephenSHorton/toru/internal/webprint"
)

// contrastScript measures a whiteboard page's colours in the page itself
// and posts "contrast:{...}" (testdata/theme-contrast.js).
//
//go:embed testdata/theme-contrast.js
var contrastScript string

type contrastReport struct {
	Theme    string  `json:"theme"`
	Checked  int     `json:"checked"`
	MinText  float64 `json:"minText"`
	MinEdge  float64 `json:"minEdge"`
	MinInk   float64 `json:"minInk"`
	Failures []struct {
		Kind  string  `json:"kind"`
		What  string  `json:"what"`
		Ratio float64 `json:"ratio"`
		Need  float64 `json:"need"`
		Fg    string  `json:"fg"`
		Bg    string  `json:"bg"`
	} `json:"failures"`
}

// themeContrast opens a page in WebView2 (a file, or a URL when url is set)
// and measures its contrast.
func themeContrast(t *testing.T, path, url string) contrastReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	p, err := webprint.Open(ctx, path, webprint.Options{DataDir: filepath.Join(t.TempDir(), "webview"), URL: url, Width: 1600, Height: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.Eval(ctx, contrastScript); err != nil {
		t.Fatal(err)
	}
	msg, err := p.Next(ctx, "contrast:")
	if err != nil {
		t.Fatal(err)
	}
	var r contrastReport
	if err := json.Unmarshal([]byte(msg), &r); err != nil {
		t.Fatalf("bad report %q: %v", msg, err)
	}
	return r
}

func logContrast(t *testing.T, name string, r contrastReport) {
	t.Logf("%s, %s theme in WebView2: %d colour pairs; lowest text %.2f:1 (needs 4.5, 3 when large), control edge %.2f:1 (needs 3), pen stroke %.2f:1 (needs 3)",
		name, r.Theme, r.Checked, r.MinText, r.MinEdge, r.MinInk)
	for _, f := range r.Failures {
		t.Errorf("%s, %s: %s %q is %.2f:1, needs %.1f (%s on %s)", name, r.Theme, f.Kind, f.What, f.Ratio, f.Need, f.Fg, f.Bg)
	}
}

// themeBoard is the board the check measures: TORU_THEME_BOARD (a
// .toruboard file) or one with every block type, colour and pen.
func themeBoard(t *testing.T) board.Board {
	t.Helper()
	if path := os.Getenv("TORU_THEME_BOARD"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		b, err := board.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := board.New("brd-themes", "Every block in both themes", time.Now().UTC())
	add := func(id, typ string, x, y, w, h float64, props string) {
		b.Blocks = append(b.Blocks, board.Block{ID: id, Type: typ, X: x, Y: y, W: w, H: h, Props: json.RawMessage(props)})
	}
	colours := []string{"yellow", "pink", "blue", "green", "purple", "orange", "grey"}
	for i, c := range colours {
		x := float64(i) * 240
		b.Frames = append(b.Frames, board.Frame{ID: "frame-" + c, Name: "Frame " + c, X: x, Y: 0, W: 220, H: 360, Color: c})
		add("sticky-"+c, "sticky", x+10, 20, 200, 150, `{"text":"A `+c+` sticky note","color":"`+c+`"}`)
		add("card-"+c, "card", x+10, 190, 200, 140, `{"title":"A `+c+` card","body":"Notes under the title","color":"`+c+`"}`)
	}
	add("text", "text", 0, 400, 400, 60, `{"text":"Text on the canvas","size":"m"}`)
	add("check", "checklist", 440, 400, 260, 160, `{"title":"Checklist","items":[{"text":"Done","done":true},{"text":"Not yet"}]}`)
	add("poll", "poll", 740, 400, 260, 180, `{"question":"A poll","options":[{"label":"One","votes":2},{"label":"Two","votes":1}]}`)
	add("table", "table", 1040, 400, 320, 160, `{"title":"Table","columns":["Name","Price"],"rows":[["Box","$5"],["Truck","$300"]]}`)
	add("rating", "rating", 1400, 400, 220, 90, `{"title":"Rating","value":3}`)
	add("tags", "tag-list", 1400, 520, 220, 90, `{"title":"Tags","items":["one","two"]}`)
	pens := []string{"ink-white", "ink-blue", "ink-red", "ink-yellow", "ink-green"}
	for i, c := range pens {
		y := 640 + float64(i)*30
		b.Ink = append(b.Ink, board.Stroke{ID: "ink-" + c, Points: []float64{0, y, 0.5, 400, y, 0.5}, Color: c, Width: 4})
	}
	return b
}

// The whiteboard's two themes, measured in WebView2 on this PC: every text
// against what's behind it (4.5:1, or 3:1 when large), the edges of text
// fields, checkboxes and radio buttons (3:1) and every pen stroke against
// the canvas (3:1). It runs on the exported board page (the window's own
// canvas and blocks, the frosted header, Fit), drawn as Go's export draws
// it. With TORU_THEME_WINDOW_URL set (the board demo server:
// http://127.0.0.1:47850/?view=whiteboard&id=brd-demo&link=http) the
// whiteboard window itself, with its toolbar, is measured too. With
// TORU_THEME_OUT set, each theme's PNG export is written there.
func TestWhiteboardThemeContrastInWebView2(t *testing.T) {
	exporter := newBoardExporter()
	page, err := exporter.Viewer()
	if err != nil || !bytes.Contains(page, []byte(`<script id="toru-board" type="application/json">null</script>`)) {
		t.Skip("the board viewer isn't built into this binary (run `bun run build` in frontend)")
	}
	exporter.WebViewDir = filepath.Join(t.TempDir(), "export-webview")
	b := themeBoard(t)
	out := os.Getenv("TORU_THEME_OUT")
	for _, theme := range []string{themeLight, themeDark} {
		exporter.Theme = func() string { return theme }
		html, err := exporter.Export(t.Context(), b, boardexport.HTML)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "board.html")
		if err := os.WriteFile(path, html, 0o600); err != nil {
			t.Fatal(err)
		}
		r := themeContrast(t, path, "")
		if r.Theme != theme {
			t.Fatalf("the exported page shows the %q theme, want %q", r.Theme, theme)
		}
		if r.Checked < 20 {
			t.Fatalf("only %d colour pairs measured: the board didn't render", r.Checked)
		}
		logContrast(t, "exported board", r)

		if u := os.Getenv("TORU_THEME_WINDOW_URL"); u != "" {
			sep := "?"
			if strings.Contains(u, "?") {
				sep = "&"
			}
			r := themeContrast(t, "", u+sep+"theme="+theme)
			if r.Theme != theme {
				t.Fatalf("the window shows the %q theme, want %q", r.Theme, theme)
			}
			logContrast(t, "whiteboard window", r)
		}
		if out != "" {
			for _, f := range []boardexport.Format{boardexport.PNG, boardexport.PDF, boardexport.HTML} {
				data, err := exporter.Export(t.Context(), b, f)
				if err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(out, fmt.Sprintf("board-%s%s", theme, f.Ext()))
				if err := os.WriteFile(name, data, 0o644); err != nil {
					t.Fatal(err)
				}
				t.Logf("wrote %s", name)
			}
		}
	}
}
