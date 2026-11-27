package board

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// Layout kinds.
const (
	LayoutGrid    = "grid"    // rows and columns, in stacking order
	LayoutTree    = "tree"    // follows the links: roots first, each level one column (or row) further
	LayoutCluster = "cluster" // one column per tag (a block's first tag), untagged last
)

// LayoutOptions tune Layout. Zero values pick defaults.
type LayoutOptions struct {
	Gap       float64 // space between blocks (default 40)
	Columns   int     // grid: blocks per row (default about a square)
	Direction string  // tree: "right" (default) or "down"
	X, Y      *float64
}

// Point is a block's new top-left corner.
type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Layout works out new positions for the blocks ids on b (every block the
// person hasn't locked when ids is empty; a locked one named in ids is
// refused when the move is applied). The blocks start where their top-left-most corner is now,
// unless opt gives X and Y.
func Layout(b Board, ids []string, kind string, opt LayoutOptions) (map[string]Point, error) {
	byID := map[string]Block{}
	for _, x := range b.Blocks {
		byID[x.ID] = x
	}
	var blocks []Block
	if len(ids) == 0 {
		for _, x := range b.Blocks {
			if !x.Locked {
				blocks = append(blocks, x)
			}
		}
	} else {
		seen := map[string]bool{}
		for _, id := range ids {
			x, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("no block %q on the board", id)
			}
			if !seen[id] {
				seen[id] = true
				blocks = append(blocks, x)
			}
		}
	}
	if len(blocks) == 0 {
		return map[string]Point{}, nil
	}
	gap := opt.Gap
	if gap <= 0 {
		gap = 40
	}
	ox, oy := math.Inf(1), math.Inf(1)
	for _, x := range blocks {
		ox, oy = math.Min(ox, x.X), math.Min(oy, x.Y)
	}
	if opt.X != nil && opt.Y != nil {
		ox, oy = *opt.X, *opt.Y
	}
	out := map[string]Point{}
	switch kind {
	case LayoutGrid, "":
		cols := opt.Columns
		if cols <= 0 {
			cols = int(math.Ceil(math.Sqrt(float64(len(blocks)))))
		}
		cw, ch := 0.0, 0.0
		for _, x := range blocks {
			cw, ch = math.Max(cw, x.W), math.Max(ch, x.H)
		}
		for i, x := range blocks {
			out[x.ID] = Point{ox + float64(i%cols)*(cw+gap), oy + float64(i/cols)*(ch+gap)}
		}
	case LayoutCluster:
		groups := map[string][]Block{}
		var names []string
		for _, x := range blocks {
			t := firstTag(x.Props)
			if _, ok := groups[t]; !ok {
				names = append(names, t)
			}
			groups[t] = append(groups[t], x)
		}
		sort.Slice(names, func(i, j int) bool {
			if (names[i] == "") != (names[j] == "") {
				return names[j] == "" // untagged last
			}
			return strings.ToLower(names[i]) < strings.ToLower(names[j])
		})
		colX := ox
		for _, n := range names {
			y, w := oy, 0.0
			for _, x := range groups[n] {
				out[x.ID] = Point{colX, y}
				y += x.H + gap
				w = math.Max(w, x.W)
			}
			colX += w + gap*2
		}
	case LayoutTree:
		in := map[string]bool{}
		for _, x := range blocks {
			in[x.ID] = true
		}
		children := map[string][]string{}
		hasParent := map[string]bool{}
		for _, l := range b.Links {
			if in[l.From] && in[l.To] {
				children[l.From] = append(children[l.From], l.To)
				hasParent[l.To] = true
			}
		}
		level := map[string]int{}
		var order []string
		visit := func(roots []string) {
			queue := append([]string(nil), roots...)
			for _, r := range roots {
				level[r] = 0
			}
			for len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				order = append(order, id)
				for _, c := range children[id] {
					if _, done := level[c]; !done {
						level[c] = level[id] + 1
						queue = append(queue, c)
					}
				}
			}
		}
		var roots []string
		for _, x := range blocks {
			if !hasParent[x.ID] {
				roots = append(roots, x.ID)
			}
		}
		visit(roots)
		// Blocks only in cycles: start from each one not reached yet.
		for _, x := range blocks {
			if _, done := level[x.ID]; !done {
				visit([]string{x.ID})
			}
		}
		levels := [][]string{}
		for _, id := range order {
			l := level[id]
			for len(levels) <= l {
				levels = append(levels, nil)
			}
			levels[l] = append(levels[l], id)
		}
		down := opt.Direction == "down"
		along := 0.0
		for _, ids := range levels {
			across, size := 0.0, 0.0
			for _, id := range ids {
				x := byID[id]
				if down {
					out[id] = Point{ox + across, oy + along}
					across += x.W + gap
					size = math.Max(size, x.H)
				} else {
					out[id] = Point{ox + along, oy + across}
					across += x.H + gap
					size = math.Max(size, x.W)
				}
			}
			along += size + gap*2
		}
	default:
		return nil, fmt.Errorf("no layout %q (use grid, tree or cluster)", kind)
	}
	return out, nil
}

// firstTag is a block's first tag (props.tags[0]), or "".
func firstTag(props json.RawMessage) string {
	var p struct {
		Tags []string `json:"tags"`
	}
	if json.Unmarshal(props, &p) == nil && len(p.Tags) > 0 {
		return p.Tags[0]
	}
	return ""
}
