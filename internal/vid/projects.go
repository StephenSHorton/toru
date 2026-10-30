package vid

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// projects.go keeps the v2 editor's projects (docs/video-editor-v2.md, v2
// scope 11): the edit window autosaves its Project as JSON to
// %AppData%\toru\projects\<id>.json while you work, and the Library's
// Projects tab lists them to reopen. The file is the frontend's Project as
// is (frontend/src/trim/project.ts, times in seconds); Go only reads the few
// fields the list shows. Media are referenced by path where they are, so a
// file moved or deleted since shows as missing when the project opens.

// ProjectSummary is one saved project as the Library lists it.
type ProjectSummary struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	UpdatedAt  time.Time `json:"updatedAt"`
	DurationMs int       `json:"durationMs"`
	Clips      int       `json:"clips"`
	// FirstMedia is the first video or image file in the project ("" if
	// none), for a thumbnail.
	FirstMedia string `json:"firstMedia"`
	// Missing counts media files that no longer exist where they were.
	Missing int `json:"missing"`
}

// projectsDir is %AppData%\toru\projects (os.UserConfigDir on Windows reads
// %AppData%, which the tests redirect). A var so tests can point it elsewhere.
var projectsDir = func() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru", "projects"), nil
}

// Project ids come from the frontend (newId("proj")): letters, digits and
// dashes only, so an id can never name a path outside the folder.
var projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,80}$`)

// projectHead is the part of a saved project the list reads.
type projectHead struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Media   []struct {
		ID   string `json:"id"`
		Path string `json:"path"`
		Kind string `json:"kind"`
	} `json:"media"`
	Tracks []struct {
		Clips []struct {
			Start float64 `json:"start"`
			In    float64 `json:"in"`
			Out   float64 `json:"out"`
			Speed float64 `json:"speed"`
		} `json:"clips"`
	} `json:"tracks"`
}

func projectPath(id string) (string, error) {
	if !projectIDPattern.MatchString(id) {
		return "", fmt.Errorf("bad project id %q", id)
	}
	dir, err := projectsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id+".json"), nil
}

// SaveProject writes a project (the frontend's Project as JSON) to its file,
// replacing the last save. The write goes to a temp file first, so a crash
// mid-save never leaves half a project.
func (s *VideoService) SaveProject(doc string) error {
	var h projectHead
	if err := json.Unmarshal([]byte(doc), &h); err != nil {
		return fmt.Errorf("project isn't valid JSON: %w", err)
	}
	if h.Version != 2 {
		return fmt.Errorf("unsupported project version %d", h.Version)
	}
	path, err := projectPath(h.ID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(doc), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// LoadProject returns a saved project's JSON.
func (s *VideoService) LoadProject(id string) (string, error) {
	path, err := projectPath(id)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("that project was deleted")
	}
	return string(b), err
}

// DeleteProject removes a saved project (not its media).
func (s *VideoService) DeleteProject(id string) error {
	path, err := projectPath(id)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RenameProject gives a saved project a new name ("" or blank is refused).
// Only the name changes; the rest of the file is kept as saved.
func (s *VideoService) RenameProject(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("give the project a name")
	}
	if len([]rune(name)) > 120 {
		return errors.New("that name is too long (120 characters at most)")
	}
	doc, err := s.LoadProject(id)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(doc), &fields); err != nil {
		return fmt.Errorf("project isn't valid JSON: %w", err)
	}
	b, err := json.Marshal(name)
	if err != nil {
		return err
	}
	fields["name"] = b
	out, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	return s.SaveProject(string(out))
}

// ListProjects lists the saved projects, most recently changed first.
// Unreadable files are skipped.
func (s *VideoService) ListProjects() ([]ProjectSummary, error) {
	dir, err := projectsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []ProjectSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []ProjectSummary{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		sum, ok := summarize(b, info.ModTime())
		if !ok {
			continue
		}
		out = append(out, sum)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// summarize reads a saved project's list entry.
func summarize(b []byte, mod time.Time) (ProjectSummary, bool) {
	var h projectHead
	if json.Unmarshal(b, &h) != nil || h.Version != 2 || !projectIDPattern.MatchString(h.ID) {
		return ProjectSummary{}, false
	}
	sum := ProjectSummary{ID: h.ID, Name: h.Name, UpdatedAt: mod}
	end := 0.0
	for _, t := range h.Tracks {
		for _, c := range t.Clips {
			sum.Clips++
			speed := c.Speed
			if speed <= 0 {
				speed = 1
			}
			end = max(end, c.Start+(c.Out-c.In)/speed)
		}
	}
	sum.DurationMs = int(end*1000 + 0.5)
	for _, m := range h.Media {
		if _, err := os.Stat(m.Path); err != nil {
			sum.Missing++
		} else if sum.FirstMedia == "" && m.Kind != "audio" {
			sum.FirstMedia = m.Path
		}
	}
	if sum.Name == "" {
		sum.Name = "Untitled"
	}
	return sum, true
}
