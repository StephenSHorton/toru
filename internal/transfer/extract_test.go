package transfer

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeZip builds a zip at path from name→content pairs (in the given order).
func writeZip(t *testing.T, path string, entries [][2]string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZipHappyPath(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "in.zip")
	writeZip(t, zp, [][2]string{
		{"readme.txt", "hello"},
		{"sub/", ""},
		{"sub/nested/deep.bin", "deep"},
		{"./dot.txt", "dot"},
		{"weird:name?.txt", "scrubbed"}, // Linux-legal, NTFS-illegal → scrubbed, not fatal
	})
	root := filepath.Join(dir, "out")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(context.Background(), zp, root, extractBudget(0)); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"readme.txt":          "hello",
		"sub/nested/deep.bin": "deep",
		"dot.txt":             "dot",
		"weirdname.txt":       "scrubbed",
	}
	for rel, content := range want {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			continue
		}
		if string(got) != content {
			t.Errorf("%s = %q, want %q", rel, got, content)
		}
	}
}

func TestExtractZipRejectsZipSlip(t *testing.T) {
	bad := []string{
		"../evil.txt",
		"sub/../../evil.txt",
		`..\evil.txt`,
		`C:\evil.txt`,
		"C:evil.txt",
		"/evil.txt",
		`\evil.txt`,
		"ok/../../../evil.txt",
		"nul\x00byte.txt",
	}
	for _, name := range bad {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			zp := filepath.Join(dir, "in.zip")
			writeZip(t, zp, [][2]string{{"fine.txt", "ok"}, {name, "pwned"}})
			root := filepath.Join(dir, "out")
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			err := extractZip(context.Background(), zp, root, extractBudget(0))
			if !errors.Is(err, errZipSlip) {
				t.Fatalf("entry %q: err = %v, want errZipSlip", name, err)
			}
			// Nothing may have escaped root, in particular into its parent.
			if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err == nil {
				t.Fatalf("entry %q wrote outside root", name)
			}
			if _, err := os.Stat(`C:\evil.txt`); err == nil {
				t.Fatalf("entry %q wrote to the drive root", name)
			}
		})
	}
}

