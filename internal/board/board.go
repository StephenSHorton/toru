// Package board is the whiteboard's document (docs/whiteboard.md, sections
// 2 and 5): blocks, their positions, the links between them, freehand ink
// and frames, kept as one JSON file per board in
// %AppData%\toru\boards\<id>.toruboard.
//
// Every change to a board is an Op tagged with who made it ("person" or
// "agent:<name>"). Ops are applied whole or not at all, and each one
// records its exact inverse, so undo and redo step through them (op.go).
// The Store (store.go) keeps the files and one undo history per open board,
// shared by the board window and agents.
//
// A block's props are free JSON. Their shape belongs to the block catalog,
// which lives in the front end and is mirrored as JSON Schema; this package
// checks only that props are a JSON object and hands them to ValidateProps,
// which the catalog replaces.
package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Format is the "format" field of every .toruboard file.
const Format = "toruboard"

// Version is the board file format's version. A file with a higher version
// was written by a newer Toru and is refused rather than half read.
const Version = 1

// Ext is a board file's extension.
const Ext = ".toruboard"

// Board is one whiteboard, exactly as it is saved.
//
// Coordinates are world units (CSS pixels at zoom 1), x to the right and y
// down. The order of Blocks, Ink and Frames is their stacking order, first
// at the back.
type Board struct {
	Format    string    `json:"format"`
	Version   int       `json:"version"`
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Rev counts the ops applied to the board since it was made. Each op
	// carries the Rev it produced, so a window can tell what it has seen.
	Rev    int      `json:"rev"`
	Frames []Frame  `json:"frames"`
	Blocks []Block  `json:"blocks"`
	Links  []Link   `json:"links"`
	Ink    []Stroke `json:"ink"`
	// Types are the custom block types the board's blocks use (BlockDef),
	// copied in when a block of one is first placed, so the board renders
	// on any Toru, in shared boards and in exports. Left out when none.
	Types []BlockDef `json:"types,omitempty"`
}

// Meta says who made an item and who last changed it, as Sources. Apply
// sets it; whatever a caller sends is replaced.
type Meta struct {
	CreatedBy Source `json:"createdBy,omitempty"`
	UpdatedBy Source `json:"updatedBy,omitempty"`
}

// Block is one piece of live UI on the board: a sticky, a poll, a
// checklist... Type names its block catalog entry and Props are that
// entry's props.
type Block struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	X        float64         `json:"x"`
	Y        float64         `json:"y"`
	W        float64         `json:"w"`
	H        float64         `json:"h"`
	Rotation float64         `json:"rotation,omitempty"` // degrees, clockwise
	Frame    string          `json:"frame,omitempty"`    // the frame it sits in, if any
	Group    string          `json:"group,omitempty"`    // blocks with the same group move together
	Props    json.RawMessage `json:"props"`
	Meta
}

// Link is a line from one block or frame to another.
type Link struct {
	ID       string          `json:"id"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	FromPort string          `json:"fromPort,omitempty"`
	ToPort   string          `json:"toPort,omitempty"`
	Label    string          `json:"label,omitempty"`
	Props    json.RawMessage `json:"props,omitempty"` // style: arrow heads, dash, colour
	Meta
}

// Stroke is one piece of ink: a freehand line, or a shape drawn on the ink
// layer. Points is flat: x, y, pressure for each point (pressure 0..1; 0.5
// when the device has none).
//
// Kind is empty for a freehand line. A shape (ShapeKinds) has exactly two
// points: a box's opposite corners (rect, ellipse, text, emoji) or a line's
// two ends (line, arrow), so it stays a shape to select, move and reshape.
// Fill is a rect's or ellipse's inside colour, empty for none. Text is a
// text label's words or an emoji. Highlight marks a freehand highlighter
// stroke, drawn see-through and under the other ink.
type Stroke struct {
	ID        string    `json:"id"`
	Points    []float64 `json:"points"`
	Color     string    `json:"color"`
	Width     float64   `json:"width"`
	Frame     string    `json:"frame,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Fill      string    `json:"fill,omitempty"`
	Text      string    `json:"text,omitempty"`
	Highlight bool      `json:"highlight,omitempty"`
	Meta
}

