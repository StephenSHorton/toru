package transcript

import (
	"strings"
	"testing"
	"time"
)

func TestStoreLifecycle(t *testing.T) {
	s := NewStore(t.TempDir())
	var got []Entry
	s.OnEntry(func(_ string, e Entry) { got = append(got, e) })

	start := time.Date(2026, 11, 27, 14, 0, 0, 0, time.Local)
	if _, ok := s.Add(Entry{Kind: KindLine, Text: "nothing active"}); ok {
		t.Fatal("Add with no active transcript")
	}
	id, err := s.Begin("Alice", start)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Begin("Alice", start); again != id {
		t.Fatalf("Begin while active made a second transcript %q", again)
	}
	ms := start.UnixMilli()
	s.Add(Entry{Kind: KindStarted, At: ms, Speaker: "Alice", ID: "tx-1"})
	s.Add(Entry{Kind: KindLine, At: ms + 5000, End: ms + 7000, Speaker: "Bob", Text: "  second  ", ID: "b1"})
	s.Add(Entry{Kind: KindLine, At: ms + 2000, End: ms + 4000, Speaker: "Alice", Text: "first", ID: "a1"})
	if _, ok := s.Add(Entry{Kind: KindLine, At: ms + 2000, Speaker: "Alice", Text: "first", ID: "a1"}); ok {
		t.Error("duplicate line id was added")
	}
	if len(got) != 3 {
		t.Fatalf("OnEntry saw %d entries, want 3", len(got))
	}

	// Live: arrival order and a cursor.
	lid, entries, next := s.Live(0)
	if lid != id || len(entries) != 3 || next != 3 {
		t.Fatalf("Live(0) = %q, %d entries, cursor %d", lid, len(entries), next)
	}
	s.Add(Entry{Kind: KindLine, At: ms + 9000, Speaker: "Bob", Text: "third", ID: "b2"})
	_, entries, next = s.Live(next)
	if len(entries) != 1 || entries[0].Text != "third" || next != 4 {
		t.Fatalf("Live(3) = %+v, cursor %d", entries, next)
	}

	if err := s.Delete(id); err == nil {
		t.Error("deleted the transcript of the call in progress")
	}
	s.End(start.Add(90 * time.Second))
	if s.Active() != "" {
		t.Fatal("still active after End")
	}

	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("List = %v, %v", list, err)
	}
	m := list[0]
	if m.Lines != 3 || m.DurationS != 90 || m.Live || m.Custom {
		t.Errorf("meta = %+v", m)
	}
	if !strings.HasPrefix(m.Title, "Call with Bob · ") {
		t.Errorf("auto title %q", m.Title)
	}

	// Read: time order, ranges.
	_, lines, err := s.Read(id, Range{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, l := range lines {
		order = append(order, l.Kind+":"+l.Text)
	}
	if strings.Join(order, ",") != "started:,line:first,line:second,line:third" {
		t.Errorf("order = %v", order)
	}
	if lines[1].Time != "0:02" || lines[1].Line != 2 {
		t.Errorf("line 2 = %+v", lines[1])
	}
	_, part, _ := s.Read(id, Range{FromLine: 2, ToLine: 3})
	if len(part) != 2 || part[0].Text != "first" {
		t.Errorf("lines 2-3 = %+v", part)
	}
	_, part, _ = s.Read(id, Range{FromSec: 4.5})
	if len(part) != 2 || part[0].Text != "second" {
		t.Errorf("from 4.5 s = %+v", part)
	}

	hits, err := s.Search("SECOND bob", 0)
	if err != nil || len(hits) != 1 || hits[0].Transcript != id || hits[0].Line.Line != 3 {
		t.Errorf("Search = %+v, %v", hits, err)
	}
	if _, err := s.Search("  ", 0); err == nil {
		t.Error("empty search allowed")
	}

	if m, err := s.Rename(id, "Release planning"); err != nil || m.Title != "Release planning" || !m.Custom {
		t.Errorf("Rename = %+v, %v", m, err)
	}
	md, err := s.Export(id, "md")
	if err != nil || !strings.Contains(md, "# Release planning") || !strings.Contains(md, "**Bob** [0:05]: second") {
		t.Errorf("markdown export:\n%s\n%v", md, err)
	}
	txt, _ := s.Export(id, "txt")
	if !strings.Contains(txt, "[0:02] Alice: first") || !strings.Contains(txt, "(Alice started transcription)") {
		t.Errorf("text export:\n%s", txt)
	}
	if _, err := s.Export(id, "pdf"); err == nil {
		t.Error("unknown export format allowed")
	}

	if err := s.Delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(id); !IsNotFound(err) {
		t.Errorf("Get after delete: %v", err)
	}
	if _, _, err := s.Read("../../etc", Range{}); !IsNotFound(err) {
		t.Errorf("a path as an id: %v", err)
	}
}

func TestAutoTitle(t *testing.T) {
	at := time.Date(2026, 1, 2, 15, 4, 0, 0, time.Local).UnixMilli()
	for _, c := range []struct {
		people []string
		want   string
	}{
		{nil, "Call · "},
		{[]string{"Me"}, "Call · "},
		{[]string{"Me", "Ann"}, "Call with Ann · "},
		{[]string{"Ann", "Me", "Bo"}, "Call with Ann and Bo · "},
		{[]string{"Ann", "Bo", "Cy", "Di", "Me"}, "Call with Ann, Bo and 2 more · "},
	} {
		got := autoTitle(metaFile{People: c.people, Self: "Me", StartedAt: at})
		if !strings.HasPrefix(got, c.want) || !strings.HasSuffix(got, "Jan 2, 2026 3:04 PM") {
			t.Errorf("autoTitle(%v) = %q", c.people, got)
		}
	}
}
