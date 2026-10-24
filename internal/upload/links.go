package upload

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// linkKey identifies one shared version of a clip: the file, the shared range
// and the file's modification time (so an edited file is shared afresh).
type linkKey struct {
	Path    string `json:"path"`
	StartMs int    `json:"startMs"`
	EndMs   int    `json:"endMs"` // 0 = to the end
	ModTime int64  `json:"modTime"`
}

type linkRecord struct {
	linkKey
	URL      string    `json:"url"`
	SharedAt time.Time `json:"sharedAt"`
}

// maxLinks bounds %AppData%\toru\links.json.
const maxLinks = 1000

var linksMu sync.Mutex

func linksPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "toru", "links.json"), nil
}

func loadLinks() []linkRecord {
	p, err := linksPath()
	if err != nil {
		return nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var recs []linkRecord
	_ = json.Unmarshal(raw, &recs)
	return recs
}

func sameKey(a, b linkKey) bool {
	return strings.EqualFold(a.Path, b.Path) && a.StartMs == b.StartMs && a.EndMs == b.EndMs && a.ModTime == b.ModTime
}

// lookupLink returns the saved link for k, or "".
func lookupLink(k linkKey) string {
	linksMu.Lock()
	defer linksMu.Unlock()
	for _, r := range loadLinks() {
		if sameKey(r.linkKey, k) {
			return r.URL
		}
	}
	return ""
}

// saveLink records a new link (newest first, bounded). A whole-clip share is
// stored with EndMs 0 so LinkFor finds it regardless of the probed duration.
func saveLink(k linkKey, url string) {
	linksMu.Lock()
	defer linksMu.Unlock()
	p, err := linksPath()
	if err != nil {
		return
	}
	recs := append([]linkRecord{{linkKey: k, URL: url, SharedAt: time.Now()}}, loadLinks()...)
	if len(recs) > maxLinks {
		recs = recs[:maxLinks]
	}
	raw, err := json.MarshalIndent(recs, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	tmp := p + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) == nil {
		_ = os.Rename(tmp, p)
	}
}
