package board

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sampleBoard is the window's sample board (one block of every type, a
// frame, links and a picture) as a saved board.
func sampleBoard(t *testing.T) Board {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "whiteboard", "sample-board.json"))
	if err != nil {
		t.Fatal(err)
	}
	b := New("brd-sample01", "Launch plan", time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatal(err)
	}
	b.Name = "Launch plan"
	// The sample's link ids are the window's ("a->b"); a saved board's are
	// plain ids.
	for i := range b.Links {
		b.Links[i].ID = strings.NewReplacer("->", "-to-").Replace(b.Links[i].ID)
	}
	b.Ink = append(b.Ink, Stroke{ID: "ink-1", Points: []float64{0, 0, 0.5, 40, 20, 0.5, 80, 0, 0.5}, Color: "ink-blue", Width: 3, Meta: Meta{CreatedBy: Person}})
	b.Rev = 7
	if err := b.Validate(); err != nil {
		t.Fatal(err)
	}
	return b
}

// An exported .toruboard carries its pictures, and imports on a Toru that
// has never seen them: the same board, item for item, with the pictures in
// its board images.
func TestPortableRoundTrip(t *testing.T) {
	useTempBoards(t)
	if _, err := ImportImageFile(filepath.Join("testdata", "images", samplePicture)); err != nil {
		t.Fatal(err)
	}
	b := sampleBoard(t)
	if got := Assets(b); len(got) != 1 || got[0] != samplePicture {
		t.Fatalf("assets %v", got)
	}
	data, err := EncodePortable(b, ReadImage)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"images": {`)) || !bytes.Contains(data, []byte(samplePicture)) {
		t.Fatal("the export has no inlined picture")
	}
	// Still a board file anything reading .toruboard opens.
	if _, err := Decode(data); err != nil {
		t.Fatal("an exported board isn't a board file:", err)
	}

	// Another Toru: no boards, no pictures.
	dir := useTempBoards(t)
	export := filepath.Join(t.TempDir(), "Launch plan.toruboard")
	if err := os.WriteFile(export, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStore()
	got, err := s.ImportFile(export)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != b.ID {
		t.Errorf("id %q, want the file's %q while it is free", got.ID, b.ID)
	}
	if _, err := os.Stat(filepath.Join(dir, "images", samplePicture)); err != nil {
		t.Fatal("the picture didn't come in:", err)
	}
	if !sameBoard(t, got, b) {
		t.Fatal("the imported board differs from the exported one")
	}
	// Saved: it opens from disk as the same board.
	loaded, err := NewStore().Load(got.ID)
	if err != nil || !sameBoard(t, loaded, b) {
		t.Fatalf("the saved import differs: %v", err)
	}
	// Toru's own file never keeps the pictures.
	own, _ := os.ReadFile(filepath.Join(dir, got.ID+Ext))
	if bytes.Contains(own, []byte(`"images"`)) {
		t.Error("the board's own file kept the inlined pictures")
	}

	// Again: a new id, never replacing the board.
	again, err := s.ImportFile(export)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID == got.ID || !strings.HasPrefix(again.ID, "brd-") {
		t.Errorf("second import id %q (first %q)", again.ID, got.ID)
	}
	again.ID = b.ID
	if !sameBoard(t, again, b) {
		t.Error("the second import differs")
	}
}

// sameBoard compares two boards as files, but for when they were last
// changed.
func sameBoard(t *testing.T, a, b Board) bool {
	t.Helper()
	a.UpdatedAt, b.UpdatedAt = time.Time{}, time.Time{}
	x, err := Encode(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(x, y) {
		t.Logf("got:\n%s\nwant:\n%s", x, y)
		return false
	}
	return true
}

func TestImportPlainBoardFile(t *testing.T) {
	useTempBoards(t)
	b := sampleBoard(t)
	data, err := Encode(b)
	if err != nil {
		t.Fatal(err)
	}
	got, err := NewStore().Import(data)
	if err != nil {
		t.Fatal(err)
	}
	if !sameBoard(t, got, b) {
		t.Fatal("a plain board file imported differently")
	}
}

func TestImportRefusesBadPictures(t *testing.T) {
	useTempBoards(t)
	b := sampleBoard(t)
	pic, err := os.ReadFile(filepath.Join("testdata", "images", samplePicture))
	if err != nil {
		t.Fatal(err)
	}
	other := append(append([]byte(nil), pic...), 0) // not the bytes its name says
	for name, images := range map[string]map[string]string{
		"wrong bytes":  {samplePicture: base64.StdEncoding.EncodeToString(other)},
		"bad name":     {"../evil.png": base64.StdEncoding.EncodeToString(pic)},
		"not base64":   {samplePicture: "%%%"},
		"not an image": {strings.Repeat("0", 64) + ".png": base64.StdEncoding.EncodeToString([]byte("hello"))},
	} {
		data, err := json.Marshal(portable{Board: b, Images: images})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore().Import(data); err == nil {
			t.Errorf("%s: imported", name)
		}
	}
	if list, _ := NewStore().List(); len(list) != 0 {
		t.Errorf("a refused import left %d boards", len(list))
	}
}

// The outline names what the card asks for: frames as headings, notes
// (stickies) as bullets, checklists as boxes, tables as tables, and links.
func TestMarkdownOutline(t *testing.T) {
	md := Markdown(sampleBoard(t))
	for _, want := range []string{
		"# Launch plan\n",
		"\n## Ideas\n\n- Send a screenshot straight to a board #capture\n",
		"  - [x] Write the catalog\n",
		"  | Task | Owner | Due |\n",
		"\n## Links\n\n- ",
		" → ",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("outline lacks %q:\n%s", want, md)
		}
	}
}
