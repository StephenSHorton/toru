package board

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Markdown is a board as an outline for reading (docs/whiteboard.md section
// 3): the board's name as the title, frames as headings, blocks as bullets
// in reading order (top to bottom, then left to right), checklists as
// "- [ ]", polls with their votes, and links as "A → B" lines.
func Markdown(b Board) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n", oneLine(b.Name))

	label := map[string]string{}
	for _, f := range b.Frames {
		label[f.ID] = f.Name
	}
	for _, x := range b.Blocks {
		label[x.ID] = blockLabel(x)
	}

	byFrame := map[string][]Block{}
	for _, x := range b.Blocks {
		byFrame[x.Frame] = append(byFrame[x.Frame], x)
	}
	section := func(blocks []Block) {
		sort.SliceStable(blocks, func(i, j int) bool {
			if blocks[i].Y != blocks[j].Y {
				return blocks[i].Y < blocks[j].Y
			}
			return blocks[i].X < blocks[j].X
		})
		for _, x := range blocks {
			writeBlock(&sb, x)
		}
	}
	if loose := byFrame[""]; len(loose) > 0 {
		sb.WriteString("\n")
		section(loose)
	}
	frames := append([]Frame(nil), b.Frames...)
	sort.SliceStable(frames, func(i, j int) bool {
		if frames[i].Y != frames[j].Y {
			return frames[i].Y < frames[j].Y
		}
		return frames[i].X < frames[j].X
	})
	for _, f := range frames {
		fmt.Fprintf(&sb, "\n## %s\n\n", oneLine(f.Name))
		section(byFrame[f.ID])
	}
	if len(b.Links) > 0 {
		sb.WriteString("\n## Links\n\n")
		for _, l := range b.Links {
			line := fmt.Sprintf("- %s → %s", label[l.From], label[l.To])
			if l.Label != "" {
				line += ": " + oneLine(l.Label)
			}
			sb.WriteString(line + "\n")
		}
	}
	return sb.String()
}

type blockProps struct {
	Text     string `json:"text"`
	Title    string `json:"title"`
	Question string `json:"question"`
	Items    []struct {
		Text string `json:"text"`
		Done bool   `json:"done"`
	} `json:"items"`
	Options []struct {
		Label string `json:"label"`
		Votes int    `json:"votes"`
	} `json:"options"`
	Tags []string `json:"tags"`
}

func blockPropsOf(x Block) blockProps {
	var p blockProps
	_ = json.Unmarshal(x.Props, &p)
	return p
}

// blockLabel is a block's name in a link line: its text, title or question,
// cut short, else its type and id.
func blockLabel(x Block) string {
	p := blockPropsOf(x)
	for _, s := range []string{p.Text, p.Title, p.Question} {
		if s = oneLine(s); s != "" {
			if r := []rune(s); len(r) > 60 {
				s = string(r[:59]) + "…"
			}
			return s
		}
	}
	return fmt.Sprintf("%s %s", x.Type, x.ID)
}

func writeBlock(sb *strings.Builder, x Block) {
	p := blockPropsOf(x)
	tags := ""
	if len(p.Tags) > 0 {
		tags = " #" + strings.Join(p.Tags, " #")
	}
	switch x.Type {
	case "checklist":
		head := oneLine(p.Title)
		if head == "" {
			head = "Checklist"
		}
		fmt.Fprintf(sb, "- %s%s\n", head, tags)
		for _, it := range p.Items {
			box := " "
			if it.Done {
				box = "x"
			}
			fmt.Fprintf(sb, "  - [%s] %s\n", box, oneLine(it.Text))
		}
	case "poll":
		fmt.Fprintf(sb, "- Poll: %s%s\n", oneLine(p.Question), tags)
		for _, o := range p.Options {
			fmt.Fprintf(sb, "  - %s (%d)\n", oneLine(o.Label), o.Votes)
		}
	default:
		fmt.Fprintf(sb, "- %s%s\n", blockLabelFull(x, p), tags)
	}
}

func blockLabelFull(x Block, p blockProps) string {
	for _, s := range []string{p.Text, p.Title, p.Question} {
		if s = oneLine(s); s != "" {
			return s
		}
	}
	return fmt.Sprintf("(%s)", x.Type)
}

// oneLine folds a text's lines into one.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
