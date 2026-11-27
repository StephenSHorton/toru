package board

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const samplePicture = "334fac5220a72cdbb351ea83b8e4a5d48d7bb81d438ba78bf94cd863669c346b.png"

func useTempBoards(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := Dir
	Dir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { Dir = old })
	return dir
}

func TestImportImageFile(t *testing.T) {
	dir := useTempBoards(t)
	img, err := ImportImageFile(filepath.Join("testdata", "images", samplePicture))
	if err != nil {
		t.Fatal(err)
	}
	if img.Asset != samplePicture || img.URL != "/__board/image/"+samplePicture || img.Width != 1280 || img.Height != 800 {
		t.Fatalf("got %+v", img)
	}
	if _, err := os.Stat(filepath.Join(dir, "images", samplePicture)); err != nil {
		t.Fatal("not copied into the board images:", err)
	}
	again, err := ImportImageFile(filepath.Join("testdata", "images", samplePicture))
	if err != nil || again.Asset != img.Asset {
		t.Fatalf("the same picture twice: %+v, %v", again, err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "images"))
	if len(entries) != 1 {
		t.Fatalf("%d files in images, want 1 (no temp files left)", len(entries))
	}
}

func TestImportImageRefuses(t *testing.T) {
	useTempBoards(t)
	for name, data := range map[string][]byte{
		"empty":    {},
		"text":     []byte("not a picture at all"),
		"svg":      []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"too big":  append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, MaxImageBytes)...),
		"html":     []byte("<html><script>alert(1)</script></html>"),
		"riff wav": []byte("RIFF\x00\x00\x00\x00WAVEfmt "),
	} {
		if _, err := ImportImage(data); err == nil {
			t.Errorf("%s: imported", name)
		}
	}
	for _, bad := range []string{"../x.png", "abc.png", samplePicture + "/..", strings.ToUpper(samplePicture), strings.TrimSuffix(samplePicture, ".png") + ".svg"} {
		if _, err := ImagePath(bad); err == nil {
			t.Errorf("ImagePath(%q) accepted", bad)
		}
	}
}

func TestImageService(t *testing.T) {
	useTempBoards(t)
	shot := filepath.Join("testdata", "images", samplePicture)
	svc := NewImageService(func() []LibraryItem {
		return []LibraryItem{{ID: "shot1", Label: "Screenshot", Path: shot, TakenAt: time.Unix(1, 0)}}
	})

	lib := svc.LibraryImages()
	if len(lib) != 1 || lib[0].ID != "shot1" || lib[0].URL != "/__board/library/shot1" {
		t.Fatalf("library: %+v", lib)
	}
	img, err := svc.ImportLibrary("shot1")
	if err != nil || img.Asset != samplePicture {
		t.Fatalf("import from library: %+v, %v", img, err)
	}
	if _, err := svc.ImportLibrary("nope"); err == nil {
		t.Fatal("imported a screenshot the Library doesn't have")
	}

	data, _ := os.ReadFile(shot)
	img, err = svc.ImportData(base64.StdEncoding.EncodeToString(data))
	if err != nil || img.Asset != samplePicture {
		t.Fatalf("import data: %+v, %v", img, err)
	}
	if _, err := svc.ImportData("!!not base64"); err == nil {
		t.Fatal("bad base64 imported")
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := svc.Middleware(next)
	for path, want := range map[string]int{
		"/__board/image/" + samplePicture:                    http.StatusOK,
		"/__board/library/shot1":                             http.StatusOK,
		"/__board/library/nope":                              http.StatusNotFound,
		"/__board/image/../../secret.png":                    http.StatusNotFound,
		"/__board/image/abc.png":                             http.StatusNotFound,
		"/__board/image/" + strings.Repeat("0", 64) + ".png": http.StatusNotFound,
		"/somewhere/else":                                    http.StatusTeapot,
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", path, rec.Code, want)
		}
		if want == http.StatusOK && rec.Header().Get("Content-Type") != "image/png" {
			t.Errorf("GET %s: content type %q", path, rec.Header().Get("Content-Type"))
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/__board/image/"+samplePicture, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST = %d", rec.Code)
	}
}
