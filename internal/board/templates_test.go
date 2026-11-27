package board

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TORU_UPDATE_TEMPLATES=1 go test ./internal/board -run 'TestTemplatesFile|TestLayoutGolden'
// rewrites the files the board window reads.
const updateEnv = "TORU_UPDATE_TEMPLATES"

func TestTemplatesMakeReadyBoards(t *testing.T) {
	if n := len(Templates()); n < 5 {
		t.Fatalf("%d templates, want at least 5", n)
	}
	for _, name := range TemplateNames() {
		t.Run(name, func(t *testing.T) {
			op, err := TemplateOp(name, "Launch plan")
			if err != nil {
				t.Fatal(err)
			}
			op.Source = Agent("test")
			d := NewDoc(New("b-1", "Launch plan", time.Now()))
			if _, err := d.Apply(op); err != nil {
				t.Fatalf("apply: %v", err)
			}
			b := d.Board()
			if len(b.Blocks) < 4 {
				t.Fatalf("only %d blocks", len(b.Blocks))
			}
			if err := b.Validate(); err != nil {
				t.Fatal(err)
			}
			frames := map[string]Frame{}
			for _, f := range b.Frames {
				frames[f.ID] = f
			}
			tagged := 0
			for _, x := range b.Blocks {
				if err := CheckProps(x.Type, x.Props); err != nil {
					t.Errorf("block %s: %v", x.ID, err)
				}
				if firstTag(x.Props) != "" {
					tagged++
				}
				if x.Frame != "" {
					f := frames[x.Frame]
					if x.X < f.X || x.Y < f.Y+tplHeadH-1 || x.X+x.W > f.X+f.W || x.Y+x.H > f.Y+f.H {
						t.Errorf("block %s sticks out of frame %s", x.ID, f.ID)
					}
				}
			}
			if tagged == 0 {
				t.Error("no block has a tag, so cluster by tag does nothing")
			}
			// No two blocks overlap.
			for i, a := range b.Blocks {
				for _, c := range b.Blocks[i+1:] {
					if a.X < c.X+c.W && c.X < a.X+a.W && a.Y < c.Y+c.H && c.Y < a.Y+a.H {
						t.Errorf("blocks %s and %s overlap", a.ID, c.ID)
					}
				}
			}
			// The topic heads the board.
			if md := Markdown(b); !strings.Contains(md, "Launch plan") {
				t.Errorf("markdown lacks the topic:\n%s", md)
			}
			for _, kind := range []string{LayoutGrid, LayoutTree, LayoutCluster} {
				if _, err := Layout(b, nil, kind, LayoutOptions{}); err != nil {
					t.Errorf("layout %s: %v", kind, err)
				}
			}
		})
	}
	if _, err := TemplateOp("nope", ""); err == nil {
		t.Error("unknown template accepted")
	}
}

func TestMindMapIsATree(t *testing.T) {
	op, _ := TemplateOp("mind-map", "")
	if len(op.Links) != 12 {
		t.Fatalf("%d links, want 4 branches and 8 ideas", len(op.Links))
	}
	b := New("m", "m", time.Now())
	b.Blocks, b.Links = op.Blocks, op.Links
	pos, err := Layout(b, nil, LayoutTree, LayoutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if pos["topic"].X >= pos["branch-1"].X || pos["branch-1"].X >= pos["branch-1-1"].X {
		t.Errorf("tree layout doesn't put the topic, then branches, then ideas left to right: %v", pos)
	}
}

// templateFile is frontend/src/whiteboard/templates.json: every template as
// the board window places it.
type templateFile struct {
	Note      string         `json:"_note"`
	Templates []templateItem `json:"templates"`
}

type templateItem struct {
	TemplateInfo
	Frames []Frame `json:"frames"`
	Blocks []Block `json:"blocks"`
	Links  []Link  `json:"links"`
}

func encodeJSON(t *testing.T, v any) []byte {
	t.Helper()
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// golden compares got with the file at path, or writes it under updateEnv.
func golden(t *testing.T, path string, got []byte) {
	t.Helper()
	if os.Getenv(updateEnv) != "" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with %s=1 to write it)", err, updateEnv)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), got) {
		t.Fatalf("%s is out of date: run %s=1 go test ./internal/board -run 'TestTemplatesFile|TestLayoutGolden'", path, updateEnv)
	}
}

func TestTemplatesFile(t *testing.T) {
	f := templateFile{Note: "Written by internal/board/templates_test.go from internal/board/templates.go. Don't edit by hand."}
	for _, info := range Templates() {
		op, err := TemplateOp(info.Name, "")
		if err != nil {
			t.Fatal(err)
		}
		item := templateItem{TemplateInfo: info, Frames: op.Frames, Blocks: op.Blocks, Links: op.Links}
		if item.Frames == nil {
			item.Frames = []Frame{}
		}
		if item.Links == nil {
			item.Links = []Link{}
		}
		f.Templates = append(f.Templates, item)
	}
	golden(t, filepath.Join("..", "..", "frontend", "src", "whiteboard", "templates.json"), encodeJSON(t, f))
}

// TestLayoutGolden writes Go's layouts of one board for the board window's
// own layout (frontend/src/whiteboard/layout.ts) to match, in board-check.
func TestLayoutGolden(t *testing.T) {
	op, _ := TemplateOp("mind-map", "")
	b := New("g", "g", time.Now())
	b.Blocks, b.Links = op.Blocks, op.Links
	// Uneven sizes, so the layouts depend on them.
	b.Blocks[1].W, b.Blocks[1].H = 300, 160
	type caseOut struct {
		Kind      string           `json:"kind"`
		Direction string           `json:"direction,omitempty"`
		Columns   int              `json:"columns,omitempty"`
		Gap       float64          `json:"gap,omitempty"`
		Only      []string         `json:"only,omitempty"`
		Positions map[string]Point `json:"positions"`
	}
	cases := []caseOut{
		{Kind: LayoutGrid},
		{Kind: LayoutGrid, Columns: 3, Gap: 10},
		{Kind: LayoutTree},
		{Kind: LayoutTree, Direction: "down"},
		{Kind: LayoutCluster},
		{Kind: LayoutCluster, Only: []string{"branch-2", "branch-1-1", "topic", "branch-1"}},
	}
	for i, c := range cases {
		pos, err := Layout(b, c.Only, c.Kind, LayoutOptions{Gap: c.Gap, Columns: c.Columns, Direction: c.Direction})
		if err != nil {
			t.Fatal(err)
		}
		cases[i].Positions = pos
	}
	golden(t, filepath.Join("testdata", "layout-golden.json"), encodeJSON(t, map[string]any{
		"_note":  "Written by TestLayoutGolden (internal/board/templates_test.go). board-check runs the window's layout on these blocks and links and compares.",
		"blocks": b.Blocks,
		"links":  b.Links,
		"cases":  cases,
	}))
}
