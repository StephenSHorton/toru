package board

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Source is who made a change: "person" for the person at the board, or
// "agent:<name>" for an agent (its MCP client's name).
type Source string

// Person is the person at the board.
const Person Source = "person"

const agentPrefix = "agent:"

var agentNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._@()/+-]{0,63}$`)

// Agent is the Source for an agent named name.
func Agent(name string) Source { return Source(agentPrefix + strings.TrimSpace(name)) }

// AgentNamed is the Source for an agent whose name came from outside (an
// MCP client's name): characters a source can't hold become "-", it is cut
// to 64 characters, and an empty name becomes "unknown".
func AgentNamed(raw string) Source {
	var b strings.Builder
	n := 0
	for _, r := range strings.TrimSpace(raw) {
		if n == 64 {
			break
		}
		ok := r < 0x80 && (r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" ._@()/+-", r))
		if !ok {
			r = '-'
		}
		b.WriteRune(r)
		n++
	}
	name := strings.TrimSpace(b.String())
	// The first character must be a letter or digit.
	name = strings.TrimLeft(name, " ._@()/+-")
	if name == "" {
		name = "unknown"
	}
	return Agent(name)
}

// Check reports whether s is "person" or "agent:<name>".
func (s Source) Check() error {
	if s == Person {
		return nil
	}
	if name, ok := strings.CutPrefix(string(s), agentPrefix); ok && agentNamePattern.MatchString(name) {
		return nil
	}
	return fmt.Errorf("bad source %q: want \"person\" or \"agent:<name>\"", string(s))
}

// IsAgent reports whether an agent made the change.
func (s Source) IsAgent() bool { return strings.HasPrefix(string(s), agentPrefix) }

// Kinds of op.
const (
	KindChange = "change" // a change someone made
	KindUndo   = "undo"   // undoing an earlier change
	KindRedo   = "redo"   // redoing an undone change
)

// Op is one change to a board, applied whole or not at all.
//
// Applying an op: first everything in Delete goes, then the frames, blocks,
// strokes and links are put (an item whose id is on the board replaces it,
// a new one is added; an empty id gets a new one), then the board takes
// Name if it is set.
//
// Deleting a block or frame also deletes the links to it, and the blocks
// and strokes in a deleted frame stay where they are, out of the frame.
//
// Pos places put items in their list's stacking order (0 is the back).
// Without it a replaced item keeps its place and a new one goes on top.
//
// Apply returns the op as applied: every item it touched in its final
// state (with who made and changed it, and where it sits in Pos) and every
// id it removed. Sending that op to another copy of the board makes the
// same change, which is how the board window follows.
type Op struct {
	// Rev, At and Kind are set by Apply.
	Rev  int       `json:"rev,omitempty"`
	At   time.Time `json:"at,omitzero"`
	Kind string    `json:"kind,omitempty"`

	Source Source         `json:"source"`
	Name   *string        `json:"name,omitempty"`
	Delete []string       `json:"delete,omitempty"`
	Frames []Frame        `json:"frames,omitempty"`
	Blocks []Block        `json:"blocks,omitempty"`
	Ink    []Stroke       `json:"ink,omitempty"`
	Links  []Link         `json:"links,omitempty"`
	Pos    map[string]int `json:"pos,omitempty"`
	// Types puts custom block type definitions on the board (BlockDef):
	// a new type is added, a newer version of one the board has (same
	// Origin) replaces it. DropTypes takes types off; a type a block still
	// uses can't go. Undo uses both to put a board's types back.
	Types     []BlockDef `json:"types,omitempty"`
	DropTypes []string   `json:"dropTypes,omitempty"`
}

// empty reports whether the op changes nothing.
func (o *Op) empty() bool {
	return o.Name == nil && len(o.Delete) == 0 && len(o.Frames) == 0 &&
		len(o.Blocks) == 0 && len(o.Ink) == 0 && len(o.Links) == 0 &&
		len(o.Types) == 0 && len(o.DropTypes) == 0
}

// ErrNothingToUndo and ErrNothingToRedo are returned by Undo and Redo when
// their history is empty.
var (
	ErrNothingToUndo = errors.New("nothing to undo")
	ErrNothingToRedo = errors.New("nothing to redo")
)

// MaxHistory is how many changes a board's undo history keeps.
const MaxHistory = 500

// now is the clock, a var for tests.
var now = func() time.Time { return time.Now().UTC() }

// entry is one step of the undo history: the change as applied and the op
// that takes it back.
type entry struct {
	forward, inverse Op
}

// Doc is a board being edited, with its undo and redo history. A Doc is
// not safe for use by several goroutines; the Store locks around it.
type Doc struct {
	b          Board
	undo, redo []entry
}

// NewDoc starts editing b, with an empty history.
func NewDoc(b Board) *Doc {
	b.fill()
	return &Doc{b: b}
}

// Board is a copy of the board as it stands.
func (d *Doc) Board() Board { return clone(d.b) }

// CanUndo and CanRedo report whether Undo and Redo have a step to take.
func (d *Doc) CanUndo() bool { return len(d.undo) > 0 }
func (d *Doc) CanRedo() bool { return len(d.redo) > 0 }

// Apply makes a change and records it for undo. It returns the op as
// applied (see Op). On an error the board is unchanged.
func (d *Doc) Apply(op Op) (Op, error) {
	if op.Kind != "" && op.Kind != KindChange {
		return Op{}, fmt.Errorf("an op of kind %q can't be sent: use undo or redo", op.Kind)
	}
	fwd, inv, err := d.apply(op, KindChange, false)
	if err != nil {
		return Op{}, err
	}
	d.undo = append(d.undo, entry{fwd, inv})
	if len(d.undo) > MaxHistory {
		d.undo = append([]entry(nil), d.undo[len(d.undo)-MaxHistory:]...)
	}
	d.redo = nil
	return fwd, nil
}

// Undo takes back the latest change still in the history, whoever made it.
// src is who asked. It returns the op that did it.
func (d *Doc) Undo(src Source) (Op, error) {
	if len(d.undo) == 0 {
		return Op{}, ErrNothingToUndo
	}
	e := d.undo[len(d.undo)-1]
	op := e.inverse
	op.Source = src
	fwd, _, err := d.apply(op, KindUndo, true)
	if err != nil {
		return Op{}, err
	}
	d.undo = d.undo[:len(d.undo)-1]
	d.redo = append(d.redo, e)
	return fwd, nil
}

// Redo makes the latest undone change again. src is who asked.
func (d *Doc) Redo(src Source) (Op, error) {
	if len(d.redo) == 0 {
		return Op{}, ErrNothingToRedo
	}
	e := d.redo[len(d.redo)-1]
	op := e.forward
	op.Source = src
	fwd, _, err := d.apply(op, KindRedo, true)
	if err != nil {
		return Op{}, err
	}
	d.redo = d.redo[:len(d.redo)-1]
	d.undo = append(d.undo, e)
	return fwd, nil
}

// apply makes op on a copy of the board and, if it is valid, keeps it. It
// returns the op as applied and its inverse. restore keeps the items' Meta
// as sent (undo and redo put back exact earlier states); otherwise Meta is
// set from op.Source.
func (d *Doc) apply(op Op, kind string, restore bool) (fwd, inv Op, err error) {
	if err := op.Source.Check(); err != nil {
		return Op{}, Op{}, err
	}
	if op.empty() {
		return Op{}, Op{}, errors.New("the change is empty")
	}
	before := d.b
	a := applier{b: shallow(before), src: op.Source, restore: restore, kinds: kindsOf(before)}
	if err := a.run(op); err != nil {
		return Op{}, Op{}, err
	}
	t := now()
	a.b.Rev = before.Rev + 1
	a.b.UpdatedAt = t

	inv = restoreOp(before, a.touched)
	fwd = restoreOp(a.b, a.touched)
	inv.Types, inv.DropTypes = restoreTypes(before, a.touchedTypes)
	fwd.Types, fwd.DropTypes = restoreTypes(a.b, a.touchedTypes)
	if op.Name != nil {
		n, old := a.b.Name, before.Name
		fwd.Name, inv.Name = &n, &old
	}
	if fwd.empty() {
		return Op{}, Op{}, errors.New("the change changes nothing")
	}
	fwd.Rev, fwd.At, fwd.Kind, fwd.Source = a.b.Rev, t, kind, op.Source
	d.b = a.b
	return fwd, inv, nil
}

// applier makes one op on a board.
type applier struct {
	b       Board
	src     Source
	restore bool
	kinds   map[string]string // every id on the board, to its kind
	touched []string          // ids the op deleted, put or changed, in order
	seen    map[string]bool
	// touchedTypes are the custom block types the op put or dropped.
	touchedTypes []string
}

func (a *applier) touch(id string) {
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	if !a.seen[id] {
		a.seen[id] = true
		a.touched = append(a.touched, id)
	}
}

func (a *applier) run(op Op) error {
	if err := a.del(op.Delete); err != nil {
		return err
	}
	if err := a.types(op.Types, op.DropTypes); err != nil {
		return err
	}
	var err error
	if a.b.Frames, err = put(a, "frame", "frm", a.b.Frames, op.Frames, op.Pos, frameItem); err != nil {
		return err
	}
	if a.b.Blocks, err = put(a, "block", "blk", a.b.Blocks, op.Blocks, op.Pos, blockItem); err != nil {
		return err
	}
	if a.b.Ink, err = put(a, "stroke", "ink", a.b.Ink, op.Ink, op.Pos, strokeItem); err != nil {
		return err
	}
	if a.b.Links, err = put(a, "link", "lnk", a.b.Links, op.Links, op.Pos, linkItem); err != nil {
		return err
	}
	if n := a.b.Len(); n > MaxItems {
		return fmt.Errorf("the board would have %d items, more than %d", n, MaxItems)
	}
	if op.Name != nil {
		name := strings.TrimSpace(*op.Name)
		if err := checkName(name, "board"); err != nil {
			return err
		}
		a.b.Name = name
	}
	if len(op.DropTypes) > 0 && !a.restore {
		for _, t := range op.DropTypes {
			for _, x := range a.b.Blocks {
				if x.Type == t {
					return fmt.Errorf("block type %q is still used by block %q", t, x.ID)
				}
			}
		}
	}
	return a.b.checkRefs(a.kinds)
}

// types puts and drops the board's custom block types. Outside undo and
// redo, a type the board has can only be replaced by a newer version of
// the same block (same Origin); the same version is left as it is.
func (a *applier) types(put []BlockDef, drop []string) error {
	if len(put) == 0 && len(drop) == 0 {
		return nil
	}
	list := append([]BlockDef(nil), a.b.Types...)
	find := func(t string) int {
		for i := range list {
			if list[i].Type == t {
				return i
			}
		}
		return -1
	}
	touched := map[string]bool{}
	touch := func(t string) error {
		if touched[t] {
			return fmt.Errorf("the change puts or drops block type %q twice", t)
		}
		touched[t] = true
		a.touchedTypes = append(a.touchedTypes, t)
		return nil
	}
	for _, t := range drop {
		i := find(t)
		if i < 0 {
			return fmt.Errorf("the board has no custom block type %q", t)
		}
		if err := touch(t); err != nil {
			return err
		}
		list = append(list[:i], list[i+1:]...)
	}
	for _, d := range put {
		d := d
		if err := d.Check(); err != nil {
			return err
		}
		i := find(d.Type)
		if i >= 0 && !a.restore {
			old := list[i]
			if old.Origin != d.Origin {
				return fmt.Errorf("this board already has a different block type called %q: give yours another name", d.Type)
			}
			if d.Version < old.Version {
				return fmt.Errorf("this board has a newer version of block type %q (%d, not %d)", d.Type, old.Version, d.Version)
			}
			if d.Version == old.Version {
				continue
			}
		}
		if err := touch(d.Type); err != nil {
			return err
		}
		if i >= 0 {
			list[i] = d
		} else {
			list = append(list, d)
		}
	}
	if len(list) > MaxBoardTypes {
		return fmt.Errorf("the board would have more than %d custom block types", MaxBoardTypes)
	}
	if len(list) == 0 {
		list = nil
	}
	a.b.Types = list
	return nil
}

// restoreTypes is what brings the named custom types to their state in b:
// the ones b has, put back, and the rest dropped.
func restoreTypes(b Board, names []string) (put []BlockDef, drop []string) {
	for _, t := range names {
		if d := b.BoardType(t); d != nil {
			put = append(put, *d)
		} else {
			drop = append(drop, t)
		}
	}
	return put, drop
}

// del removes ids, the links to them, and empties the frames they were.
func (a *applier) del(ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	gone := map[string]bool{}
	for _, id := range ids {
		if _, ok := a.kinds[id]; !ok {
			return fmt.Errorf("nothing on the board has the id %q", id)
		}
		gone[id] = true
	}
	for _, l := range a.b.Links {
		if gone[l.From] || gone[l.To] {
			gone[l.ID] = true
		}
	}
	// Keep the op's order, then the links it took with it.
	for _, id := range ids {
		a.touch(id)
	}
	for _, l := range a.b.Links {
		if gone[l.ID] {
			a.touch(l.ID)
		}
	}
	for id := range gone {
		delete(a.kinds, id)
	}
	a.b.Frames = keep(a.b.Frames, gone, frameItem.id)
	a.b.Blocks = keep(a.b.Blocks, gone, blockItem.id)
	a.b.Ink = keep(a.b.Ink, gone, strokeItem.id)
	a.b.Links = keep(a.b.Links, gone, linkItem.id)
	for i := range a.b.Blocks {
		if x := &a.b.Blocks[i]; gone[x.Frame] {
			x.Frame = ""
			a.changed(&x.Meta, x.ID)
		}
	}
	for i := range a.b.Ink {
		if x := &a.b.Ink[i]; gone[x.Frame] {
			x.Frame = ""
			a.changed(&x.Meta, x.ID)
		}
	}
	return nil
}

// changed marks an item the op changed as a side effect.
func (a *applier) changed(m *Meta, id string) {
	if !a.restore {
		m.UpdatedBy = a.src
	}
	a.touch(id)
}

func keep[T any](list []T, gone map[string]bool, id func(*T) string) []T {
	out := make([]T, 0, len(list))
	for i := range list {
		if !gone[id(&list[i])] {
			out = append(out, list[i])
		}
	}
	return out
}

// itemOps is how put reaches into one kind of item.
type itemOps[T any] struct {
	id    func(*T) string
	setID func(*T, string)
	meta  func(*T) *Meta
	check func(*T) error
	clone func(T) T
}

var (
	frameItem = itemOps[Frame]{
		id: func(x *Frame) string { return x.ID }, setID: func(x *Frame, id string) { x.ID = id },
		meta: func(x *Frame) *Meta { return &x.Meta }, check: (*Frame).check,
		clone: func(x Frame) Frame { return x },
	}
	blockItem = itemOps[Block]{
		id: func(x *Block) string { return x.ID }, setID: func(x *Block, id string) { x.ID = id },
		meta: func(x *Block) *Meta { return &x.Meta }, check: (*Block).check,
		clone: func(x Block) Block { x.Props = cloneRaw(x.Props); return x },
	}
	strokeItem = itemOps[Stroke]{
		id: func(x *Stroke) string { return x.ID }, setID: func(x *Stroke, id string) { x.ID = id },
		meta: func(x *Stroke) *Meta { return &x.Meta }, check: (*Stroke).check,
		clone: func(x Stroke) Stroke { x.Points = append([]float64(nil), x.Points...); return x },
	}
	linkItem = itemOps[Link]{
		id: func(x *Link) string { return x.ID }, setID: func(x *Link, id string) { x.ID = id },
		meta: func(x *Link) *Meta { return &x.Meta }, check: (*Link).check,
		clone: func(x Link) Link { x.Props = cloneRaw(x.Props); return x },
	}
)

func cloneRaw(r json.RawMessage) json.RawMessage {
	if r == nil {
		return nil
	}
	return append(json.RawMessage(nil), r...)
}

// put adds or replaces items in list. kind names them in errors, prefix
// starts the ids it makes.
func put[T any](a *applier, kind, prefix string, list, items []T, pos map[string]int, ops itemOps[T]) ([]T, error) {
	if len(items) == 0 {
		return list, nil
	}
	index := make(map[string]int, len(list))
	for i := range list {
		index[ops.id(&list[i])] = i
	}
	type placed struct {
		item T
		at   int
	}
	var moved []placed
	moving := map[string]bool{}
	inOp := map[string]bool{}
	for _, in := range items {
		x := ops.clone(in)
		id := ops.id(&x)
		if id == "" {
			id = a.newID(prefix)
			ops.setID(&x, id)
		}
		if inOp[id] {
			return nil, fmt.Errorf("the change puts %s %q twice", kind, id)
		}
		inOp[id] = true
		if k, ok := a.kinds[id]; ok && k != kind {
			return nil, fmt.Errorf("id %q is already a %s, not a %s", id, k, kind)
		}
		i, exists := index[id]
		m := ops.meta(&x)
		if !a.restore {
			if exists {
				m.CreatedBy = ops.meta(&list[i]).CreatedBy
			} else {
				m.CreatedBy = a.src
			}
			m.UpdatedBy = a.src
		}
		if err := ops.check(&x); err != nil {
			return nil, err
		}
		a.kinds[id] = kind
		a.touch(id)
		if p, ok := pos[id]; ok {
			moving[id] = exists
			moved = append(moved, placed{x, p})
			continue
		}
		if exists {
			list[i] = x
		} else {
			list = append(list, x)
			index[id] = len(list) - 1
		}
	}
	if len(moved) == 0 {
		return list, nil
	}
	// Take the placed items out, then put them in at their places, lowest
	// first, so each lands exactly where Pos says.
	out := make([]T, 0, len(list)+len(moved))
	for i := range list {
		if _, ok := moving[ops.id(&list[i])]; !ok {
			out = append(out, list[i])
		}
	}
	sort.SliceStable(moved, func(i, j int) bool { return moved[i].at < moved[j].at })
	for _, m := range moved {
		at := min(max(m.at, 0), len(out))
		out = append(out, m.item)
		copy(out[at+1:], out[at:])
		out[at] = m.item
	}
	return out, nil
}

// newID is a fresh item id, not used on the board.
func (a *applier) newID(prefix string) string {
	for {
		id := fmt.Sprintf("%s-%s", prefix, randHex(6))
		if _, used := a.kinds[id]; !used {
			return id
		}
	}
}

// restoreOp is the op that brings the listed ids to their state in b: the
// ones on b are put back exactly, at their places, and the rest deleted.
func restoreOp(b Board, ids []string) Op {
	op := Op{Pos: map[string]int{}}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	found := map[string]bool{}
	pick := func(id string, at int) bool {
		if !want[id] {
			return false
		}
		found[id] = true
		op.Pos[id] = at
		return true
	}
	for i, x := range b.Frames {
		if pick(x.ID, i) {
			op.Frames = append(op.Frames, frameItem.clone(x))
		}
	}
	for i, x := range b.Blocks {
		if pick(x.ID, i) {
			op.Blocks = append(op.Blocks, blockItem.clone(x))
		}
	}
	for i, x := range b.Ink {
		if pick(x.ID, i) {
			op.Ink = append(op.Ink, strokeItem.clone(x))
		}
	}
	for i, x := range b.Links {
		if pick(x.ID, i) {
			op.Links = append(op.Links, linkItem.clone(x))
		}
	}
	for _, id := range ids {
		if !found[id] {
			op.Delete = append(op.Delete, id)
		}
	}
	if len(op.Pos) == 0 {
		op.Pos = nil
	}
	return op
}

// kindsOf maps every id on b to its kind.
func kindsOf(b Board) map[string]string {
	k := make(map[string]string, b.Len())
	for _, x := range b.Frames {
		k[x.ID] = "frame"
	}
	for _, x := range b.Blocks {
		k[x.ID] = "block"
	}
	for _, x := range b.Ink {
		k[x.ID] = "stroke"
	}
	for _, x := range b.Links {
		k[x.ID] = "link"
	}
	return k
}

// Len is how many items are on the board.
func (b *Board) Len() int { return len(b.Frames) + len(b.Blocks) + len(b.Links) + len(b.Ink) }

// shallow copies b's lists (not the items' props or points, which are
// never changed in place).
func shallow(b Board) Board {
	b.Frames = append([]Frame{}, b.Frames...)
	b.Blocks = append([]Block{}, b.Blocks...)
	b.Ink = append([]Stroke{}, b.Ink...)
	b.Links = append([]Link{}, b.Links...)
	if b.Types != nil {
		b.Types = append([]BlockDef{}, b.Types...)
	}
	return b
}

// clone is a deep copy of b, safe to hand out.
func clone(b Board) Board {
	b = shallow(b)
	for i := range b.Blocks {
		b.Blocks[i] = blockItem.clone(b.Blocks[i])
	}
	for i := range b.Ink {
		b.Ink[i] = strokeItem.clone(b.Ink[i])
	}
	for i := range b.Links {
		b.Links[i] = linkItem.clone(b.Links[i])
	}
	return b
}
