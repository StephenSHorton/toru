// Package media is the video editor's media bin backend (docs/video-editor-v2.md,
// "Media serving and import"): it registers files the user imports (dragged
// from Explorer or picked in a dialog) or adds from the Library, probes them
// and serves them to the editor window at /__media/<id>/<name>.
//
// /__file/ only serves Toru's temp and Library folders; imported files can be
// anywhere, so only paths that went through Register are ever served. Files
// are used where they are; nothing is copied.
//
// JS binding name: MediaService.*
package media

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/StephenSHorton/toru/internal/vid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// EventDropped carries files dropped on the edit window ([]string paths) to
// the page, which registers them (windows.go forwards WindowFilesDropped).
const EventDropped = "media:dropped"

// Kind is what a media file holds.
type Kind string

const (
	KindVideo Kind = "video"
	KindImage Kind = "image"
	KindAudio Kind = "audio"
)

// Media is one registered file, as the editor's project stores it
// (frontend/src/trim/project.ts Media).
type Media struct {
	ID       string           `json:"id"`       // stable for a path
	Path     string           `json:"path"`     // absolute
	URL      string           `json:"url"`      // /__media/<id>/<name>
	Kind     Kind             `json:"kind"`     //
	Duration float64          `json:"duration"` // seconds; 0 for images
	Width    int              `json:"width"`    // 0 for audio
	Height   int              `json:"height"`   //
	FPS      float64          `json:"fps"`      // 0 for image/audio
	HasAudio bool             `json:"hasAudio"` //
	Tracks   []vid.AudioTrack `json:"tracks"`   // audio streams, titles, mix flag
}

// Extensions the bin accepts, by kind. A video container with no picture
// (an .m4a in .mp4 clothing) registers as audio.
var (
	videoExts = []string{".mp4", ".mkv", ".mov", ".webm"}
	audioExts = []string{".mp3", ".wav", ".m4a", ".aac", ".ogg", ".flac"}
	imageExts = []string{".png", ".jpg", ".jpeg", ".webp", ".gif"}
)

// Service is the Wails-bound media bin API.
type Service struct {
	mu    sync.RWMutex
	items map[string]Media // by ID
}

// New returns an empty media registry.
func New() *Service { return &Service{items: map[string]Media{}} }

// Register probes each path and makes it servable, returning the files that
// worked in the order given. Unsupported or unreadable files are left out
// (the page says how many were skipped). Registering a path again returns
// the same ID and re-probes it.
func (s *Service) Register(paths []string) []Media {
	type result struct {
		m  Media
		ok bool
	}
	results := make([]result, len(paths))
	sem := make(chan struct{}, 4) // ffprobe is the slow part; a few at a time
	var wg sync.WaitGroup
	for i, p := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := probeFile(p)
			results[i] = result{m, err == nil}
		}()
	}
	wg.Wait()

	out := []Media{}
	seen := map[string]bool{}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range results {
		if !r.ok || seen[r.m.ID] {
			continue
		}
		seen[r.m.ID] = true
		s.items[r.m.ID] = r.m
		out = append(out, r.m)
	}
	return out
}

// PickMedia opens a multi-select file dialog for video, audio and images
// and registers the chosen files. Cancel returns an empty list.
func (s *Service) PickMedia() ([]Media, error) {
	app := application.Get()
	if app == nil {
		return nil, errors.New("import: no running application")
	}
	all := append(append(append([]string{}, videoExts...), audioExts...), imageExts...)
	dlg := app.Dialog.OpenFile().
		CanChooseFiles(true).
		SetTitle("Import media").
		AddFilter("Video, audio and images", patterns(all)).
		AddFilter("Video", patterns(videoExts)).
		AddFilter("Audio", patterns(audioExts)).
		AddFilter("Images", patterns(imageExts))
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}
	paths, err := dlg.PromptForMultipleSelection()
	if err != nil || len(paths) == 0 {
		return []Media{}, nil // cancelled
	}
	return s.Register(paths), nil
}

// Thumbnail returns the URL of a small JPEG for a registered file: a frame
// from 10% into a video, the picture itself for an image, "" for audio.
// Video thumbnails are made on first request and cached in the temp folder.
func (s *Service) Thumbnail(id string) (string, error) {
	m, ok := s.get(id)
	if !ok {
		return "", fmt.Errorf("thumbnail: no media %q", id)
	}
	switch m.Kind {
	case KindImage:
		return m.URL, nil
	case KindVideo:
		return thumbPrefix + id + ".jpg", nil
	}
	return "", nil
}

func (s *Service) get(id string) (Media, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.items[id]
	return m, ok
}

// mediaID is stable for a path (case-insensitive: Windows), so a project
// reopened later re-registers its files under the same IDs.
func mediaID(abs string) string {
	h := sha1.Sum([]byte(strings.ToLower(filepath.Clean(abs))))
	return hex.EncodeToString(h[:6])
}

func mediaURL(id, abs string) string {
	return filePrefix + id + "/" + url.PathEscape(filepath.Base(abs))
}

// kindByExt is the kind a file's extension promises ("" = not accepted).
func kindByExt(path string) Kind {
	ext := strings.ToLower(filepath.Ext(path))
	switch {
	case slices.Contains(videoExts, ext):
		return KindVideo
	case slices.Contains(audioExts, ext):
		return KindAudio
	case slices.Contains(imageExts, ext):
		return KindImage
	}
	return ""
}

func patterns(exts []string) string {
	ps := make([]string, len(exts))
	for i, e := range exts {
		ps[i] = "*" + e
	}
	return strings.Join(ps, ";")
}

// absFile resolves path and checks it is an existing regular file.
func absFile(path string) (string, os.FileInfo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	if !fi.Mode().IsRegular() {
		return "", nil, fmt.Errorf("%s is not a file", abs)
	}
	return abs, fi, nil
}
