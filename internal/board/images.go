package board

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif" // DecodeConfig for the formats boards take
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
)

// Board images: the pictures image blocks show. Each one is copied into
// %AppData%\toru\boards\images and named by its content's SHA-256, so the
// same picture is kept once however many blocks show it, and a block keeps
// its picture after the Library deletes the screenshot it came from. An
// image block's props name it in "asset".

// MaxImageBytes is the largest picture a board takes.
const MaxImageBytes = 32 << 20

// Served paths: a board image, and a Library screenshot for the window's
// picker (only items the Library lists as images).
const (
	imageURLPrefix   = "/__board/image/"
	libraryURLPrefix = "/__board/library/"
)

var assetPattern = regexp.MustCompile(`^[0-9a-f]{64}\.(png|jpg|gif|webp)$`)

var imageExts = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// ImagesDir is where board images are kept.
func ImagesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "images"), nil
}

// ImageURL is the address the board window shows a board image at.
func ImageURL(asset string) string { return imageURLPrefix + asset }

// ImagePath is a board image's file, for a valid name.
func ImagePath(asset string) (string, error) {
	if !assetPattern.MatchString(asset) {
		return "", fmt.Errorf("%q isn't a board image name", asset)
	}
	dir, err := ImagesDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, asset), nil
}

// Image is a picture copied into the board images.
type Image struct {
	Asset  string `json:"asset"` // the name an image block's props take
	URL    string `json:"url"`   // where the window shows it
	Width  int    `json:"width"` // pixels; 0 when it can't be read
	Height int    `json:"height"`
}

// ImportImage copies a PNG, JPEG, GIF or WebP picture into the board
// images and names it. Importing the same picture again gives the same name.
func ImportImage(data []byte) (Image, error) {
	if len(data) == 0 {
		return Image{}, errors.New("that file is empty")
	}
	if len(data) > MaxImageBytes {
		return Image{}, fmt.Errorf("that picture is over %d MB", MaxImageBytes>>20)
	}
	asset := imageName(data)
	if asset == "" {
		return Image{}, errors.New("that file isn't a PNG, JPEG, GIF or WebP picture")
	}
	path, err := ImagePath(asset)
	if err != nil {
		return Image{}, err
	}
	if _, err := os.Stat(path); err != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return Image{}, err
		}
		tmp, err := os.CreateTemp(filepath.Dir(path), "import-*")
		if err != nil {
			return Image{}, err
		}
		_, werr := tmp.Write(data)
		cerr := tmp.Close()
		if werr == nil {
			werr = cerr
		}
		if werr == nil {
			werr = os.Rename(tmp.Name(), path)
		}
		if werr != nil {
			_ = os.Remove(tmp.Name())
			return Image{}, werr
		}
	}
	img := Image{Asset: asset, URL: ImageURL(asset)}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(data)); err == nil {
		img.Width, img.Height = cfg.Width, cfg.Height
	}
	return img, nil
}

// imageName is the name ImportImage gives a picture: the SHA-256 of its
// bytes and its type's extension, or "" for a file that isn't a picture it
// takes.
func imageName(data []byte) string {
	ext, ok := imageExts[http.DetectContentType(data)]
	if !ok {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "." + ext
}

// ImportImageFile imports the picture at path.
func ImportImageFile(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, MaxImageBytes+1))
	if err != nil {
		return Image{}, err
	}
	return ImportImage(data)
}

// LibraryItem is one Toru Library screenshot, as the image service sees it.
type LibraryItem struct {
	ID      string
	Label   string
	Path    string
	TakenAt time.Time
}

// LibraryImage is a Library screenshot offered to an image block.
type LibraryImage struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	TakenAt time.Time `json:"takenAt"`
	URL     string    `json:"url"` // a preview, served while the Library keeps it
}

// ImageService is the board window's way to put pictures in image blocks:
// a file people choose or drop, or a Toru Library screenshot.
type ImageService struct {
	// library lists the Library's screenshots, newest first.
	library func() []LibraryItem
}

// NewImageService gives the window the Library's screenshots through
// library (newest first, images only).
func NewImageService(library func() []LibraryItem) *ImageService {
	return &ImageService{library: library}
}

func (s *ImageService) find(id string) (LibraryItem, bool) {
	if s.library == nil {
		return LibraryItem{}, false
	}
	for _, it := range s.library() {
		if it.ID == id {
			return it, true
		}
	}
	return LibraryItem{}, false
}

// ImportData imports a picture the window read from a chosen or dropped
// file, sent as base64.
func (s *ImageService) ImportData(dataBase64 string) (Image, error) {
	if len(dataBase64) > base64.StdEncoding.EncodedLen(MaxImageBytes) {
		return Image{}, fmt.Errorf("that picture is over %d MB", MaxImageBytes>>20)
	}
	data, err := base64.StdEncoding.DecodeString(dataBase64)
	if err != nil {
		return Image{}, errors.New("the picture didn't arrive whole")
	}
	return ImportImage(data)
}

// LibraryImages lists the Library's screenshots, newest first.
func (s *ImageService) LibraryImages() []LibraryImage {
	out := []LibraryImage{}
	if s.library == nil {
		return out
	}
	for _, it := range s.library() {
		out = append(out, LibraryImage{ID: it.ID, Label: it.Label, TakenAt: it.TakenAt, URL: libraryURLPrefix + it.ID})
	}
	return out
}

// ImportLibrary copies a Library screenshot into the board images, by its
// Library id.
func (s *ImageService) ImportLibrary(id string) (Image, error) {
	it, ok := s.find(id)
	if !ok {
		return Image{}, fmt.Errorf("the Library has no screenshot %q", id)
	}
	return ImportImageFile(it.Path)
}

// ServeHTTP serves board images at /__board/image/<name> and the Library's
// screenshots at /__board/library/<id>. Go-only.
//
//wails:ignore
func (s *ImageService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var path string
	if name, ok := strings.CutPrefix(r.URL.Path, imageURLPrefix); ok {
		p, err := ImagePath(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		path = p
		w.Header().Set("Cache-Control", "max-age=31536000, immutable") // named by content
	} else if id, ok := strings.CutPrefix(r.URL.Path, libraryURLPrefix); ok {
		it, found := s.find(id)
		if !found {
			http.NotFound(w, r)
			return
		}
		path = it.Path
		w.Header().Set("Cache-Control", "no-store")
	} else {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

// Middleware hands /__board/ requests to ServeHTTP and passes the rest on.
//
//wails:ignore
func (s *ImageService) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, imageURLPrefix) || strings.HasPrefix(r.URL.Path, libraryURLPrefix) {
			s.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
