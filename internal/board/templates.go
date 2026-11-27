package board

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Brainstorm templates (docs/whiteboard.md, section 6): a board that starts
// ready to fill, made only of block catalog types, frames and links, so it
// is an ordinary board as soon as it's made.
//
// Go is the one source of the templates. board.create's template field uses
// them, and the board window reads the same boards from
// frontend/src/whiteboard/templates.json, which TestTemplatesFile writes
// (TORU_UPDATE_TEMPLATES=1) and checks.
//
// Every block carries props.tags, so board.layout's cluster mode regroups a
// template's notes by column, theme or branch.

// Template is one starting board.
type Template struct {
	Name        string `json:"name"`  // its id, as board.create's template takes it
	Title       string `json:"title"` // what a menu shows
	Description string `json:"description"`
	build       func(topic string) Op
}

// TemplateInfo is a template as board.templates lists it.
type TemplateInfo struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Templates is every template, in menu order.
func Templates() []TemplateInfo {
	out := make([]TemplateInfo, len(templates))
	for i, t := range templates {
		out[i] = TemplateInfo{Name: t.Name, Title: t.Title, Description: t.Description}
	}
	return out
}

// TemplateNames is every template's name, in menu order.
func TemplateNames() []string {
	out := make([]string, len(templates))
	for i, t := range templates {
		out[i] = t.Name
	}
	return out
}

// TemplateOp is the op that fills an empty board with template name. topic
// heads the board (its name, usually); an empty topic uses the template's
// title. Ids are fixed per template, so it is meant for a new board.
func TemplateOp(name, topic string) (Op, error) {
	for _, t := range templates {
		if t.Name == name {
			topic = strings.TrimSpace(topic)
			if topic == "" {
				topic = t.Title
			}
			return t.build(topic), nil
		}
	}
	return Op{}, fmt.Errorf("no template %q (use %s)", name, strings.Join(TemplateNames(), ", "))
}

// Sizes used by the templates, in world units.
const (
	tplNoteW   = 200.0
	tplNoteH   = 120.0
	tplGap     = 20.0
	tplHeadH   = 56.0 // room for a frame's heading above its first block
	tplTitleH  = 80.0
	tplTitleGo = tplTitleH + 40 // the title's height and the space under it
)

// tpl builds one template's op.
type tpl struct{ op Op }

