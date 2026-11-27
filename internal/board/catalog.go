package board

import (
	"bytes"
	_ "embed"
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
// The catalog is catalog.json, which the board window reads too
// (frontend/src/whiteboard/catalog.ts), so Go and the window agree on every
// type and field. A new block type is added there and rendered in
// frontend/src/whiteboard/blocks.
type BlockType struct {
	Type        string `json:"type"`
	Label       string `json:"label"` // what people call it, like "Sticky note"
	Description string `json:"description"`
	// Region marks a type that isn't placed as a block: frame, which is the
	// board's Frames list. Its props schema is the frame's fields.
	Region bool            `json:"region,omitempty"`
	W      float64         `json:"w"` // default size, world units
	H      float64         `json:"h"`
	Props  json.RawMessage `json:"props"` // JSON Schema of the props object
}

//go:embed catalog.json
var catalogJSON []byte

// catalog is catalog.json's types, in its order.
var catalog = func() []BlockType {
	var f struct {
		Types []BlockType `json:"types"`
	}
	if err := json.Unmarshal(catalogJSON, &f); err != nil {
		panic("board: catalog.json: " + err.Error())
	}
	return f.Types
}()

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
	if ok && bt.Region {
		return fmt.Errorf("a %s isn't a block: add it under frames, and put blocks in it with their frame field", blockType)
	}
	if !ok {
		names := make([]string, 0, len(catalog))
		for _, c := range Catalog() {
			if c.Region {
				continue
			}
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
