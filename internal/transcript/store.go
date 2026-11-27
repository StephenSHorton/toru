// Package transcript keeps voice-call transcripts and runs the local
// speech-to-text engine (engine.go) that makes them.
//
// A transcript is one call on this PC: from the first time transcription is
// turned on in a call to hanging up. It is two files in Toru's data folder
// (%APPDATA%\toru\transcripts): <id>.json (title, times, people) and
// <id>.jsonl (one entry per line, appended as each arrives, so a crash
// loses nothing). Entries are final lines (speaker, start and end time,
// text) and the "transcription started/stopped by" marks everyone saw.
package transcript

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry kinds.
const (
	KindLine    = "line"    // someone said Text
	KindStarted = "started" // Speaker turned transcription on
	KindStopped = "stopped" // Speaker turned transcription off
)

// MaxTextRunes caps one line's text (a long monologue is cut into segments
// well before this).
const MaxTextRunes = 2000

// Entry is one line of a transcript.
type Entry struct {
	N         int    `json:"n"`    // 1, 2, 3... in the order this PC got them; the cursor for Live
	Kind      string `json:"kind"` // KindLine, KindStarted, KindStopped
	At        int64  `json:"at"`   // when it was said (unix ms)
	End       int64  `json:"end,omitempty"`
	Speaker   string `json:"speaker"` // the person's name in the room
	SpeakerID string `json:"speakerId,omitempty"`
	Text      string `json:"text,omitempty"`
	ID        string `json:"id,omitempty"` // the sender's line id (duplicates are dropped)
}

