package stream

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/transcript"
)

// transcribe.go is call transcription. Anyone in the call can turn it on or
// off for the whole call; everyone in the call sees that it's on (the call
// card, a toast, and a "started/stopped by" line in the transcript), so no
// one is transcribed without knowing.
//
// Each person's Toru transcribes only its own microphone, on this PC
// (internal/transcript's whisper engine), and sends the text lines to the
// room over the same encrypted channel as the call's signaling. So every
// line carries its speaker's name, no audio leaves anyone's PC, and a
// person without the speech model still gets everyone else's lines. Every
// Toru in the call saves all the lines it gets as one transcript per call.
//
// Who turned it on or off is agreed by revision: each toggle carries a
// revision (its time, and always above the last one seen), presence repeats
// the current one so latecomers learn it, and the highest wins.

// Room messages.
const (
	msgTx     = "tx"     // everyone: transcription turned on/off (txBody)
	msgTxLine = "txline" // everyone: one transcribed line of the sender's speech (txLineBody)
)

// EventTranscriptEntry carries TranscriptEntry: a new line (or mark) in the
// call's transcript, for the live captions.
const EventTranscriptEntry = "transcript:entry"

// lateLineGrace is how long after transcription stops lines still arriving
// (said just before the stop, transcribed after) are kept.
const lateLineGrace = 20 * time.Second

// Segments the call window sends: at most this long (it cuts longer ones),
// at least this long, and at most this many waiting.
const (
	maxSegment   = 30 * time.Second
	minSegment   = 300 * time.Millisecond
	maxQueued    = 6
	finishWithin = 15 * time.Second // hanging up waits this long for lines still being transcribed
)

type txBody struct {
	On  bool   `json:"on"`
	Rev int64  `json:"rev"`
	By  string `json:"by"` // their name
}

