package board

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
)

// An exported .toruboard (docs/whiteboard.md section 3) is the board file
// with the pictures its image blocks show inlined beside it: an "images"
// object, asset name → the picture's bytes as base64. Toru's own board files
// never carry it (pictures stay in ImagesDir), and Decode ignores it, so an
// exported file still opens as a plain board and a plain board file still
// imports.

// maxPortableBytes caps an exported board read back: the board itself
// (maxFileBytes) and its pictures.
const maxPortableBytes = 512 << 20

type portable struct {
	Board
	Images map[string]string `json:"images,omitempty"`
}

// Assets is every picture name b's image blocks show, sorted: built-in
// image blocks, and the image parts of custom blocks (BlockDef).
func Assets(b Board) []string {
	seen := map[string]bool{}
	for _, x := range b.Blocks {
		keys := []string{"asset"}
		if x.Type != "image" {
			d := b.BoardType(x.Type)
			if d == nil {
				continue
			}
			keys = d.ImageProps()
		}
		var p map[string]any
		if json.Unmarshal(x.Props, &p) != nil {
			continue
		}
		for _, k := range keys {
			if a, _ := p[k].(string); assetPattern.MatchString(a) {
				seen[a] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for a := range seen {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

// EncodePortable is b as an exported .toruboard: the board file with the
// pictures its image blocks show inlined. read gives a picture's bytes by
// name (ReadImage); a picture that can't be read is left out, and its block
// imports empty.
func EncodePortable(b Board, read func(asset string) ([]byte, error)) ([]byte, error) {
	b.fill()
	b.MirrorOf = nil // an exported board stands alone
	out := portable{Board: b}
	for _, a := range Assets(b) {
		data, err := read(a)
		if err != nil {
			continue
		}
		if out.Images == nil {
			out.Images = map[string]string{}
		}
		out.Images[a] = base64.StdEncoding.EncodeToString(data)
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// DecodePortable reads an exported (or plain) .toruboard: the board, checked
// as Decode does, and its inlined pictures by name. Each picture's name
// must be the SHA-256 of its bytes with its type's extension, as
// ImportImage names it, or the file is refused.
func DecodePortable(data []byte) (Board, map[string][]byte, error) {
	b, err := Decode(data)
	if err != nil {
		return Board{}, nil, err
	}
	var p struct {
		Images map[string]string `json:"images"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return Board{}, nil, fmt.Errorf("not a Toru board file: %w", err)
	}
	images := map[string][]byte{}
	for name, b64 := range p.Images {
		if !assetPattern.MatchString(name) {
			return Board{}, nil, fmt.Errorf("the file has a picture with a bad name %q", name)
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return Board{}, nil, fmt.Errorf("picture %s isn't base64: %w", name, err)
		}
		if got := imageName(raw); got != name {
			return Board{}, nil, fmt.Errorf("picture %s doesn't match its name", name)
		}
		images[name] = raw
	}
	return b, images, nil
}

// ReadImage is a board picture's bytes, by name.
func ReadImage(asset string) ([]byte, error) {
	path, err := ImagePath(asset)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// ImportFile reads a .toruboard (an exported one with its pictures, or a
// plain board file) and saves it as a board. It keeps the file's id unless a
// board has it, then takes a new "brd-" id, so an import never replaces a
// board. The pictures go into ImagesDir. The board's undo history starts
// empty.
func (s *Store) ImportFile(path string) (Board, error) {
	f, err := os.Open(path)
	if err != nil {
		return Board{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxPortableBytes+1))
	if err != nil {
		return Board{}, err
	}
	if len(data) > maxPortableBytes {
		return Board{}, errors.New("that file is too big to be a board")
	}
	return s.Import(data)
}

// Import is ImportFile for a file's bytes.
func (s *Store) Import(data []byte) (Board, error) {
	b, images, err := DecodePortable(data)
	if err != nil {
		return Board{}, err
	}
	for name, raw := range images {
		img, err := ImportImage(raw)
		if err != nil {
			return Board{}, fmt.Errorf("picture %s: %w", name, err)
		}
		if img.Asset != name {
			return Board{}, fmt.Errorf("picture %s doesn't match its name", name)
		}
	}
	b.MirrorOf = nil
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.idTaken(b.ID) {
		b.ID = "brd-" + randHex(8)
	}
	b.UpdatedAt = now()
	if err := s.writeFile(b); err != nil {
		return Board{}, err
	}
	s.docs[b.ID] = NewDoc(b)
	return clone(b), nil
}

// idTaken reports whether a board with id is open or saved. Call it with
// s.mu held.
func (s *Store) idTaken(id string) bool {
	if _, ok := s.docs[id]; ok {
		return true
	}
	p, err := s.boardPath(id)
	if err != nil {
		return true
	}
	_, err = os.Stat(p)
	return !errors.Is(err, os.ErrNotExist)
}
