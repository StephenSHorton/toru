//go:build windows

package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/agentctl"
	"github.com/StephenSHorton/toru/internal/board"
)

// An agent's board.export, wired as main.go wires it: Toru's embedded board
// viewer (frontend/dist/board-export.html, built by `bun run build`) and
// the export WebView2. An agent builds a board with the window closed and
// exports it as PNG, PDF and HTML.
func TestAgentBoardExportAsToruWiresIt(t *testing.T) {
	exporter := newBoardExporter()
	page, err := exporter.Viewer()
	if err != nil || !bytes.Contains(page, []byte(`<script id="toru-board" type="application/json">null</script>`)) {
		if os.Getenv("TORU_REQUIRE_VIEWER") != "" {
			t.Fatalf("Toru's embedded board viewer is missing or has no board slot: %v", err)
		}
		t.Skip("the board viewer isn't built into this binary (run `bun run build` in frontend)")
	}
	exporter.WebViewDir = filepath.Join(t.TempDir(), "webview")

	dir := t.TempDir()
	old := board.Dir
	board.Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { board.Dir = old })
	reg := agentctl.NewRegistry()
	registerBoardAgentCommands(reg, boardAgentDeps{boards: board.NewStore(), exporter: exporter})

	id := call(t, reg, "Claude Code", "board.create", `{"name":"Agent export"}`)["id"].(string)
	call(t, reg, "Claude Code", "board.add", `{"id":"`+id+`","blocks":[
		{"type":"sticky","x":0,"y":0,"w":200,"h":160,"props":{"text":"Drawn with the window closed","color":"yellow"}},
		{"type":"checklist","x":240,"y":0,"w":260,"h":160,"props":{"title":"Steps","items":[{"text":"Build","done":true},{"text":"Export"}]}},
		{"type":"sticky","x":3000,"y":1800,"w":200,"h":160,"props":{"text":"Far away","color":"blue"}}]}`)

	out := t.TempDir()
	export := func(format string) []byte {
		t.Helper()
		p := call(t, reg, "Claude Code", "board.export", `{"id":"`+id+`","format":"`+format+`","path":`+jsonString(filepath.Join(out, "board"))+`}`)["path"].(string)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	img, err := png.Decode(bytes.NewReader(export("png")))
	if err != nil {
		t.Fatal(err)
	}
	// Blocks from (0,0) to (3200,1960), 48 around, at 2×.
	if s := img.Bounds().Size(); s.X != (3200+96)*2 || s.Y != (1960+96)*2 {
		t.Errorf("png is %v, want %d × %d", s, (3200+96)*2, (1960+96)*2)
	}
	pdf := export("pdf")
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.Contains(pdf, []byte("/FontFile")) {
		t.Error("the pdf isn't a PDF with its text as text")
	}
	html := string(export("html"))
	if !strings.Contains(html, "Drawn with the window closed") || !strings.Contains(html, "<title>Agent export</title>") {
		t.Error("the html doesn't hold the board")
	}
}
