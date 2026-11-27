package board

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
)

// BlockDef is a custom block type: one an agent designed through
// board.block.define (whiteboard#27), kept in the person's block library
// (library.go) and copied into every board that uses it, so the board
// renders the same on any Toru.
//
// A definition is data only. Its layout is a tree of the parts listed in
// blockparts.json (text, fields, checkboxes, polls, sliders, tables...),
// each bound to one of the block's props, and the board window draws each
// part with its own UI SDK components. No markup, script or style from a
// definition is ever run: that keeps the plan's rule that agents never
// send code to the board window, which can call into Toru.
type BlockDef struct {
	// Type is the name blocks use, like "feature-idea". It can't be a
	// built-in type's name.
	Type        string  `json:"type"`
	Label       string  `json:"label"` // what people call it, like "Feature idea"
	Description string  `json:"description,omitempty"`
	Icon        string  `json:"icon"` // one of blockparts.json's icons
	W           float64 `json:"w"`    // default size, world units
	H           float64 `json:"h"`
	// Props is the JSON Schema of a block's props, in the subset the
	// built-in catalog uses: a closed object at the top.
	Props json.RawMessage `json:"props"`
	// Defaults are a new block's props; they must pass Props.
	Defaults json.RawMessage `json:"defaults"`
	// Layout is the tree of parts the block shows (blockparts.json).
	Layout json.RawMessage `json:"layout"`
	// NameProp is the text prop that names a block, like "title": "Feature
	// idea: Dark mode". Default: the first required text prop.
	NameProp string `json:"nameProp,omitempty"`
	// Version counts the definition's changes, from 1. A board keeps the
	// version it was given until a newer one of the same Origin replaces it.
	Version int `json:"version"`
	// Origin is the id the definition was made with. It stays the same
	// through updates and copies, so Toru can tell a newer version of the
	// same block from a different block that happens to share its name.
	Origin string `json:"origin"`
}

// Limits on custom blocks.
const (
	MaxDefBytes       = 64 << 10 // one definition, as JSON
	MaxLayoutNodes    = 200
	maxLayoutDepth    = 12
	maxChildren       = 50
	maxSchemaDepth    = 5
	maxTopProps       = 40
	maxEnumValues     = 50
	MaxBoardTypes     = 100 // custom types one board carries
	maxDefLabelLen    = 60
	maxDefDescLen     = 500
	maxPartTextLen    = 200
	maxPartTemplate   = 500
	minDefSize        = 40
	maxDefSize        = 4000
	maxSchemaDescLen  = 300
	maxColumnNameLen  = 60
	maxColumns        = 12
	maxColorsEntries  = 30
	maxPropNameLen    = 40
	maxEnumValueChars = 100
)

var (
	propNamePattern = regexp.MustCompile(`^[a-z][A-Za-z0-9_]{0,39}$`)
	templateRef     = regexp.MustCompile(`\{([^{}]*)\}`)
)

//go:embed blockparts.json
var blockPartsJSON []byte

// partField is one field a layout part takes.
type partField struct {
	Kind     string   `json:"kind"`
	Enum     []string `json:"enum"`
	Required bool     `json:"required"`
	Binds    []string `json:"binds"`
}

// partSpec is one kind of layout part.
type partSpec struct {
	Description string               `json:"description"`
	Children    bool                 `json:"children"`
	Fields      map[string]partField `json:"fields"`
	OneOf       []string             `json:"oneOf"`
}

// blockParts is blockparts.json.
var blockParts = func() struct {
	Parts map[string]partSpec `json:"parts"`
	Icons []string            `json:"icons"`
} {
	var f struct {
		Parts map[string]partSpec `json:"parts"`
		Icons []string            `json:"icons"`
	}
	if err := json.Unmarshal(blockPartsJSON, &f); err != nil {
		panic("board: blockparts.json: " + err.Error())
	}
	return f
}()

// BlockParts is blockparts.json as it is: the parts a custom block's layout
// may use, for board.catalog and the board.block commands to show agents.
func BlockParts() json.RawMessage { return append(json.RawMessage(nil), blockPartsJSON...) }

// boardColors is catalog.json's colours.
var boardColors = func() []string {
	var f struct {
		Colors []string `json:"colors"`
	}
	if err := json.Unmarshal(catalogJSON, &f); err != nil {
		panic("board: catalog.json colors: " + err.Error())
	}
	return f.Colors
}()