func TestSafeZipRel(t *testing.T) {
	root := t.TempDir()
	ok := map[string]string{
		"a.txt":         "a.txt",
		"a/b/c.txt":     filepath.Join("a", "b", "c.txt"),
		"./a//b/":       filepath.Join("a", "b"),
		"":              "",
		"./":            "",
		"a/./b":         filepath.Join("a", "b"),
		"CON/x.txt":     filepath.Join("_CON", "x.txt"),
		"trail. /x":     filepath.Join("trail", "x"),
		"back\\slash":   filepath.Join("back", "slash"),
		"dots.../f.txt": filepath.Join("dots", "f.txt"),
	}
	for in, want := range ok {
		got, err := safeZipRel(root, in)
		if err != nil || got != want {
			t.Errorf("safeZipRel(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "../x", "a/../../x", "/x", "C:/x", `D:\x`, "C:x"} {
		if _, err := safeZipRel(root, in); !errors.Is(err, errZipSlip) {
			t.Errorf("safeZipRel(%q) err = %v, want errZipSlip", in, err)
		}
	}
}

func TestReceiveFolderCleansUpOnBadArchive(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "evil.zip")
	writeZip(t, zp, [][2]string{{"good.txt", "x"}, {"../evil.txt", "pwned"}})
	f, err := os.Open(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	dest := filepath.Join(dir, "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := receiveFolder(context.Background(), dest, "photos", f, 0); !errors.Is(err, errZipSlip) {
		t.Fatalf("receiveFolder err = %v, want errZipSlip", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("dest not cleaned after failed extraction: %v", names)
	}
}

// TestExtractZipBudget: an archive that inflates past the budget derived from
// the announced size is refused (decompression-bomb cap), while one within it
// extracts in full. The budget is enforced on the bytes actually written, not
// on the header the peer wrote.
func TestExtractZipBudget(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "big.zip")
	// ~1.2 MiB of zeros deflates to a few KB: the "bomb" shape, in miniature.
	big := make([]byte, 1200*1024)
	writeZip(t, zp, [][2]string{{"a.txt", "x"}, {"zeros.bin", string(big)}})

	root := filepath.Join(dir, "tight")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// Budget of 1 MiB total: a.txt fits, zeros.bin does not.
	if err := extractZip(context.Background(), zp, root, 1<<20); !errors.Is(err, errArchiveTooBig) {
		t.Fatalf("extractZip err = %v, want errArchiveTooBig", err)
	}

	root2 := filepath.Join(dir, "roomy")
	if err := os.Mkdir(root2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(context.Background(), zp, root2, extractBudget(int64(len(big))+1)); err != nil {
		t.Fatalf("extractZip within budget: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root2, "zeros.bin")); len(b) != len(big) {
		t.Fatalf("zeros.bin = %d bytes, want %d", len(b), len(big))
	}

	// receiveFolder with a lying offer (announces 0 bytes) still caps at the
	// slack, and the claimed folder is gone afterwards.
	f, err := os.Open(zp)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	huge := filepath.Join(dir, "huge.zip")
	writeZip(t, huge, [][2]string{{"zeros.bin", string(make([]byte, extractBudgetSlack+1024))}})
	hf, err := os.Open(huge)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hf.Close() }()
	dest := filepath.Join(dir, "dest")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := receiveFolder(context.Background(), dest, "bomb", hf, 0); !errors.Is(err, errArchiveTooBig) {
		t.Fatalf("receiveFolder err = %v, want errArchiveTooBig", err)
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 0 {
		t.Fatalf("dest not cleaned after refused archive: %d entries", len(entries))
	}
}

// TestReceiveFileClaimsBeforeCopy: the final name is reserved before any byte
// is read, so an unwritable name fails without consuming the transfer, and a
// failed copy leaves neither the placeholder nor the .part behind.
func TestReceiveFileClaimsBeforeCopy(t *testing.T) {
	dir := t.TempDir()
	// A reader that fails after a few bytes stands in for a dropped sender.
	r := &failingReader{data: []byte("partial"), fail: errors.New("boom")}
	if _, err := receiveFile(context.Background(), dir, "doc.txt", r); err == nil {
		t.Fatal("receiveFile succeeded on a failing reader")
	}
	if r.reads == 0 {
		t.Fatal("reader was never read")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		names := []string{}
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("leftovers after failed receive: %v", names)
	}
	// An unclaimable name: a directory already occupies every candidate slot
	// up to the suffix bound is impractical, so use a dir that cannot be
	// written into instead — a FILE where the Save-to folder should be.
	notDir := filepath.Join(dir, "file-not-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r2 := &failingReader{data: []byte("never"), fail: nil}
	if _, err := receiveFile(context.Background(), notDir, "doc.txt", r2); err == nil {
		t.Fatal("receiveFile succeeded with a file as the destination dir")
	}
	if r2.reads != 0 {
		t.Fatalf("reader consumed (%d reads) although the name could not be claimed", r2.reads)
	}
}

// failingReader yields data then fails with fail (or io.EOF when fail is nil).
type failingReader struct {
	data  []byte
	fail  error
	reads int
	off   int
}

func (f *failingReader) Read(p []byte) (int, error) {
	f.reads++
	if f.off >= len(f.data) {
		if f.fail != nil {
			return 0, f.fail
		}
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func TestReceiveFileNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "src.bin")
	if err := os.WriteFile(src, []byte("incoming"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	out, err := receiveFile(context.Background(), dir, "doc.txt", f)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(out) != "doc (2).txt" {
		t.Fatalf("out = %q, want doc (2).txt", out)
	}
	if b, _ := os.ReadFile(existing); string(b) != "original" {
		t.Fatalf("existing file was overwritten: %q", b)
	}
	if b, _ := os.ReadFile(out); string(b) != "incoming" {
		t.Fatalf("received content = %q", b)
	}
	// No .part left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".part" {
			t.Fatalf("stray spool file %s", e.Name())
		}
	}
}