func tplProps(v map[string]any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func (t *tpl) block(id, typ string, x, y, w, h float64, frame string, p map[string]any) {
	t.op.Blocks = append(t.op.Blocks, Block{ID: id, Type: typ, X: x, Y: y, W: w, H: h, Frame: frame, Props: tplProps(p)})
}

func (t *tpl) title(topic string, x, y, w float64) {
	t.block("title", "text", x, y, w, tplTitleH, "", map[string]any{"text": topic, "size": "xl", "align": "left"})
}

func (t *tpl) note(id string, x, y float64, frame, text, color string, tags ...string) {
	p := map[string]any{"text": text, "color": color}
	var kept []string
	for _, tag := range tags {
		if tag != "" {
			kept = append(kept, tag)
		}
	}
	if len(kept) > 0 {
		p["tags"] = kept
	}
	t.block(id, "sticky", x, y, tplNoteW, tplNoteH, frame, p)
}

func (t *tpl) frame(id, name string, x, y, w, h float64, color string) {
	t.op.Frames = append(t.op.Frames, Frame{ID: id, Name: name, X: x, Y: y, W: w, H: h, Color: color})
}

func (t *tpl) link(from, to string) {
	t.op.Links = append(t.op.Links, Link{ID: from + "-to-" + to, From: from, To: to})
}

// column is a frame holding one column of sticky notes, tagged with tag
// (none when tag is ""). It is tall enough for rows notes. A grey column
// holds yellow notes.
func (t *tpl) column(id, name, tag, color string, x, y float64, rows int, notes ...string) {
	w := tplNoteW + 2*tplGap
	h := tplHeadH + float64(rows)*(tplNoteH+tplGap) + tplGap
	t.frame(id, name, x, y, w, h, color)
	noteColor := color
	if color == "grey" {
		noteColor = "yellow"
	}
	for i, text := range notes {
		t.note(fmt.Sprintf("%s-%d", id, i+1), x+tplGap, y+tplHeadH+float64(i)*(tplNoteH+tplGap), id, text, noteColor, tag)
	}
}

const tplColumnW = tplNoteW + 2*tplGap
const tplColumnGap = 40.0

var templates = []Template{
	{
		Name:        "mind-map",
		Title:       "Mind map",
		Description: "A topic in the middle with four branches, two ideas on each, joined by lines. Cluster by tag groups each branch.",
		build: func(topic string) Op {
			var t tpl
			cw, ch := 260.0, 100.0
			t.block("topic", "text", -cw/2, -ch/2, cw, ch, "", map[string]any{"text": topic, "size": "l", "align": "center"})
			branches := []struct {
				id, text, color string
				side, row       float64 // side -1 left, 1 right; row -1 up, 1 down
			}{
				{"branch-1", "Why it matters", "blue", 1, -1},
				{"branch-2", "What we know", "green", 1, 1},
				{"branch-3", "Open questions", "pink", -1, -1},
				{"branch-4", "Ideas", "yellow", -1, 1},
			}
			for _, b := range branches {
				bx := 260.0
				if b.side < 0 {
					bx = -260 - tplNoteW
				}
				by := b.row*160 - tplNoteH/2
				t.note(b.id, bx, by, "", b.text, b.color, b.text)
				t.link("topic", b.id)
				for j := range 2 {
					cx := bx + b.side*(tplNoteW+80)
					cy := by + (float64(j)-0.5)*(tplNoteH+tplGap)
					id := fmt.Sprintf("%s-%d", b.id, j+1)
					t.note(id, cx, cy, "", "A thought", b.color, b.text)
					t.link(b.id, id)
				}
			}
			return t.op
		},
	},
	{
		Name:        "affinity",
		Title:       "Affinity clusters",
		Description: "Unsorted ideas on the left and three theme frames to group them into. Each note is tagged with its theme; cluster by tag regroups them.",
		build: func(topic string) Op {
			var t tpl
			t.title(topic, 0, 0, 4*tplColumnW+3*tplColumnGap)
			y := tplTitleGo
			t.column("unsorted", "Unsorted", "", "grey", 0, y, 4,
				"One idea per note", "Write fast, sort later", "Another idea", "Drag me to a theme")
			for i, th := range []struct{ id, name, color string }{
				{"theme-a", "Theme A", "blue"},
				{"theme-b", "Theme B", "green"},
				{"theme-c", "Theme C", "purple"},
			} {
				x := float64(i+1) * (tplColumnW + tplColumnGap)
				t.column(th.id, th.name, th.name, th.color, x, y, 4,
					"An idea that fits "+th.name, "Another one")
			}
			return t.op
		},
	},
	{
		Name:        "retro",
		Title:       "Retro",
		Description: "Went well, didn't go well and ideas to try, as three columns, with an actions checklist to close the retro.",
		build: func(topic string) Op {
			var t tpl
			t.title(topic, 0, 0, 4*tplColumnW+3*tplColumnGap)
			y := tplTitleGo
			cols := []struct{ id, name, color, prompt string }{
				{"went-well", "Went well", "green", "What helped us?"},
				{"went-badly", "Didn't go well", "pink", "What slowed us down?"},
				{"try-next", "Ideas to try", "blue", "What would we change?"},
			}
			for i, c := range cols {
				x := float64(i) * (tplColumnW + tplColumnGap)
				t.column(c.id, c.name, c.name, c.color, x, y, 4, c.prompt)
			}
			ax := 3 * (tplColumnW + tplColumnGap)
			h := tplHeadH + 4*(tplNoteH+tplGap) + tplGap
			t.frame("actions", "Actions", ax, y, tplColumnW, h, "orange")
			t.block("actions-list", "checklist", ax+tplGap, y+tplHeadH, tplNoteW, 2*tplNoteH+tplGap, "actions", map[string]any{
				"title": "Who does what",
				"items": []map[string]any{{"text": "An action, with an owner"}, {"text": "Another action"}},
				"tags":  []string{"Actions"},
			})
			return t.op
		},
	},
	{
		Name:        "pros-cons",
		Title:       "Pros and cons",
		Description: "The decision as a heading, a pros column and a cons column, and a poll to decide.",
		build: func(topic string) Op {
			var t tpl
			w := 2*tplColumnW + tplColumnGap
			t.title(topic, 0, 0, w)
			y := tplTitleGo
			t.column("pros", "Pros", "Pros", "green", 0, y, 3, "A reason for", "Another reason for")
			t.column("cons", "Cons", "Cons", "pink", tplColumnW+tplColumnGap, y, 3, "A reason against", "Another reason against")
			py := y + tplHeadH + 3*(tplNoteH+tplGap) + tplGap + tplColumnGap
			t.block("decide", "poll", 0, py, w, 220, "", map[string]any{
				"question": "Do we go ahead?",
				"options":  []map[string]any{{"label": "Yes"}, {"label": "No"}, {"label": "Not yet: we need to know more"}},
				"tags":     []string{"Decision"},
			})
			return t.op
		},
	},
	{
		Name:        "kanban",
		Title:       "Kanban",
		Description: "To do, doing and done columns of cards. Each card is tagged with its column.",
		build: func(topic string) Op {
			var t tpl
			t.title(topic, 0, 0, 3*tplColumnW+2*tplColumnGap)
			y := tplTitleGo
			cols := []struct {
				id, name, color string
				notes           []string
			}{
				{"todo", "To do", "grey", []string{"A task", "Another task", "One more"}},
				{"doing", "Doing", "blue", []string{"In progress"}},
				{"done", "Done", "green", []string{"Finished"}},
			}
			for i, c := range cols {
				x := float64(i) * (tplColumnW + tplColumnGap)
				t.column(c.id, c.name, c.name, c.color, x, y, 5, c.notes...)
			}
			return t.op
		},
	},
	{
		Name:        "swot",
		Title:       "SWOT",
		Description: "Strengths, weaknesses, opportunities and threats in four frames, two by two.",
		build: func(topic string) Op {
			var t tpl
			fw := 2*tplNoteW + 3*tplGap
			fh := tplHeadH + 2*(tplNoteH+tplGap) + tplGap
			t.title(topic, 0, 0, 2*fw+tplColumnGap)
			y := tplTitleGo
			for i, q := range []struct{ id, name, color, prompt string }{
				{"strengths", "Strengths", "green", "What we do well"},
				{"weaknesses", "Weaknesses", "pink", "Where we fall short"},
				{"opportunities", "Opportunities", "blue", "What we could use"},
				{"threats", "Threats", "orange", "What could hurt us"},
			} {
				x := float64(i%2) * (fw + tplColumnGap)
				fy := y + float64(i/2)*(fh+tplColumnGap)
				t.frame(q.id, q.name, x, fy, fw, fh, q.color)
				t.note(q.id+"-1", x+tplGap, fy+tplHeadH, q.id, q.prompt, q.color, q.name)
			}
			return t.op
		},
	},
}