func inList(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// IsBuiltinType reports whether t is a type of the built-in catalog.
func IsBuiltinType(t string) bool {
	_, ok := LookupType(t)
	return ok
}

// ValidBlockTypeName reports whether t can name a block type.
func ValidBlockTypeName(t string) bool { return typePattern.MatchString(t) }

// Check checks a whole definition and stores its JSON compact. Version and
// Origin are checked too: the library sets them.
func (d *BlockDef) Check() error {
	if err := d.checkContent(); err != nil {
		return err
	}
	if d.Version < 1 {
		return fmt.Errorf("block type %q has a bad version %d", d.Type, d.Version)
	}
	if !idPattern.MatchString(d.Origin) {
		return fmt.Errorf("block type %q has a bad origin %q", d.Type, d.Origin)
	}
	if n, err := json.Marshal(d); err != nil || len(n) > MaxDefBytes {
		return fmt.Errorf("block type %q is too big (%d KB at most)", d.Type, MaxDefBytes>>10)
	}
	return nil
}

// checkContent checks everything an agent sends: all but Version and
// Origin.
func (d *BlockDef) checkContent() error {
	if !typePattern.MatchString(d.Type) {
		return fmt.Errorf("bad block type name %q: lower-case letters, digits and -, starting with a letter, up to 40", d.Type)
	}
	if IsBuiltinType(d.Type) {
		return fmt.Errorf("%q is a built-in block type: give yours another name", d.Type)
	}
	what := fmt.Sprintf("block type %q", d.Type)
	if err := checkPlain(d.Label, "label", maxDefLabelLen, true); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := checkPlain(d.Description, "description", maxDefDescLen, false); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if !inList(blockParts.Icons, d.Icon) {
		return fmt.Errorf("%s: icon %q isn't one Toru has (board.catalog lists the icons)", what, d.Icon)
	}
	for _, v := range []float64{d.W, d.H} {
		if math.IsNaN(v) || v < minDefSize || v > maxDefSize {
			return fmt.Errorf("%s: w and h must be between %d and %d", what, minDefSize, maxDefSize)
		}
	}

	schema, err := compactObject(&d.Props, "props")
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		return fmt.Errorf(`%s: props must be a closed object schema: {"type": "object", "additionalProperties": false, "properties": {...}}`, what)
	}
	if err := checkSchemaShape(schema, "props", 0, true); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	props, _ := schema["properties"].(map[string]any)
	kinds := map[string]string{}
	for name, s := range props {
		kinds[name] = PropKind(s.(map[string]any))
	}

	if _, err := compactObject(&d.Defaults, "defaults"); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if err := checkJSONSchema(schema, d.Defaults, "defaults"); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}

	if d.NameProp != "" {
		if k := kinds[d.NameProp]; k != "string" && k != "enum" {
			return fmt.Errorf("%s: nameProp %q must name a text prop", what, d.NameProp)
		}
	}

	var layout any
	if err := compactJSON(&d.Layout, "layout"); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	_ = json.Unmarshal(d.Layout, &layout)
	lc := layoutCheck{kinds: kinds, schemas: props}
	if err := lc.node(layout, "layout", 0); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// Name is the prop that names a block of this type: NameProp, or the first
// required text prop, or "".
func (d *BlockDef) Name() string {
	if d.NameProp != "" {
		return d.NameProp
	}
	var s struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(d.Props, &s)
	for _, r := range s.Required {
		var p map[string]any
		_ = json.Unmarshal(s.Properties[r], &p)
		if PropKind(p) == "string" {
			return r
		}
	}
	return ""
}

// CheckProps checks a block's props against the definition's schema.
func (d *BlockDef) CheckProps(props json.RawMessage) error {
	var schema map[string]any
	if err := json.Unmarshal(d.Props, &schema); err != nil {
		return err
	}
	return checkJSONSchema(schema, props, "props")
}

// Same reports whether two definitions are the same version of the same
// block.
func (d *BlockDef) Same(o *BlockDef) bool {
	a, _ := json.Marshal(d)
	b, _ := json.Marshal(o)
	return bytes.Equal(a, b)
}

func checkJSONSchema(schema map[string]any, raw json.RawMessage, at string) error {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return fmt.Errorf("%s aren't JSON: %w", at, err)
	}
	return checkSchema(schema, value, at)
}

