// Package toast is Toru's notification system: small native cards stacked in
// a corner of the active monitor, over every other window, that never take
// focus. Replay, recording, the clicker and the updater all post here, and the
// s&box editor sounds that go with each card (sounds/) are played from here
// too, so Toru has one sound system.
//
// This file is the OS-agnostic model (notes, the stack, expiry, options); the
// window that draws it is toast_windows.go.
package toast

import (
	"math"
	"strings"
	"sync"
	"time"
)

// Kind picks a card's accent, icon and sound.
type Kind int

const (
	Info Kind = iota
	Success
	Warn
	Error
)

// Action is a button on a card. Run is called on its own goroutine after the
// card closes.
type Action struct {
	Label string
	Run   func()
}

// Note is one notification.
type Note struct {
	// ID, when set, replaces the live card with the same ID in place (and
	// restarts its timer) instead of stacking a new one: "Saving clip…" then
	// "Clip saved".
	ID    string
	Title string
	Body  string // up to two lines; longer text is ellipsized
	Kind  Kind
	// Silent skips the sound, e.g. for a progress card a later one replaces.
	Silent bool
	// Expire overrides the default lifetime (defaultLifetime). Ignored when
	// Sticky.
	Expire time.Duration
	// Sticky cards stay until clicked, dismissed or closed.
	Sticky bool
	// Actions are up to two buttons (extra ones are dropped). The first one
	// is drawn as the primary button.
	Actions []Action
	// OnClick runs on a left click on the card body. A body click on a card
	// without OnClick counts as a dismiss.
	OnClick func()
	// OnDismiss runs when the user dismisses the card (right click, the x, or
	// a body click with no OnClick). Not when it expires, is replaced, pushed
	// off a full stack, or closed with Close.
	OnDismiss func()
}

// Corner values for Options.Corner.
const (
	CornerTopRight    = "top-right"
	CornerBottomRight = "bottom-right"
	CornerTopLeft     = "top-left"
	CornerBottomLeft  = "bottom-left"
)

// Options are the user's notification settings.
type Options struct {
	// Enabled off makes Post a no-op: no card and no sound. PlaySound still
	// plays (it is for callers with their own sound setting, like replay's).
	Enabled bool `json:"enabled"`
	// Sound off silences the cards' sounds. PlaySound ignores it, for the
	// same reason it ignores Enabled.
	Sound  bool    `json:"sound"`
	Volume float64 `json:"volume"` // 0..1, PlaySound's too
	Corner string  `json:"corner"` // one of the Corner* values
}

// DefaultOptions is on, with sound, in the top-right corner (which also keeps
// cards clear of most games' minimap and health/ammo HUD corners).
func DefaultOptions() Options {
	return Options{Enabled: true, Sound: true, Volume: 0.6, Corner: CornerTopRight}
}

// normalized snaps a hand-edited or partial Options to valid values.
func (o Options) normalized() Options {
	switch {
	case math.IsNaN(o.Volume):
		o.Volume = DefaultOptions().Volume
	case o.Volume < 0:
		o.Volume = 0
	case o.Volume > 1:
		o.Volume = 1
	}
	switch o.Corner {
	case CornerTopRight, CornerBottomRight, CornerTopLeft, CornerBottomLeft:
	default:
		o.Corner = CornerTopRight
	}
	return o
}

var (
	optMu sync.Mutex
	opts  = DefaultOptions()
)

// SetOptions applies the user's notification settings. Turning notifications
// off also clears the cards on screen.
func SetOptions(o Options) {
	o = o.normalized()
	optMu.Lock()
	opts = o
	optMu.Unlock()
	optionsChanged(o)
}

// CurrentOptions returns the settings in effect.
func CurrentOptions() Options {
	optMu.Lock()
	defer optMu.Unlock()
	return opts
}

const (
	maxCards   = 5
	maxActions = 2

	fadeIn  = 140 * time.Millisecond
	fadeOut = 320 * time.Millisecond
	// afterHover is the least time a card has left once the cursor leaves the
	// stack, so it doesn't vanish the instant you look away.
	afterHover = 1500 * time.Millisecond
	fullAlpha  = 245
)