// Meta is a transcript's summary.
type Meta struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`           // what lists show (the custom title, or one made from the people and date)
	Custom    bool     `json:"custom"`          // Title was set by hand
	StartedAt int64    `json:"startedAt"`       // unix ms
	EndedAt   int64    `json:"endedAt"`         // unix ms; 0 while the call goes on
	People    []string `json:"people"`          // everyone heard, and who turned transcription on or off
	Lines     int      `json:"lines"`           // spoken lines
	Live      bool     `json:"live"`            // the call in progress on this PC
	DurationS int64    `json:"durationSeconds"` // EndedAt (or now) - StartedAt
}

type metaFile struct {
	ID        string   `json:"id"`
	Title     string   `json:"title,omitempty"` // "" = made from People and StartedAt
	StartedAt int64    `json:"startedAt"`
	EndedAt   int64    `json:"endedAt,omitempty"`
	People    []string `json:"people"`
	Lines     int      `json:"lines"`
	Self      string   `json:"self,omitempty"` // this PC's own name at the time
}

// Store is the transcripts folder.
type Store struct {
	dir string

	mu      sync.Mutex
	active  string // the transcript of the call in progress, or ""
	meta    metaFile
	entries []Entry
	ids     map[string]bool
	onEntry func(id string, e Entry)
}

// DefaultDir is %APPDATA%\toru\transcripts.
func DefaultDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru", "transcripts"), nil
}

// NewStore returns the store at dir (created on first write).
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// OnEntry installs a callback for every entry added to the active
// transcript (the live captions).
func (s *Store) OnEntry(fn func(id string, e Entry)) {
	s.mu.Lock()
	s.onEntry = fn
	s.mu.Unlock()
}

var errNotFound = errors.New("no transcript with that id")

// IsNotFound reports whether err is a missing transcript.
func IsNotFound(err error) bool { return errors.Is(err, errNotFound) }

func validID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r != '-' && (r < '0' || r > '9') && (r < 'a' || r > 'z') {
			return false
		}
	}
	return true
}

func (s *Store) metaPath(id string) string  { return filepath.Join(s.dir, id+".json") }
func (s *Store) linesPath(id string) string { return filepath.Join(s.dir, id+".jsonl") }

// Begin starts a transcript for the call in progress and makes it active,
// unless one already is (then that id is returned). self is this PC's name.
func (s *Store) Begin(self string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" {
		return s.active, nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return "", err
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	id := now.Format("20060102-150405") + "-" + hex.EncodeToString(b)
	m := metaFile{ID: id, StartedAt: now.UnixMilli(), People: []string{}, Self: self}
	if err := s.writeMeta(m); err != nil {
		return "", err
	}
	s.active, s.meta, s.entries, s.ids = id, m, nil, map[string]bool{}
	return id, nil
}

// Active is the transcript of the call in progress, or "".
func (s *Store) Active() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Add appends one entry to the active transcript and returns it numbered.
// ok is false when nothing is active or the entry is a duplicate.
func (s *Store) Add(e Entry) (Entry, bool) {
	s.mu.Lock()
	if s.active == "" {
		s.mu.Unlock()
		return Entry{}, false
	}
	if e.ID != "" {
		if s.ids[e.ID] {
			s.mu.Unlock()
			return Entry{}, false
		}
		s.ids[e.ID] = true
	}
	e.Speaker = clipRunes(strings.TrimSpace(e.Speaker), 64)
	if e.Speaker == "" {
		e.Speaker = "Someone"
	}
	e.Text = clipRunes(strings.TrimSpace(e.Text), MaxTextRunes)
	e.N = len(s.entries) + 1
	s.entries = append(s.entries, e)
	if !contains(s.meta.People, e.Speaker) {
		s.meta.People = append(s.meta.People, e.Speaker)
	}
	if e.Kind == KindLine {
		s.meta.Lines++
	}
	id, m := s.active, s.meta
	err := s.appendLine(id, e)
	if err == nil {
		err = s.writeMeta(m)
	}
	fn := s.onEntry
	s.mu.Unlock()
	if err != nil {
		log.Printf("transcript: save: %v", err)
	}
	if fn != nil {
		fn(id, e)
	}
	return e, true
}

// End closes the active transcript (the call was hung up).
func (s *Store) End(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == "" {
		return
	}
	s.meta.EndedAt = now.UnixMilli()
	if err := s.writeMeta(s.meta); err != nil {
		log.Printf("transcript: save: %v", err)
	}
	s.active, s.entries, s.ids = "", nil, nil
}

// Live returns the active transcript's id and its entries after cursor
// (an entry's N; 0 for all), plus the cursor to pass next time.
func (s *Store) Live(cursor int) (id string, entries []Entry, next int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == "" {
		return "", []Entry{}, cursor
	}
	cursor = max(cursor, 0)
	out := []Entry{}
	if cursor < len(s.entries) {
		out = append(out, s.entries[cursor:]...)
	}
	return s.active, out, len(s.entries)
}

func (s *Store) writeMeta(m metaFile) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.metaPath(m.ID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.metaPath(m.ID))
}

func (s *Store) appendLine(id string, e Entry) error {
	f, err := os.OpenFile(s.linesPath(id), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(e)
	_, err = f.Write(append(raw, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

func (s *Store) readMeta(id string) (metaFile, error) {
	var m metaFile
	if !validID(id) {
		return m, errNotFound
	}
	raw, err := os.ReadFile(s.metaPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return m, errNotFound
	}
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, err
	}
	m.ID = id
	if m.People == nil {
		m.People = []string{}
	}
	return m, nil
}

func (s *Store) readEntries(id string) ([]Entry, error) {
	f, err := os.Open(s.linesPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := []Entry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

func (s *Store) summary(m metaFile, now time.Time) Meta {
	s.mu.Lock()
	live := s.active == m.ID
	s.mu.Unlock()
	end := m.EndedAt
	if end == 0 {
		end = now.UnixMilli()
		if !live && m.StartedAt > 0 {
			end = m.StartedAt // never closed (Toru quit mid-call): no length to show
		}
	}
	out := Meta{ID: m.ID, Title: m.Title, Custom: m.Title != "", StartedAt: m.StartedAt, EndedAt: m.EndedAt,
		People: m.People, Lines: m.Lines, Live: live, DurationS: max(end-m.StartedAt, 0) / 1000}
	if out.Title == "" {
		out.Title = autoTitle(m)
	}
	return out
}

// autoTitle is "Call with Alice and Bob · Nov 27, 2026 2:03 PM".
func autoTitle(m metaFile) string {
	var others []string
	for _, p := range m.People {
		if p != m.Self {
			others = append(others, p)
		}
	}
	who := "Call"
	switch len(others) {
	case 0:
	case 1:
		who = "Call with " + others[0]
	case 2:
		who = "Call with " + others[0] + " and " + others[1]
	default:
		who = fmt.Sprintf("Call with %s, %s and %d more", others[0], others[1], len(others)-2)
	}
	return who + " · " + time.UnixMilli(m.StartedAt).Format("Jan 2, 2006 3:04 PM")
}

// List returns every transcript, newest first.
func (s *Store) List() ([]Meta, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Meta{}, nil
	}
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []Meta{}
	for _, en := range entries {
		name := en.Name()
		if en.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		m, err := s.readMeta(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		out = append(out, s.summary(m, now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out, nil
}

// Get returns one transcript's summary.
func (s *Store) Get(id string) (Meta, error) {
	m, err := s.readMeta(id)
	if err != nil {
		return Meta{}, err
	}
	return s.summary(m, time.Now()), nil
}

// Range picks part of a transcript. Zero values mean no limit. Lines are
// counted 1, 2, 3... in time order; seconds are from the transcript's start.
type Range struct {
	FromLine int     `json:"fromLine"`
	ToLine   int     `json:"toLine"`
	FromSec  float64 `json:"fromSeconds"`
	ToSec    float64 `json:"toSeconds"`
}

// Line is an entry as Read returns it: in time order, with its place.
type Line struct {
	Line   int     `json:"line"`   // 1, 2, 3... in time order
	Offset float64 `json:"offset"` // seconds from the transcript's start
	Time   string  `json:"time"`   // Offset as m:ss or h:mm:ss
	Entry
}

// Read returns a transcript's summary and its entries in time order, cut to r.
func (s *Store) Read(id string, r Range) (Meta, []Line, error) {
	m, err := s.readMeta(id)
	if err != nil {
		return Meta{}, nil, err
	}
	entries, err := s.readEntries(id)
	if err != nil {
		return Meta{}, nil, err
	}
	lines := ordered(m, entries)
	out := []Line{}
	for _, l := range lines {
		if r.FromLine > 0 && l.Line < r.FromLine || r.ToLine > 0 && l.Line > r.ToLine {
			continue
		}
		if r.FromSec > 0 && l.Offset < r.FromSec || r.ToSec > 0 && l.Offset > r.ToSec {
			continue
		}
		out = append(out, l)
	}
	return s.summary(m, time.Now()), out, nil
}

func ordered(m metaFile, entries []Entry) []Line {
	sorted := append([]Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].At < sorted[j].At })
	out := make([]Line, len(sorted))
	for i, e := range sorted {
		off := float64(max(e.At-m.StartedAt, 0)) / 1000
		out[i] = Line{Line: i + 1, Offset: off, Time: clock(off), Entry: e}
	}
	return out
}

// clock formats seconds as m:ss, or h:mm:ss from an hour.
func clock(sec float64) string {
	t := int64(sec)
	h, m, s := t/3600, t/60%60, t%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// Hit is one search match.
type Hit struct {
	Transcript string `json:"transcript"` // its id
	Title      string `json:"title"`
	StartedAt  int64  `json:"startedAt"`
	Line
}

// Search finds spoken lines containing every word of query (any case),
// across all transcripts, newest transcript first. limit <= 0 means 50.
func (s *Store) Search(query string, limit int) ([]Hit, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, errors.New("search for some words")
	}
	if limit <= 0 {
		limit = 50
	}
	list, err := s.List()
	if err != nil {
		return nil, err
	}
	out := []Hit{}
	for _, t := range list {
		m, err := s.readMeta(t.ID)
		if err != nil {
			continue
		}
		entries, err := s.readEntries(t.ID)
		if err != nil {
			continue
		}
		for _, l := range ordered(m, entries) {
			if l.Kind != KindLine {
				continue
			}
			text := strings.ToLower(l.Text + " " + l.Speaker)
			all := true
			for _, w := range words {
				all = all && strings.Contains(text, w)
			}
			if !all {
				continue
			}
			out = append(out, Hit{Transcript: t.ID, Title: t.Title, StartedAt: t.StartedAt, Line: l})
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

// Rename sets a transcript's title ("" goes back to the made-up one).
func (s *Store) Rename(id, title string) (Meta, error) {
	title = clipRunes(strings.TrimSpace(title), 120)
	m, err := s.rename(id, title)
	if err != nil {
		return Meta{}, err
	}
	return s.summary(m, time.Now()), nil
}

func (s *Store) rename(id, title string) (metaFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" && id == s.active {
		s.meta.Title = title
		return s.meta, s.writeMeta(s.meta)
	}
	m, err := s.readMeta(id)
	if err != nil {
		return m, err
	}
	m.Title = title
	return m, s.writeMeta(m)
}

// Delete removes a transcript. The call in progress's can't be deleted
// until it ends.
func (s *Store) Delete(id string) error {
	if _, err := s.readMeta(id); err != nil {
		return err
	}
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if id == active {
		return errors.New("that's the transcript of the call in progress: hang up first")
	}
	if err := os.Remove(s.linesPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Remove(s.metaPath(id))
}

// Export renders a transcript as "md" (Markdown) or "txt".
func (s *Store) Export(id, format string) (string, error) {
	m, lines, err := s.Read(id, Range{})
	if err != nil {
		return "", err
	}
	md := format == "md" || format == "markdown"
	if !md && format != "txt" && format != "text" {
		return "", errors.New(`format is "md" or "txt"`)
	}
	var b strings.Builder
	date := time.UnixMilli(m.StartedAt).Format("Monday, January 2, 2006 3:04 PM")
	if md {
		fmt.Fprintf(&b, "# %s\n\n", m.Title)
		fmt.Fprintf(&b, "%s · %s · %s\n\n", date, strings.Join(m.People, ", "), clock(float64(m.DurationS)))
	} else {
		fmt.Fprintf(&b, "%s\n%s · %s · %s\n\n", m.Title, date, strings.Join(m.People, ", "), clock(float64(m.DurationS)))
	}
	for _, l := range lines {
		switch l.Kind {
		case KindLine:
			if md {
				fmt.Fprintf(&b, "**%s** [%s]: %s\n\n", l.Speaker, l.Time, l.Text)
			} else {
				fmt.Fprintf(&b, "[%s] %s: %s\n", l.Time, l.Speaker, l.Text)
			}
		case KindStarted, KindStopped:
			what := "started"
			if l.Kind == KindStopped {
				what = "stopped"
			}
			if md {
				fmt.Fprintf(&b, "_[%s] %s %s transcription._\n\n", l.Time, l.Speaker, what)
			} else {
				fmt.Fprintf(&b, "[%s] (%s %s transcription)\n", l.Time, l.Speaker, what)
			}
		}
	}
	return b.String(), nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

func logf(format string, args ...any) { log.Printf("transcript: "+format, args...) }