// checkPlain checks a short text shown as is.
func checkPlain(s, what string, max int, required bool) error {
	if required && strings.TrimSpace(s) == "" {
		return fmt.Errorf("needs a %s", what)
	}
	if len([]rune(s)) > max {
		return fmt.Errorf("%s is too long (%d characters at most)", what, max)
	}
	if strings.ContainsFunc(s, func(r rune) bool { return isControl(r) && r != '\n' }) {
		return fmt.Errorf("%s has a control character", what)
	}
	return nil
}

func compactJSON(raw *json.RawMessage, what string) error {
	t := bytes.TrimSpace(*raw)
	if len(t) == 0 || string(t) == "null" {
		return fmt.Errorf("needs %s", what)
	}
	var c bytes.Buffer
	if err := json.Compact(&c, t); err != nil {
		return fmt.Errorf("%s isn't JSON: %w", what, err)
	}
	*raw = c.Bytes()
	return nil
}

func compactObject(raw *json.RawMessage, what string) (map[string]any, error) {
	if err := compactJSON(raw, what); err != nil {
		return nil, err
	}
	var m map[string]any
	if (*raw)[0] != '{' || json.Unmarshal(*raw, &m) != nil {
		return nil, fmt.Errorf("%s must be a JSON object", what)
	}
	return m, nil
}

// schemaKeywords are the JSON Schema keywords checkSchema checks. Any
// other keyword would be silently ignored while agents read it as a rule.
var schemaKeywords = map[string]bool{
	"type": true, "properties": true, "required": true, "additionalProperties": true, "items": true,
	"enum": true, "minLength": true, "maxLength": true, "minItems": true, "maxItems": true,
	"minimum": true, "maximum": true, "description": true,
}

// checkSchemaShape checks a custom block's props schema uses only what
// checkSchema checks, and is small enough to draw.
func checkSchemaShape(s map[string]any, at string, depth int, top bool) error {
	if depth > maxSchemaDepth {
		return fmt.Errorf("%s nests too deep (%d levels at most)", at, maxSchemaDepth)
	}
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !schemaKeywords[k] {
			return fmt.Errorf("%s: keyword %q isn't one Toru checks (it takes %s)", at, k, keywordList())
		}
	}
	typ, _ := s["type"].(string)
	switch typ {
	case "object", "array", "string", "boolean", "integer", "number":
	default:
		return fmt.Errorf("%s needs a type: object, array, string, boolean, integer or number", at)
	}
	if d, ok := s["description"]; ok {
		if str, ok := d.(string); !ok || len([]rune(str)) > maxSchemaDescLen {
			return fmt.Errorf("%s: description must be text up to %d characters", at, maxSchemaDescLen)
		}
	}
	for _, k := range []string{"minLength", "maxLength", "minItems", "maxItems", "minimum", "maximum"} {
		if v, ok := s[k]; ok {
			if f, ok := v.(float64); !ok || math.IsInf(f, 0) {
				return fmt.Errorf("%s: %s must be a number", at, k)
			}
		}
	}
	if v, ok := s["additionalProperties"]; ok {
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: additionalProperties must be true or false", at)
		}
	}
	switch typ {
	case "object":
		props, ok := s["properties"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: an object needs properties", at)
		}
		if top && len(props) > maxTopProps {
			return fmt.Errorf("%s has more than %d props", at, maxTopProps)
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if top && !propNamePattern.MatchString(name) {
				return fmt.Errorf("%s: bad prop name %q (a lower-case letter, then letters, digits and _, up to %d)", at, name, maxPropNameLen)
			}
			if len(name) > maxPropNameLen {
				return fmt.Errorf("%s: field name %q is too long", at, name)
			}
			sub, ok := props[name].(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s must be a schema object", at, name)
			}
			if err := checkSchemaShape(sub, at+"."+name, depth+1, false); err != nil {
				return err
			}
		}
		if req, ok := s["required"]; ok {
			list, ok := req.([]any)
			if !ok {
				return fmt.Errorf("%s: required must be a list of field names", at)
			}
			for _, r := range list {
				name, ok := r.(string)
				if _, has := props[name]; !ok || !has {
					return fmt.Errorf("%s: required names %v, which isn't one of its properties", at, r)
				}
			}
		}
	case "array":
		items, ok := s["items"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: a list needs items", at)
		}
		if err := checkSchemaShape(items, at+"[]", depth+1, false); err != nil {
			return err
		}
	}
	if typ != "object" {
		for _, k := range []string{"properties", "required", "additionalProperties"} {
			if _, ok := s[k]; ok {
				return fmt.Errorf("%s: %s is only for objects", at, k)
			}
		}
	}
	if typ != "array" {
		if _, ok := s["items"]; ok {
			return fmt.Errorf("%s: items is only for lists", at)
		}
	}
	if e, ok := s["enum"]; ok {
		list, ok := e.([]any)
		if typ != "string" || !ok || len(list) == 0 || len(list) > maxEnumValues {
			return fmt.Errorf("%s: enum must be a list of 1 to %d texts on a text field", at, maxEnumValues)
		}
		for _, v := range list {
			str, ok := v.(string)
			if !ok || str == "" || len([]rune(str)) > maxEnumValueChars {
				return fmt.Errorf("%s: enum values must be texts of 1 to %d characters", at, maxEnumValueChars)
			}
		}
	}
	return nil
}