// defaultLifetime is how long a card stays fully visible before it fades.
func defaultLifetime(n Note) time.Duration {
	switch {
	case n.Sticky:
		return 0
	case n.Expire > 0:
		return n.Expire
	case len(n.Actions) > 0:
		return 10 * time.Second
	case n.Kind == Error || n.Kind == Warn:
		return 6 * time.Second
	default:
		return 3 * time.Second
	}
}

// clean trims a note to what a card can show. ok is false for a note with
// nothing to say.
func clean(n Note) (Note, bool) {
	n.Title, n.Body = strings.TrimSpace(n.Title), strings.TrimSpace(n.Body)
	if n.Title == "" {
		n.Title, n.Body = n.Body, ""
	}
	if len(n.Actions) > maxActions {
		n.Actions = n.Actions[:maxActions]
	}
	return n, n.Title != ""
}

// card is a note on screen.
type card struct {
	Note
	key     uint64    // stable for the card's life, across same-ID replaces
	rev     uint64    // bumps on every replace, so the window repaints
	born    time.Time // first shown (fade-in)
	until   time.Time // when it starts to fade; zero = sticky
	closing time.Time // fading out since; zero = not fading
}

// stack is the live cards, oldest first. Not safe for concurrent use; the
// platform code guards it.
type stack struct {
	cards []*card
	next  uint64
}

// post adds n, or replaces the card with the same ID in place (keeping its
// slot). When the stack is full the oldest non-sticky card (else the oldest)
// is pushed off and returned as dropped.
func (s *stack) post(n Note, now time.Time) (c *card, dropped *card) {
	until := time.Time{}
	if d := defaultLifetime(n); d > 0 {
		until = now.Add(d)
	}
	if n.ID != "" {
		for _, c := range s.cards {
			if c.ID == n.ID {
				c.Note, c.until, c.closing = n, until, time.Time{}
				c.rev++
				return c, nil
			}
		}
	}
	if len(s.cards) >= maxCards {
		i := 0
		for j, c := range s.cards {
			if !c.Sticky {
				i = j
				break
			}
		}
		dropped = s.cards[i]
		s.cards = append(s.cards[:i], s.cards[i+1:]...)
	}
	s.next++
	c = &card{Note: n, key: s.next, born: now, until: until}
	s.cards = append(s.cards, c)
	return c, dropped
}

// take removes and returns the card with key (a click or dismiss), or nil.
func (s *stack) take(key uint64) *card {
	for i, c := range s.cards {
		if c.key == key {
			s.cards = append(s.cards[:i], s.cards[i+1:]...)
			return c
		}
	}
	return nil
}

// closeID removes the card with id at once (Close), reporting whether one was
// live.
func (s *stack) closeID(id string) bool {
	if id == "" {
		return false
	}
	for _, c := range s.cards {
		if c.ID == id {
			return s.take(c.key) != nil
		}
	}
	return false
}

// tick advances expiry. While the cursor is over the stack nothing expires
// (so the cards don't reflow under it) and a fading card comes back; after
// it leaves every card keeps at least afterHover. Cards whose fade-out has
// finished are removed and returned.
func (s *stack) tick(now time.Time, hovered bool) (gone []*card) {
	if hovered {
		for _, c := range s.cards {
			c.closing = time.Time{}
			if !c.until.IsZero() && c.until.Sub(now) < afterHover {
				c.until = now.Add(afterHover)
			}
		}
		return nil
	}
	keep := s.cards[:0]
	for _, c := range s.cards {
		if c.closing.IsZero() && !c.until.IsZero() && !now.Before(c.until) {
			c.closing = now
		}
		if !c.closing.IsZero() && now.Sub(c.closing) >= fadeOut {
			gone = append(gone, c)
			continue
		}
		keep = append(keep, c)
	}
	s.cards = keep
	return gone
}

// alpha is a card's opacity now: fading in after it appears, out once it
// expires.
func (c *card) alpha(now time.Time) byte {
	f := 1.0
	if age := now.Sub(c.born); age < fadeIn {
		f = math.Max(0, float64(age)/float64(fadeIn))
	}
	if !c.closing.IsZero() {
		f = math.Min(f, 1-float64(now.Sub(c.closing))/float64(fadeOut))
	}
	if f <= 0 {
		return 0
	}
	return byte(f*fullAlpha + 0.5)
}