// ShapeKinds are the shapes a Stroke can be (Stroke.Kind).
var ShapeKinds = []string{"rect", "ellipse", "line", "arrow", "text", "emoji"}

// IsShape reports whether a stroke is a shape rather than a freehand line.
func (s *Stroke) IsShape() bool { return s.Kind != "" }

// Frame is a named region that holds blocks and ink, like a section of the
// board.
type Frame struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	W     float64 `json:"w"`
	H     float64 `json:"h"`
	Color string  `json:"color,omitempty"`
	Meta
}

// Limits.
const (
	MaxNameLen       = 120       // a board or frame name, in characters
	MaxLabelLen      = 2000      // a link label, in characters
	MaxItems         = 20000     // blocks, links, strokes and frames together
	MaxPropsBytes    = 256 << 10 // one block's or link's props
	MaxStrokeFloats  = 3 * 20000 // one stroke's points (x, y, pressure each)
	maxCoord         = 1e7
	maxFileBytes     = 128 << 20
	maxStrokeWidth   = 1000
	maxColorLen      = 64
	MaxShapeText     = 2000 // a text label's words, in characters
	maxEmojiBytes    = 64
	maxTypeOrPortLen = 40
)

var (
	idPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	typePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	portPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
)

// ValidateProps checks a block's props against the block catalog's schema
// for its type. Until the catalog's JSON Schema is wired in, every type and
// every props object is accepted. It is a var so the catalog (and tests)
// can replace it; props is always a JSON object when it is called.
var ValidateProps = func(blockType string, props json.RawMessage) error { return nil }

// New is an empty board.
func New(id, name string, now time.Time) Board {
	return Board{
		Format:    Format,
		Version:   Version,
		ID:        id,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
		Frames:    []Frame{},
		Blocks:    []Block{},
		Links:     []Link{},
		Ink:       []Stroke{},
	}
}

// Encode is a board as its file's bytes.
func Encode(b Board) ([]byte, error) {
	b.fill()
	// No HTML escaping: props keep "&" and "<" as written, so a board
	// reads back byte for byte.
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(b); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
}