func keywordList() string {
	ks := make([]string, 0, len(schemaKeywords))
	for k := range schemaKeywords {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}

// PropKind is which kind of prop a schema describes, as blockparts.json's
// propKinds name them, or "" for one no part binds to.
func PropKind(s map[string]any) string {
	switch s["type"] {
	case "string":
		if _, ok := s["enum"]; ok {
			return "enum"
		}
		return "string"
	case "number", "integer", "boolean":
		return s["type"].(string)
	case "array":
		items, _ := s["items"].(map[string]any)
		switch items["type"] {
		case "string":
			return "string-list"
		case "array":
			inner, _ := items["items"].(map[string]any)
			if inner["type"] == "string" {
				return "rows"
			}
		case "object":
			fields, _ := items["properties"].(map[string]any)
			if fieldsAre(fields, map[string][]string{"text": {"string"}, "done": {"boolean"}}, "text") {
				return "checklist"
			}
			if fieldsAre(fields, map[string][]string{"label": {"string"}, "votes": {"integer", "number"}}, "label") {
				return "poll"
			}
		}
	}
	return ""
}

// fieldsAre reports whether an object's fields are only the ones named,
// each of one of its types, and include must.
func fieldsAre(fields map[string]any, want map[string][]string, must string) bool {
	if _, ok := fields[must]; !ok {
		return false
	}
	for name, f := range fields {
		types, ok := want[name]
		if !ok {
			return false
		}
		s, _ := f.(map[string]any)
		t, _ := s["type"].(string)
		if !inList(types, t) || (t == "string" && s["enum"] != nil) {
			return false
		}
	}
	return true
}

// layoutCheck checks a layout tree against blockparts.json and the props.
type layoutCheck struct {
	kinds   map[string]string // prop name to its PropKind
	schemas map[string]any
	nodes   int
}

func (c *layoutCheck) node(v any, at string, depth int) error {
	if depth > maxLayoutDepth {
		return fmt.Errorf("%s nests too deep (%d levels at most)", at, maxLayoutDepth)
	}
	c.nodes++
	if c.nodes > MaxLayoutNodes {
		return fmt.Errorf("the layout has more than %d parts", MaxLayoutNodes)
	}
	n, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf(`%s must be a part: {"part": "stack", ...}`, at)
	}
	name, _ := n["part"].(string)
	spec, ok := blockParts.Parts[name]
	if !ok {
		return fmt.Errorf("%s: no part %q (board.catalog lists the parts: %s)", at, n["part"], partList())
	}
	at = fmt.Sprintf("%s (%s)", at, name)
	keys := make([]string, 0, len(n))
	for k := range n {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "part" {
			continue
		}
		if k == "children" {
			if !spec.Children {
				return fmt.Errorf("%s has no children", at)
			}
			kids, ok := n[k].([]any)
			if !ok {
				return fmt.Errorf("%s: children must be a list of parts", at)
			}
			if len(kids) > maxChildren {
				return fmt.Errorf("%s has more than %d children", at, maxChildren)
			}
			for i, kid := range kids {
				if err := c.node(kid, fmt.Sprintf("%s.children[%d]", at, i), depth+1); err != nil {
					return err
				}
			}
			continue
		}
		f, ok := spec.Fields[k]
		if !ok {
			return fmt.Errorf("%s has no field %q", at, k)
		}
		if err := c.field(f, n[k], at+"."+k); err != nil {
			return err
		}
	}
	for k, f := range spec.Fields {
		if _, has := n[k]; f.Required && !has {
			return fmt.Errorf("%s needs %q", at, k)
		}
	}
	if len(spec.OneOf) > 0 {
		found := 0
		for _, k := range spec.OneOf {
			if _, has := n[k]; has {
				found++
			}
		}
		if found != 1 {
			return fmt.Errorf("%s needs exactly one of %s", at, strings.Join(spec.OneOf, ", "))
		}
	}
	return nil
}

