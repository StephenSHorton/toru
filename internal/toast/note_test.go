package toast

import (
	"encoding/binary"
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

func TestSameIDReplacesInPlace(t *testing.T) {
	var s stack
	s.post(Note{ID: "save", Title: "Saving clip…", Silent: true}, t0)
	s.post(Note{Title: "Other"}, t0)
	c, dropped := s.post(Note{ID: "save", Title: "Clip saved"}, t0.Add(time.Second))
	if dropped != nil || len(s.cards) != 2 {
		t.Fatalf("replace stacked: %d cards, dropped %v", len(s.cards), dropped)
	}
	if s.cards[0] != c || c.Title != "Clip saved" || c.rev != 1 || !c.born.Equal(t0) {
		t.Fatalf("replace lost its slot or content: %+v", c)
	}
	if want := t0.Add(time.Second + defaultLifetime(c.Note)); !c.until.Equal(want) {
		t.Errorf("replace didn't restart the timer: until %v, want %v", c.until, want)
	}
}

func TestFullStackDropsOldestNonSticky(t *testing.T) {
	var s stack
	s.post(Note{Title: "sticky", Sticky: true}, t0)
	for i := 0; i < maxCards-1; i++ {
		s.post(Note{Title: "n"}, t0)
	}
	second := s.cards[1]
	_, dropped := s.post(Note{Title: "new"}, t0)
	if dropped != second || len(s.cards) != maxCards || s.cards[0].Title != "sticky" {
		t.Fatalf("dropped %+v; stack %d, first %q", dropped, len(s.cards), s.cards[0].Title)
	}
	if s.cards[maxCards-1].Title != "new" {
		t.Error("new card should be last (newest)")
	}
}

func TestTickExpiresThenFades(t *testing.T) {
	var s stack
	c, _ := s.post(Note{Title: "x"}, t0)
	life := defaultLifetime(c.Note)
	if gone := s.tick(t0.Add(life-time.Millisecond), false); gone != nil || !c.closing.IsZero() {
		t.Fatal("expired early")
	}
	at := t0.Add(life)
	s.tick(at, false)
	if c.closing.IsZero() || c.alpha(at.Add(fadeOut/2)) >= fullAlpha {
		t.Fatal("should be fading after its lifetime")
	}
	if gone := s.tick(at.Add(fadeOut), false); len(gone) != 1 || len(s.cards) != 0 {
		t.Fatalf("not removed after the fade: gone %d, left %d", len(gone), len(s.cards))
	}
}

func TestHoverPausesAndRevives(t *testing.T) {
	var s stack
	c, _ := s.post(Note{Title: "x"}, t0)
	end := t0.Add(defaultLifetime(c.Note))
	s.tick(end, false) // starts fading
	s.tick(end.Add(fadeOut/2), true)
	if !c.closing.IsZero() {
		t.Fatal("hover should bring a fading card back")
	}
	later := end.Add(time.Minute)
	if gone := s.tick(later, true); gone != nil {
		t.Fatal("nothing expires while hovered")
	}
	if c.until.Sub(later) < afterHover {
		t.Errorf("leaving should give at least %v, got %v", afterHover, c.until.Sub(later))
	}
}

func TestStickyNeverExpires(t *testing.T) {
	var s stack
	c, _ := s.post(Note{Title: "Update ready", Sticky: true, Expire: time.Second}, t0)
	if gone := s.tick(t0.Add(time.Hour), false); gone != nil || !c.closing.IsZero() {
		t.Fatal("sticky card expired")
	}
}

func TestLifetimes(t *testing.T) {
	act := []Action{{Label: "Restart"}}
	for _, tc := range []struct {
		n    Note
		want time.Duration
	}{
		{Note{Kind: Success}, 3 * time.Second},
		{Note{Kind: Error}, 6 * time.Second},
		{Note{Kind: Warn}, 6 * time.Second},
		{Note{Actions: act}, 10 * time.Second},
		{Note{Expire: 1500 * time.Millisecond, Actions: act}, 1500 * time.Millisecond},
		{Note{Sticky: true, Expire: time.Second}, 0},
	} {
		if got := defaultLifetime(tc.n); got != tc.want {
			t.Errorf("%+v: %v, want %v", tc.n, got, tc.want)
		}
	}
}

func TestCleanAndClose(t *testing.T) {
	if _, ok := clean(Note{Title: "  ", Body: " "}); ok {
		t.Error("empty note should be dropped")
	}
	n, _ := clean(Note{Body: "Copied", Actions: make([]Action, 3)})
	if n.Title != "Copied" || n.Body != "" || len(n.Actions) != maxActions {
		t.Errorf("clean = %+v", n)
	}
	var s stack
	s.post(Note{ID: "a", Title: "a"}, t0)
	if s.closeID("") || !s.closeID("a") || len(s.cards) != 0 || s.closeID("a") {
		t.Error("closeID")
	}
}

func TestAlphaFadesIn(t *testing.T) {
	var s stack
	c, _ := s.post(Note{Title: "x"}, t0)
	if a := c.alpha(t0); a != 0 {
		t.Errorf("alpha at birth = %d", a)
	}
	if a := c.alpha(t0.Add(fadeIn)); a != fullAlpha {
		t.Errorf("alpha after fade-in = %d", a)
	}
}

func TestOptionsNormalized(t *testing.T) {
	o := Options{Volume: 3, Corner: "middle"}.normalized()
	if o.Volume != 1 || o.Corner != CornerTopRight {
		t.Errorf("normalized = %+v", o)
	}
	if o := (Options{Volume: math.NaN(), Corner: CornerBottomLeft}).normalized(); o.Volume != DefaultOptions().Volume || o.Corner != CornerBottomLeft {
		t.Errorf("normalized = %+v", o)
	}
	if o := (Options{Volume: -1}).normalized(); o.Volume != 0 {
		t.Errorf("negative volume = %v", o.Volume)
	}
}

func TestStackOriginsNewestNearestCorner(t *testing.T) {
	work := rect{L: 0, T: 40, R: 1000, B: 800}
	heights := []int{60, 80} // oldest, newest
	const w, gap, margin = 300, 8, 20
	for _, tc := range []struct {
		corner      string
		old, newest point
	}{
		{CornerTopRight, point{680, 40 + 20 + 80 + 8}, point{680, 60}},
		{CornerTopLeft, point{20, 40 + 20 + 80 + 8}, point{20, 60}},
		{CornerBottomRight, point{680, 800 - 20 - 80 - 8 - 60}, point{680, 800 - 20 - 80}},
		{CornerBottomLeft, point{20, 800 - 20 - 80 - 8 - 60}, point{20, 800 - 20 - 80}},
	} {
		got := stackOrigins(work, tc.corner, w, heights, gap, margin)
		if got[0] != tc.old || got[1] != tc.newest {
			t.Errorf("%s: %v, want [%v %v]", tc.corner, got, tc.old, tc.newest)
		}
	}
}

func TestCardHeights(t *testing.T) {
	if h := cardHeightDip(1, false); h != 62 {
		t.Errorf("one-line card = %v dip, want Toru's original 62", h)
	}
	if cardHeightDip(5, false) != cardHeightDip(maxBodyLines, false) {
		t.Error("body lines should clamp")
	}
	if cardHeightDip(0, true) <= cardHeightDip(0, false) {
		t.Error("buttons need room")
	}
}

func TestHitParts(t *testing.T) {
	const w, h, sc = 340, 96, 1.0
	cl := closeRect(w, sc)
	btns := buttonRects(w, h, []int{40, 30}, sc)
	if len(btns) != 2 || btns[0].R > btns[1].L || btns[1].R != w-buttonsRightDip || btns[0].B != h-padBottomDip {
		t.Fatalf("buttons = %v", btns)
	}
	if btns[0].R-btns[0].L != buttonMinDip {
		t.Errorf("short label should get the minimum width, got %d", btns[0].R-btns[0].L)
	}
	for _, tc := range []struct {
		x, y int
		want part
	}{
		{cl.L + 2, cl.T + 2, partClose},
		{btns[0].L + 1, btns[0].T + 1, 0},
		{btns[1].R - 1, btns[1].B - 1, 1},
		{60, 30, partBody},
	} {
		if got := hitPart(tc.x, tc.y, cl, btns); got != tc.want {
			t.Errorf("hit(%d,%d) = %d, want %d", tc.x, tc.y, got, tc.want)
		}
	}
	if buttonRects(w, h, nil, sc) != nil {
		t.Error("no actions, no buttons")
	}
}

func TestSoundMapping(t *testing.T) {
	for _, tc := range []struct {
		k          Kind
		actionable bool
		want       sound
	}{
		{Success, false, soundSuccess},
		{Info, false, soundSuccess},
		{Error, false, soundFail},
		{Warn, false, soundPublished},
		{Success, true, soundPublished},
		{Error, true, soundPublished},
	} {
		if got := soundFor(tc.k, tc.actionable); got != tc.want {
			t.Errorf("soundFor(%d, %v) = %d, want %d", tc.k, tc.actionable, got, tc.want)
		}
	}
}

func TestEmbeddedSoundsArePCM16(t *testing.T) {
	for name, wav := range map[string][]byte{"success": successWAV, "fail": failWAV, "published": publishedWAV} {
		off, n, ok := pcmData(wav)
		if !ok || n < 1000 || off+n > len(wav) {
			t.Errorf("%s: not 16-bit PCM (off %d, n %d, ok %v)", name, off, n, ok)
		}
	}
	if _, _, ok := pcmData([]byte("RIFF\x00\x00\x00\x00WAVE")); ok {
		t.Error("a WAV without data parsed")
	}
}

func TestScaleWAV(t *testing.T) {
	if b := scaleWAV(successWAV, 1); &b[0] != &successWAV[0] {
		t.Error("full volume should play the embedded bytes")
	}
	half := scaleWAV(successWAV, 0.5)
	off, n, _ := pcmData(successWAV)
	if len(half) != len(successWAV) || string(half[:off]) != string(successWAV[:off]) {
		t.Fatal("scaling must keep the header")
	}
	for i := off; i+1 < off+n; i += 2 {
		src := float64(int16(binary.LittleEndian.Uint16(successWAV[i:])))
		got := float64(int16(binary.LittleEndian.Uint16(half[i:])))
		if math.Abs(got-src/2) > 1 {
			t.Fatalf("sample at %d: %v from %v", i, got, src)
		}
	}
	if soundBytes(soundFail, 0) != nil {
		t.Error("volume 0 should be silence")
	}
	a, b := soundBytes(soundFail, 0.6), soundBytes(soundFail, 0.6)
	if &a[0] != &b[0] {
		t.Error("same volume should reuse the cached copy")
	}
}