// Decode reads a .toruboard file's bytes and checks the whole board. A file
// that fails is reported, never repaired.
func Decode(data []byte) (Board, error) {
	var head struct {
		Format  string `json:"format"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return Board{}, fmt.Errorf("not a Toru board file: %w", err)
	}
	if head.Format != Format {
		return Board{}, errors.New("not a Toru board file (no \"format\": \"toruboard\")")
	}
	if head.Version > Version {
		return Board{}, fmt.Errorf("this board was made with a newer Toru (format version %d)", head.Version)
	}
	if head.Version != Version {
		return Board{}, fmt.Errorf("unsupported board format version %d", head.Version)
	}
	var b Board
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&b); err != nil {
		return Board{}, fmt.Errorf("not a Toru board file: %w", err)
	}
	if dec.More() {
		return Board{}, errors.New("not a Toru board file: extra data after the board")
	}
	b.fill()
	b.dropBadTypes()
	if err := b.Validate(); err != nil {
		return Board{}, err
	}
	return b, nil
}

// dropBadTypes leaves out custom block types a file carries that don't
// pass their check (or repeat a name), so a board from a newer or broken
// Toru still opens: its blocks of those types show as plain placeholder
// cards.
func (b *Board) dropBadTypes() {
	if len(b.Types) == 0 {
		b.Types = nil
		return
	}
	seen := map[string]bool{}
	out := make([]BlockDef, 0, len(b.Types))
	for _, d := range b.Types {
		if seen[d.Type] || d.Check() != nil {
			continue
		}
		seen[d.Type] = true
		out = append(out, d)
	}
	if len(out) > MaxBoardTypes {
		out = out[:MaxBoardTypes]
	}
	if len(out) == 0 {
		out = nil
	}
	b.Types = out
}

// fill gives a board's nil lists their empty value, so they save as [].
func (b *Board) fill() {
	if b.Frames == nil {
		b.Frames = []Frame{}
	}
	if b.Blocks == nil {
		b.Blocks = []Block{}
	}
	if b.Links == nil {
		b.Links = []Link{}
	}
	if b.Ink == nil {
		b.Ink = []Stroke{}
	}
}

// Validate checks a whole board: its header, every item, that ids are
// unique across the board, and that every frame a block or stroke sits in
// and every end of a link exists.
func (b *Board) Validate() error {
	if b.Format != Format || b.Version != Version {
		return fmt.Errorf("not a version %d Toru board", Version)
	}
	if !idPattern.MatchString(b.ID) {
		return fmt.Errorf("bad board id %q", b.ID)
	}
	if err := checkName(b.Name, "board"); err != nil {
		return err
	}
	if b.Rev < 0 {
		return errors.New("bad board rev")
	}
	if n := len(b.Frames) + len(b.Blocks) + len(b.Links) + len(b.Ink); n > MaxItems {
		return fmt.Errorf("the board has %d items, more than %d", n, MaxItems)
	}
	kinds := map[string]string{}
	claim := func(id, kind string) error {
		if k, ok := kinds[id]; ok {
			return fmt.Errorf("id %q is used twice (a %s and a %s)", id, k, kind)
		}
		kinds[id] = kind
		return nil
	}
	for i := range b.Frames {
		if err := b.Frames[i].check(); err != nil {
			return err
		}
		if err := claim(b.Frames[i].ID, "frame"); err != nil {
			return err
		}
	}
	for i := range b.Blocks {
		if err := b.Blocks[i].check(); err != nil {
			return err
		}
		if err := claim(b.Blocks[i].ID, "block"); err != nil {
			return err
		}
	}
	for i := range b.Ink {
		if err := b.Ink[i].check(); err != nil {
			return err
		}
		if err := claim(b.Ink[i].ID, "stroke"); err != nil {
			return err
		}
	}
	for i := range b.Links {
		if err := b.Links[i].check(); err != nil {
			return err
		}
		if err := claim(b.Links[i].ID, "link"); err != nil {
			return err
		}
	}
	if err := b.checkTypes(); err != nil {
		return err
	}
	return b.checkRefs(kinds)
}

// checkTypes checks the board's custom block types.
func (b *Board) checkTypes() error {
	if len(b.Types) > MaxBoardTypes {
		return fmt.Errorf("the board has %d custom block types, more than %d", len(b.Types), MaxBoardTypes)
	}
	seen := map[string]bool{}
	for i := range b.Types {
		if err := b.Types[i].Check(); err != nil {
			return err
		}
		if seen[b.Types[i].Type] {
			return fmt.Errorf("custom block type %q is on the board twice", b.Types[i].Type)
		}
		seen[b.Types[i].Type] = true
	}
	return nil
}

// checkRefs checks that frames and link ends exist. kinds maps every id on
// the board to its kind.
func (b *Board) checkRefs(kinds map[string]string) error {
	inFrame := func(what, id, frame string) error {
		if frame != "" && kinds[frame] != "frame" {
			return fmt.Errorf("%s %q sits in frame %q, which isn't on the board", what, id, frame)
		}
		return nil
	}
	for _, x := range b.Blocks {
		if err := inFrame("block", x.ID, x.Frame); err != nil {
			return err
		}
	}
	for _, x := range b.Ink {
		if err := inFrame("stroke", x.ID, x.Frame); err != nil {
			return err
		}
	}
	for _, l := range b.Links {
		for _, end := range []string{l.From, l.To} {
			if k := kinds[end]; k != "block" && k != "frame" {
				return fmt.Errorf("link %q ends at %q, which isn't a block or frame on the board", l.ID, end)
			}
		}
	}
	return nil
}

func checkName(name, what string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("give the %s a name", what)
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("the %s's name has spaces around it", what)
	}
	if len([]rune(name)) > MaxNameLen {
		return fmt.Errorf("that %s name is too long (%d characters at most)", what, MaxNameLen)
	}
	if strings.ContainsFunc(name, isControl) {
		return fmt.Errorf("the %s's name has a control character", what)
	}
	return nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func checkNums(what, id string, v ...float64) error {
	for _, f := range v {
		if math.IsNaN(f) || math.IsInf(f, 0) || math.Abs(f) > maxCoord {
			return fmt.Errorf("%s %q has a bad position or size", what, id)
		}
	}
	return nil
}

func checkBox(what, id string, x, y, w, h float64) error {
	if err := checkNums(what, id, x, y, w, h); err != nil {
		return err
	}
	if w < 0 || h < 0 {
		return fmt.Errorf("%s %q has a negative size", what, id)
	}
	return nil
}

// checkObject checks props are a JSON object and stores them compact, so
// a board reads back from its file exactly as it was.
func checkObject(what, id string, raw *json.RawMessage, required bool) error {
	t := bytes.TrimSpace(*raw)
	if len(t) == 0 || string(t) == "null" {
		if required {
			return fmt.Errorf("%s %q has no props", what, id)
		}
		*raw = nil
		return nil
	}
	if t[0] != '{' || !json.Valid(t) {
		return fmt.Errorf("%s %q's props must be a JSON object", what, id)
	}
	var c bytes.Buffer
	if err := json.Compact(&c, t); err != nil {
		return fmt.Errorf("%s %q's props must be a JSON object", what, id)
	}
	if c.Len() > MaxPropsBytes {
		return fmt.Errorf("%s %q's props are too big (%d KB at most)", what, id, MaxPropsBytes>>10)
	}
	*raw = c.Bytes()
	return nil
}

func checkColor(what, id, c string) error {
	if len(c) > maxColorLen || strings.ContainsFunc(c, isControl) {
		return fmt.Errorf("%s %q has a bad colour", what, id)
	}
	return nil
}

func checkID(what, id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("bad %s id %q", what, id)
	}
	return nil
}

func (x *Block) check() error {
	if err := checkID("block", x.ID); err != nil {
		return err
	}
	if !typePattern.MatchString(x.Type) {
		return fmt.Errorf("block %q has a bad type %q", x.ID, x.Type)
	}
	if err := checkBox("block", x.ID, x.X, x.Y, x.W, x.H); err != nil {
		return err
	}
	if err := checkNums("block", x.ID, x.Rotation); err != nil {
		return err
	}
	if x.Group != "" && !idPattern.MatchString(x.Group) {
		return fmt.Errorf("block %q has a bad group %q", x.ID, x.Group)
	}
	if err := checkObject("block", x.ID, &x.Props, true); err != nil {
		return err
	}
	if err := ValidateProps(x.Type, x.Props); err != nil {
		return fmt.Errorf("block %q (%s): %w", x.ID, x.Type, err)
	}
	return x.Meta.check("block", x.ID)
}

func (l *Link) check() error {
	if err := checkID("link", l.ID); err != nil {
		return err
	}
	if l.From == l.To {
		return fmt.Errorf("link %q goes from %q to itself", l.ID, l.From)
	}
	for _, p := range []string{l.FromPort, l.ToPort} {
		if p != "" && !portPattern.MatchString(p) {
			return fmt.Errorf("link %q has a bad port %q", l.ID, p)
		}
	}
	if len([]rune(l.Label)) > MaxLabelLen {
		return fmt.Errorf("link %q's label is too long", l.ID)
	}
	if err := checkObject("link", l.ID, &l.Props, false); err != nil {
		return err
	}
	return l.Meta.check("link", l.ID)
}

func (s *Stroke) check() error {
	if err := checkID("stroke", s.ID); err != nil {
		return err
	}
	if len(s.Points) == 0 || len(s.Points)%3 != 0 {
		return fmt.Errorf("stroke %q needs points as x, y, pressure triples", s.ID)
	}
	if len(s.Points) > MaxStrokeFloats {
		return fmt.Errorf("stroke %q has too many points (%d at most)", s.ID, MaxStrokeFloats/3)
	}
	if err := checkNums("stroke", s.ID, s.Points...); err != nil {
		return err
	}
	for i := 2; i < len(s.Points); i += 3 {
		if p := s.Points[i]; p < 0 || p > 1 {
			return fmt.Errorf("stroke %q has a pressure outside 0..1", s.ID)
		}
	}
	if err := checkNums("stroke", s.ID, s.Width); err != nil {
		return err
	}
	if s.Width <= 0 || s.Width > maxStrokeWidth {
		return fmt.Errorf("stroke %q has a bad width", s.ID)
	}
	if err := checkColor("stroke", s.ID, s.Color); err != nil {
		return err
	}
	if err := s.checkShape(); err != nil {
		return err
	}
	return s.Meta.check("stroke", s.ID)
}

// checkShape checks what makes a stroke a shape: its kind, its two points,
// its fill and its text.
func (s *Stroke) checkShape() error {
	if s.Kind == "" {
		if s.Fill != "" || s.Text != "" {
			return fmt.Errorf("stroke %q is freehand: only shapes have a fill or text", s.ID)
		}
		return nil
	}
	if !slices.Contains(ShapeKinds, s.Kind) {
		return fmt.Errorf("stroke %q has a bad kind %q: one of %s", s.ID, s.Kind, strings.Join(ShapeKinds, ", "))
	}
	if s.Highlight {
		return fmt.Errorf("stroke %q: only freehand strokes are highlighter", s.ID)
	}
	if len(s.Points) != 6 {
		return fmt.Errorf("stroke %q is a %s: it needs exactly two points", s.ID, s.Kind)
	}
	if s.Fill != "" && s.Kind != "rect" && s.Kind != "ellipse" {
		return fmt.Errorf("stroke %q: only a rect or ellipse has a fill", s.ID)
	}
	if err := checkColor("stroke", s.ID, s.Fill); err != nil {
		return err
	}
	switch s.Kind {
	case "text":
		if strings.TrimSpace(s.Text) == "" {
			return fmt.Errorf("stroke %q is a text label with no text", s.ID)
		}
		if len([]rune(s.Text)) > MaxShapeText {
			return fmt.Errorf("stroke %q's text is too long (%d characters at most)", s.ID, MaxShapeText)
		}
	case "emoji":
		if s.Text == "" || len(s.Text) > maxEmojiBytes || strings.ContainsFunc(s.Text, isControl) {
			return fmt.Errorf("stroke %q needs one emoji as its text", s.ID)
		}
	default:
		if s.Text != "" {
			return fmt.Errorf("stroke %q: only a text label or emoji has text", s.ID)
		}
	}
	return nil
}

func (f *Frame) check() error {
	if err := checkID("frame", f.ID); err != nil {
		return err
	}
	if err := checkName(f.Name, "frame"); err != nil {
		return fmt.Errorf("frame %q: %w", f.ID, err)
	}
	if err := checkBox("frame", f.ID, f.X, f.Y, f.W, f.H); err != nil {
		return err
	}
	if err := checkColor("frame", f.ID, f.Color); err != nil {
		return err
	}
	return f.Meta.check("frame", f.ID)
}

func (m Meta) check(what, id string) error {
	for _, s := range []Source{m.CreatedBy, m.UpdatedBy} {
		if s != "" {
			if err := s.Check(); err != nil {
				return fmt.Errorf("%s %q: %w", what, id, err)
			}
		}
	}
	return nil
}