func (c *layoutCheck) field(f partField, v any, at string) error {
	if len(f.Enum) > 0 {
		s, ok := v.(string)
		if !ok || !inList(f.Enum, s) {
			return fmt.Errorf("%s must be one of %s", at, strings.Join(f.Enum, ", "))
		}
		return nil
	}
	switch f.Kind {
	case "text":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s must be text", at)
		}
		return checkPlain(s, at, maxPartTextLen, false)
	case "template":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s must be text", at)
		}
		if err := checkPlain(s, at, maxPartTemplate, false); err != nil {
			return err
		}
		for _, m := range templateRef.FindAllStringSubmatch(s, -1) {
			switch c.kinds[m[1]] {
			case "string", "enum", "number", "integer", "boolean":
			default:
				return fmt.Errorf("%s: {%s} isn't a text, number or true/false prop", at, m[1])
			}
		}
	case "prop":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s must name a prop", at)
		}
		k, has := c.kinds[s]
		if !has {
			return fmt.Errorf("%s: no prop %q in props", at, s)
		}
		if !inList(f.Binds, k) {
			got := k
			if got == "" {
				got = "a shape no part takes"
			}
			return fmt.Errorf("%s: prop %q is %s; this part takes %s", at, s, got, strings.Join(f.Binds, " or "))
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be true or false", at)
		}
	case "number":
		x, ok := v.(float64)
		if !ok || math.IsInf(x, 0) || math.Abs(x) > maxCoord {
			return fmt.Errorf("%s must be a number", at)
		}
	case "color":
		s, _ := v.(string)
		if !inList(boardColors, s) {
			return fmt.Errorf("%s must be one of %s", at, strings.Join(boardColors, ", "))
		}
	case "colors":
		m, ok := v.(map[string]any)
		if !ok || len(m) > maxColorsEntries {
			return fmt.Errorf("%s must be an object of up to %d values to colours", at, maxColorsEntries)
		}
		for k, col := range m {
			s, _ := col.(string)
			if len([]rune(k)) > maxEnumValueChars || !inList(boardColors, s) {
				return fmt.Errorf("%s.%s must be one of %s", at, k, strings.Join(boardColors, ", "))
			}
		}
	case "icon":
		s, _ := v.(string)
		if !inList(blockParts.Icons, s) {
			return fmt.Errorf("%s: icon %v isn't one Toru has", at, v)
		}
	case "columns":
		list, ok := v.([]any)
		if !ok || len(list) == 0 || len(list) > maxColumns {
			return fmt.Errorf("%s must be a list of 1 to %d column names", at, maxColumns)
		}
		for _, x := range list {
			s, ok := x.(string)
			if !ok || strings.TrimSpace(s) == "" {
				return fmt.Errorf("%s: each column needs a name", at)
			}
			if err := checkPlain(s, at, maxColumnNameLen, true); err != nil {
				return err
			}
		}
	default:
		return errors.New("blockparts.json: unknown field kind " + f.Kind)
	}
	return nil
}

func partList() string {
	names := make([]string, 0, len(blockParts.Parts))
	for n := range blockParts.Parts {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// BoardType is a board's own copy of a custom block type, or nil.
func (b *Board) BoardType(t string) *BlockDef {
	for i := range b.Types {
		if b.Types[i].Type == t {
			return &b.Types[i]
		}
	}
	return nil
}

// CheckBlockProps checks a block's props against its type: a built-in
// type's catalog schema, or the custom type's definition the board carries.
func CheckBlockProps(b *Board, blockType string, props json.RawMessage) error {
	if IsBuiltinType(blockType) {
		return CheckProps(blockType, props)
	}
	if d := b.BoardType(blockType); d != nil {
		return d.CheckProps(props)
	}
	return CheckProps(blockType, props)
}
