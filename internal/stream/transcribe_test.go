package stream

import (
	"encoding/json"
	"testing"
	"time"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/transcript"
)

func newTranscribeService(t *testing.T) (*StreamService, *transcript.Store) {
	t.Helper()
	s := newTestService(t)
	store := transcript.NewStore(t.TempDir())
	s.setTranscripts(store, transcript.NewEngineAt("", nil)) // no download, no whisper
	s.mu.Lock()
	s.tx.notify = func(toast.Note) {} // no real toasts from tests
	s.settings.Name = "Alice"
	s.inCall = true
	s.members["bob"] = &member{Member: Member{ID: "bob", Name: "Bob", InCall: true}, seen: time.Now()}
	s.members["carol"] = &member{Member: Member{ID: "carol", Name: "Carol", InCall: false}, seen: time.Now()}
	s.mu.Unlock()
	return s, store
}

func envelope(t *testing.T, from, typ string, body any) Envelope {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{V: 1, ID: randHex(8), From: from, Type: typ, At: time.Now().UnixMilli(), Body: raw}
}

func TestTranscribeToggleByRevision(t *testing.T) {
	s, store := newTranscribeService(t)

	if _, err := s.SetTranscribe(true); err != nil {
		t.Fatal(err)
	}
	st := s.GetState().Call.Transcribe
	if !st.On || st.By != "Alice" || st.Transcript == "" {
		t.Fatalf("after turning on: %+v", st)
	}
	rev := st.Since

	// An older toggle (someone who hadn't seen ours) loses.
	s.onMessage(envelope(t, "bob", msgTx, txBody{On: false, Rev: rev - 1, By: "Bob"}))
	if !s.GetState().Call.Transcribe.On {
		t.Fatal("an older toggle turned it off")
	}
	// A newer one wins, from anyone in the room.
	s.onMessage(envelope(t, "bob", msgTx, txBody{On: false, Rev: rev + 1, By: "Bob"}))
	st = s.GetState().Call.Transcribe
	if st.On || st.By != "Bob" {
		t.Fatalf("Bob's newer toggle: %+v", st)
	}
	// From someone not in the room: ignored.
	s.onMessage(envelope(t, "stranger", msgTx, txBody{On: true, Rev: rev + 5, By: "X"}))
	if s.GetState().Call.Transcribe.On {
		t.Fatal("a stranger's toggle was taken")
	}
	// Presence brings a toggle we missed.
	s.onMessage(envelope(t, "bob", msgPresence, presenceBody{Name: "Bob", InCall: true, Tx: &txBody{On: true, Rev: rev + 10, By: "Bob"}}))
	if st := s.GetState().Call.Transcribe; !st.On || st.Since != rev+10 {
		t.Fatalf("presence toggle: %+v", st)
	}

	_, entries, _ := store.Live(0)
	var kinds []string
	for _, e := range entries {
		kinds = append(kinds, e.Kind+":"+e.Speaker)
	}
	want := []string{"started:Alice", "stopped:Bob", "started:Bob"}
	if len(kinds) != len(want) {
		t.Fatalf("marks = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("marks = %v, want %v", kinds, want)
		}
	}
}

func TestTranscriptLinesFromOthers(t *testing.T) {
	s, store := newTranscribeService(t)
	now := time.Now().UnixMilli()
	line := func(from, id, text string) {
		s.onMessage(envelope(t, from, msgTxLine, txLineBody{ID: id, At: now, End: now + 1000, Text: text, Name: "Fake name"}))
	}
	line("bob", "1", "before it's on") // not on yet: dropped
	if _, err := s.SetTranscribe(true); err != nil {
		t.Fatal(err)
	}
	line("bob", "2", "hello there")
	line("bob", "2", "hello there")   // the same line twice (two brokers): once
	line("carol", "3", "not in call") // not in the call: dropped
	line("nobody", "4", "who")        // not in the room: dropped
	line("bob", "5", "[BLANK_AUDIO]") // not speech: dropped
	if _, err := s.SetTranscribe(false); err != nil {
		t.Fatal(err)
	}
	line("bob", "6", "said just before the stop") // within the grace: kept

	_, entries, _ := store.Live(0)
	var got []string
	for _, e := range entries {
		if e.Kind == transcript.KindLine {
			got = append(got, e.Speaker+": "+e.Text)
		}
	}
	if len(got) != 2 || got[0] != "Bob: hello there" || got[1] != "Bob: said just before the stop" {
		t.Fatalf("lines = %q", got)
	}

	// Hanging up ends the transcript.
	s.LeaveCall()
	deadline := time.Now().Add(5 * time.Second)
	for store.Active() != "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if store.Active() != "" {
		t.Fatal("transcript still active after hanging up")
	}
	list, _ := store.List()
	if len(list) != 1 || list[0].EndedAt == 0 || list[0].Lines != 2 {
		t.Fatalf("saved = %+v", list)
	}
}

func TestTranscribeSegmentNeedsItOn(t *testing.T) {
	s, _ := newTranscribeService(t)
	// Off (and no model): nothing is queued, and no error.
	if err := s.TranscribeSegment(1, 2, "AAAA"); err != nil {
		t.Fatal(err)
	}
	if n := len(s.tx.queue); n != 0 {
		t.Fatalf("queued %d segments while off", n)
	}
}

func TestCleanText(t *testing.T) {
	for in, want := range map[string]string{
		"  Hello   there. ":    "Hello there.",
		"[BLANK_AUDIO]":        "",
		"(upbeat music)":       "",
		"* coughs *":           "",
		"♪ la la ♪":            "",
		"- Yes, I think so.":   "Yes, I think so.",
		"(laughs) that's good": "(laughs) that's good",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}
