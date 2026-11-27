package board

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

// BlockType is one entry of the block catalog: what an agent may place on
// a board, with its props as JSON Schema. board.catalog lists these, and the
// board commands check every block's props against its type's schema.
//
// This is the starting set. The block catalog card (whiteboard#6) renders
// these types in the window and adds more; each new type is added here too,
// so agents see it.
type BlockType struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	W           float64         `json:"w"` // default size, world units
	H           float64         `json:"h"`
	Props       json.RawMessage `json:"props"` // JSON Schema of the props object
}

var tagsSchema = map[string]any{
	"type": "array", "maxItems": 20,
	"items":       map[string]any{"type": "string", "minLength": 1, "maxLength": 40},
	"description": "Tags, for board.layout's cluster and for search.",
}

func objectSchema(props map[string]any, required ...string) json.RawMessage {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return b
}

var catalog = []BlockType{
	{
		Type:        "sticky",
		Description: "A sticky note: a short idea in a coloured square.",
		W:           200, H: 200,
		Props: objectSchema(map[string]any{
			"text":  map[string]any{"type": "string", "maxLength": 10000},
			"color": map[string]any{"type": "string", "enum": []string{"yellow", "pink", "blue", "green", "purple", "orange", "grey"}, "description": "Default yellow."},
			"tags":  tagsSchema,
		}, "text"),
	},
	{
		Type:        "text",
		Description: "Free text on the board: a heading, a label or a paragraph.",
		W:           320, H: 80,
		Props: objectSchema(map[string]any{
			"text":  map[string]any{"type": "string", "maxLength": 20000},
			"size":  map[string]any{"type": "string", "enum": []string{"s", "m", "l", "xl"}, "description": "Default m; xl for a heading."},
			"align": map[string]any{"type": "string", "enum": []string{"left", "center", "right"}},
			"tags":  tagsSchema,
		}, "text"),
	},
	{
		Type:        "checklist",
		Description: "A list of items people and agents tick off.",
		W:           280, H: 200,
		Props: objectSchema(map[string]any{
			"title": map[string]any{"type": "string", "maxLength": 200},
			"items": map[string]any{
				"type": "array", "maxItems": 200,
				"items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"text"},
					"properties": map[string]any{
						"text": map[string]any{"type": "string", "minLength": 1, "maxLength": 1000},
						"done": map[string]any{"type": "boolean"},
					},
				},
			},
			"tags": tagsSchema,
		}, "items"),
	},
	{
		Type:        "poll",
		Description: "A question with options people and agents vote on.",
		W:           300, H: 220,
		Props: objectSchema(map[string]any{
			"question": map[string]any{"type": "string", "minLength": 1, "maxLength": 500},
			"multi":    map[string]any{"type": "boolean", "description": "Several options may be picked."},
			"options": map[string]any{
				"type": "array", "minItems": 2, "maxItems": 20,
				"items": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"label"},
					"properties": map[string]any{
						"label": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
						"votes": map[string]any{"type": "integer", "minimum": 0},
					},
				},
			},
			"tags": tagsSchema,
		}, "question", "options"),
	},
}

// Catalog is every block type agents may place, sorted by type.
func Catalog() []BlockType {
	out := append([]BlockType(nil), catalog...)
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// LookupType is the catalog entry for a block type.
func LookupType(t string) (BlockType, bool) {
	for _, bt := range catalog {
		if bt.Type == t {
			return bt, true
		}
	}
	return BlockType{}, false
}

// CheckProps checks a block's props against its catalog type's schema.
func CheckProps(blockType string, props json.RawMessage) error {
	bt, ok := LookupType(blockType)
	if !ok {
		names := make([]string, 0, len(catalog))
		for _, c := range Catalog() {
			names = append(names, c.Type)
		}
		return fmt.Errorf("no block type %q (board.catalog lists them: %s)", blockType, strings.Join(names, ", "))
	}
	var schema, value any
	if err := json.Unmarshal(bt.Props, &schema); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(props))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return fmt.Errorf("props aren't JSON: %w", err)
	}
	return checkSchema(schema.(map[string]any), value, "props")
}

// checkSchema checks v against the subset of JSON Schema the catalog uses:
// type, properties, required, additionalProperties false, items, enum,
// minLength, maxLength, minItems, maxItems, minimum, maximum.
func checkSchema(s map[string]any, v any, at string) error {
	switch s["type"] {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", at)
		}
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, r := range req {
				if _, ok := obj[r.(string)]; !ok {
					return fmt.Errorf("%s needs %q", at, r)
				}
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			sub, ok := props[k].(map[string]any)
			if !ok {
				if s["additionalProperties"] == false {
					return fmt.Errorf("%s has no field %q", at, k)
				}
				continue
			}
			if err := checkSchema(sub, obj[k], at+"."+k); err != nil {
				return err
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s must be a list", at)
		}
		if n, ok := num(s["minItems"]); ok && float64(len(arr)) < n {
			return fmt.Errorf("%s needs at least %v items", at, n)
		}
		if n, ok := num(s["maxItems"]); ok && float64(len(arr)) > n {
			return fmt.Errorf("%s has more than %v items", at, n)
		}
		if items, ok := s["items"].(map[string]any); ok {
			for i, x := range arr {
				if err := checkSchema(items, x, fmt.Sprintf("%s[%d]", at, i)); err != nil {
					return err
				}
			}
		}
	case "string":
		str, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s must be text", at)
		}
		n := float64(len([]rune(str)))
		if m, ok := num(s["minLength"]); ok && n < m {
			return fmt.Errorf("%s is too short", at)
		}
		if m, ok := num(s["maxLength"]); ok && n > m {
			return fmt.Errorf("%s is too long (%v characters at most)", at, m)
		}
		if enum, ok := s["enum"].([]any); ok {
			for _, e := range enum {
				if e == str {
					return nil
				}
			}
			return fmt.Errorf("%s must be one of %v", at, enum)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be true or false", at)
		}
	case "integer", "number":
		jn, ok := v.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be a number", at)
		}
		f, err := jn.Float64()
		if err != nil || math.IsInf(f, 0) {
			return fmt.Errorf("%s must be a number", at)
		}
		if s["type"] == "integer" && f != math.Trunc(f) {
			return fmt.Errorf("%s must be a whole number", at)
		}
		if m, ok := num(s["minimum"]); ok && f < m {
			return fmt.Errorf("%s must be at least %v", at, m)
		}
		if m, ok := num(s["maximum"]); ok && f > m {
			return fmt.Errorf("%s must be at most %v", at, m)
		}
	}
	return nil
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}