type txLineBody struct {
	ID   string `json:"id"`
	At   int64  `json:"at"` // unix ms
	End  int64  `json:"end"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// TranscribeState is the call's transcription as the call card shows it.
type TranscribeState struct {
	On         bool                    `json:"on"`
	By         string                  `json:"by"`         // who turned it on (or off)
	Since      int64                   `json:"since"`      // when, unix ms
	Transcript string                  `json:"transcript"` // the id this call's lines are saved under, "" before any
	Engine     transcript.EngineStatus `json:"engine"`     // this PC's speech model
	Mine       bool                    `json:"mine"`       // our own speech is being transcribed now
}

// TranscriptEntry is one new entry of the call's transcript.
type TranscriptEntry struct {
	Transcript string           `json:"transcript"`
	Entry      transcript.Entry `json:"entry"`
}

type segment struct {
	at, end int64
	pcm     []int16
}

// txState is StreamService's transcription state (s.mu).
type txState struct {
	cur     txBody
	stopAt  time.Time // when it last turned off
	queue   chan segment
	pending sync.WaitGroup
	store   *transcript.Store
	engine  *transcript.Engine
	notify  func(toast.Note) // toast.Post; tests swap it
}

// SetTranscripts installs the transcript store, and makes this PC's speech
// engine (downloaded on first use).
//
//wails:ignore
func (s *StreamService) SetTranscripts(store *transcript.Store) {
	s.setTranscripts(store, transcript.NewEngine(s.emitState))
}

func (s *StreamService) setTranscripts(store *transcript.Store, engine *transcript.Engine) {
	s.mu.Lock()
	s.tx.store, s.tx.engine = store, engine
	if s.tx.queue == nil {
		s.tx.queue = make(chan segment, maxQueued)
		go s.transcribeLoop(s.tx.queue)
	}
	s.mu.Unlock()
	store.OnEntry(func(id string, e transcript.Entry) {
		s.emit(EventTranscriptEntry, TranscriptEntry{Transcript: id, Entry: e})
	})
}

// SetTranscribe turns transcription on or off for everyone in the call.
// Turning it on downloads the speech model (about 70 MB, once) if this PC
// doesn't have it yet.
func (s *StreamService) SetTranscribe(on bool) (State, error) {
	s.mu.Lock()
	if !s.inCall {
		s.mu.Unlock()
		return s.GetState(), errors.New("join the call first")
	}
	if s.tx.store == nil {
		s.mu.Unlock()
		return s.GetState(), errors.New("transcription isn't available")
	}
	if s.tx.cur.On == on {
		s.mu.Unlock()
		return s.GetState(), nil
	}
	rev := max(time.Now().UnixMilli(), s.tx.cur.Rev+1)
	b := txBody{On: on, Rev: rev, By: s.nameLocked()}
	engine := s.tx.engine
	s.mu.Unlock()
	if on && engine != nil {
		engine.Download()
	}
	s.applyTx(b, s.self, true)
	_ = s.send("", msgTx, b)
	s.announce()
	return s.GetState(), nil
}

// DownloadSpeechModel downloads this PC's speech model (whisper), so its
// own speech is transcribed too while transcription is on.
func (s *StreamService) DownloadSpeechModel() State {
	s.mu.Lock()
	engine := s.tx.engine
	s.mu.Unlock()
	if engine != nil {
		engine.Download()
	}
	return s.GetState()
}

// DownloadSpeechGPU downloads the GPU build of this PC's speech model
// (whisper on an NVIDIA GPU; the call page shows its size first), so
// transcribing costs the CPU less. Once it is here whisper runs on the GPU.
func (s *StreamService) DownloadSpeechGPU() State {
	s.mu.Lock()
	engine := s.tx.engine
	s.mu.Unlock()
	if engine != nil {
		engine.DownloadGPU()
	}
	return s.GetState()
}

func (s *StreamService) nameLocked() string {
	if n := clipName(s.settings.Name); n != "" {
		return n
	}
	return "Someone"
}

// applyTx takes b if it is newer than what we have: it records the start or
// stop in the transcript and tells this person. from is the peer who sent it.
func (s *StreamService) applyTx(b txBody, from string, mine bool) {
	s.mu.Lock()
	if !s.inCall || s.tx.store == nil || b.Rev <= s.tx.cur.Rev {
		s.mu.Unlock()
		return
	}
	prev := s.tx.cur
	s.tx.cur = txBody{On: b.On, Rev: b.Rev, By: clipName(b.By)}
	if s.tx.cur.By == "" {
		s.tx.cur.By = "Someone"
	}
	if prev.On && !b.On {
		s.tx.stopAt = time.Now()
	}
	cur, store, self := s.tx.cur, s.tx.store, s.nameLocked()
	s.mu.Unlock()
	if prev.On == b.On {
		s.emitState()
		return
	}
	if b.On {
		if _, err := store.Begin(self, time.Now()); err != nil {
			logf("transcript: %v", err)
		}
	}
	kind := transcript.KindStopped
	if b.On {
		kind = transcript.KindStarted
	}
	store.Add(transcript.Entry{Kind: kind, At: b.Rev, Speaker: cur.By, SpeakerID: from, ID: "tx-" + strconv.FormatInt(b.Rev, 10)})
	s.txToast(cur, mine)
	s.emitState()
}

func (s *StreamService) txToast(b txBody, mine bool) {
	n := toast.Note{ID: "call-transcribe", Kind: toast.Info, Silent: true}
	who := b.By
	if mine {
		who = "You"
	}
	if b.On {
		n.Title = "This call is being transcribed"
		n.Body = who + " turned on transcription. What everyone in the call says is written down and saved in Toru."
	} else {
		n.Title = "Transcription stopped"
		n.Body = who + " turned off transcription for the call."
	}
	s.mu.Lock()
	post := s.tx.notify
	s.mu.Unlock()
	if post == nil {
		post = toast.Post
	}
	post(n)
}

// txPresenceLocked is what presence says about transcription: the current
// toggle, while in the call. s.mu held.
func (s *StreamService) txPresenceLocked() *txBody {
	if !s.inCall || s.tx.cur.Rev == 0 {
		return nil
	}
	b := s.tx.cur
	return &b
}

// txJoined resets transcription for a new call (the others' presence brings
// the call's state in).
func (s *StreamService) txJoined() {
	s.mu.Lock()
	s.tx.cur, s.tx.stopAt = txBody{}, time.Time{}
	s.mu.Unlock()
}

// txShutdown ends the transcript of a call still going when Toru quits.
func (s *StreamService) txShutdown() {
	s.mu.Lock()
	store, engine := s.tx.store, s.tx.engine
	s.mu.Unlock()
	if store != nil {
		store.End(time.Now())
	}
	if engine != nil {
		engine.Stop()
	}
}

// txLeft finishes the call's transcript once the lines still being
// transcribed are in (up to finishWithin), and stops the engine.
func (s *StreamService) txLeft() {
	s.mu.Lock()
	s.tx.cur, s.tx.stopAt = txBody{}, time.Time{}
	store, engine := s.tx.store, s.tx.engine
	s.mu.Unlock()
	if store == nil {
		return
	}
	go func() {
		done := make(chan struct{})
		go func() {
			s.tx.pending.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(finishWithin):
		}
		s.mu.Lock()
		again := s.inCall && s.tx.cur.On
		s.mu.Unlock()
		if !again {
			store.End(time.Now())
			if engine != nil {
				engine.Stop()
			}
		}
		s.emitState()
	}()
}

// TranscribeSegment (call window) hands over one stretch of our own speech:
// 16 kHz mono 16-bit little-endian samples, base64, said from at to end
// (unix ms). It is transcribed in the background and sent to the room.
func (s *StreamService) TranscribeSegment(at, end int64, pcm string) error {
	s.mu.Lock()
	ok := s.inCall && s.tx.cur.On && !s.muted && !s.deafened && s.tx.engine != nil && s.tx.engine.Ready()
	q := s.tx.queue
	s.mu.Unlock()
	if !ok || q == nil {
		return nil
	}
	raw, err := base64.StdEncoding.DecodeString(pcm)
	if err != nil {
		return err
	}
	n := len(raw) / 2
	dur := time.Duration(n) * time.Second / transcript.SampleRate
	if dur < minSegment {
		return nil
	}
	if dur > maxSegment {
		n = int(maxSegment * transcript.SampleRate / time.Second)
	}
	samples := make([]int16, n)
	for i := range samples {
		samples[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	s.tx.pending.Add(1)
	select {
	case q <- segment{at: at, end: end, pcm: samples}:
	default:
		s.tx.pending.Done()
		logf("transcribe: falling behind, dropped a segment")
	}
	return nil
}

func (s *StreamService) transcribeLoop(q chan segment) {
	for seg := range q {
		s.transcribeOne(seg)
		s.tx.pending.Done()
	}
}

func (s *StreamService) transcribeOne(seg segment) {
	s.mu.Lock()
	engine, store, name, self := s.tx.engine, s.tx.store, s.nameLocked(), s.self
	s.mu.Unlock()
	if engine == nil || store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	text, err := engine.Transcribe(ctx, seg.pcm)
	if err != nil {
		logf("transcribe: %v", err)
		return
	}
	text = cleanText(text)
	if text == "" {
		return
	}
	line := txLineBody{ID: randHex(8), At: seg.at, End: seg.end, Text: text, Name: name}
	store.Add(transcript.Entry{Kind: transcript.KindLine, At: line.At, End: line.End, Speaker: name, SpeakerID: self, Text: text, ID: line.ID})
	if err := s.send("", msgTxLine, line); err != nil {
		logf("transcribe: send: %v", err)
	}
	s.emitState()
}

var nonSpeech = regexp.MustCompile(`^[\[\(\*♪].*[\]\)\*♪]$`)

// cleanText tidies whisper's text and drops what isn't speech
// ("[BLANK_AUDIO]", "(music)").
func cleanText(t string) string {
	t = strings.Join(strings.Fields(t), " ")
	t = strings.TrimPrefix(t, "- ")
	if t == "" || nonSpeech.MatchString(t) {
		return ""
	}
	if r := []rune(t); len(r) > transcript.MaxTextRunes {
		t = string(r[:transcript.MaxTextRunes])
	}
	return t
}

// onTxMessage takes another person's toggle.
func (s *StreamService) onTxMessage(env Envelope) {
	b, err := decodeBody[txBody](env)
	if err != nil {
		return
	}
	s.mu.Lock()
	_, known := s.members[env.From]
	s.mu.Unlock()
	if known {
		s.applyTx(b, env.From, false)
	}
}

// onTxLine saves another person's transcribed line.
func (s *StreamService) onTxLine(env Envelope) {
	b, err := decodeBody[txLineBody](env)
	if err != nil || b.ID == "" {
		return
	}
	if b.Text = cleanText(b.Text); b.Text == "" {
		return
	}
	s.mu.Lock()
	m := s.members[env.From]
	keep := s.inCall && m != nil && m.InCall &&
		(s.tx.cur.On || (!s.tx.stopAt.IsZero() && time.Since(s.tx.stopAt) < lateLineGrace))
	name := ""
	if m != nil {
		name = m.Name
	}
	store := s.tx.store
	s.mu.Unlock()
	if !keep || store == nil {
		return
	}
	if name == "" {
		name = clipName(b.Name)
	}
	// Times come from their clock: keep them near ours.
	now := time.Now().UnixMilli()
	if b.At <= 0 || b.At > now+60_000 || b.At < now-10*60_000 {
		b.At = now
	}
	if b.End < b.At {
		b.End = b.At
	}
	store.Add(transcript.Entry{Kind: transcript.KindLine, At: b.At, End: b.End, Speaker: name, SpeakerID: env.From,
		Text: b.Text, ID: env.From + "-" + clipID(b.ID)})
}

func clipID(id string) string {
	if len(id) > 32 {
		return id[:32]
	}
	return id
}

// transcribeStateLocked builds TranscribeState; s.mu held.
func (s *StreamService) transcribeStateLocked() TranscribeState {
	st := TranscribeState{On: s.inCall && s.tx.cur.On, By: s.tx.cur.By, Since: s.tx.cur.Rev}
	if s.tx.engine != nil {
		st.Engine = s.tx.engine.Status()
	} else {
		st.Engine = transcript.EngineStatus{Phase: transcript.PhaseUnsupported}
	}
	if s.tx.store != nil && s.inCall {
		st.Transcript = s.tx.store.Active()
	}
	st.Mine = st.On && st.Engine.Phase == transcript.PhaseReady && !s.muted && !s.deafened
	return st
}
