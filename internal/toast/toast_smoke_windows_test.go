//go:build windows

package toast

import (
	"os"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// TestToastSmoke shows real cards, clicks one, and checks focus never moves.
// Opt-in (TORU_SMOKE=1): it draws on the desktop and plays the sounds.
func TestToastSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	before := windows.GetForegroundWindow()
	assertFocus := func() {
		t.Helper()
		if fg := windows.GetForegroundWindow(); fg != before {
			t.Fatalf("toast stole focus: foreground %x -> %x", before, fg)
		}
	}

	Post(Note{ID: "smoke-save", Title: "Saving clip…", Body: "Test · Last 1 min", Kind: Info, Silent: true})
	time.Sleep(700 * time.Millisecond)
	Show("Clip saved", "Test · Last 1 min", true) // stacks: no ID
	Post(Note{ID: "smoke-save", Title: "Copied to clipboard", Kind: Success})
	if n := len(cardWindowsSoon(2)); n != 2 {
		t.Fatalf("%d cards, want 2 (same ID replaces)", n)
	}

	ran := make(chan string, 1)
	Post(Note{
		Title:   "Toru v9.9.9 is ready",
		Body:    "Restart now, or it installs when you quit.",
		Sticky:  true,
		Actions: []Action{{Label: "Restart", Run: func() { ran <- "restart" }}, {Label: "Later"}},
	})
	cards := cardWindowsSoon(3)
	if len(cards) != 3 {
		t.Fatalf("%d cards, want 3", len(cards))
	}
	time.Sleep(400 * time.Millisecond)
	assertFocus()

	// Click the update card's primary button, as the user would. The rects
	// are laid out from minimum-width labels; real labels are at least as
	// wide, which only pushes the first button further left, so a point just
	// inside this one's left edge is still on it.
	newest := cards[len(cards)-1]
	r := w32.GetClientRect(newest)
	sc := float64(r.Right) / cardWidthDip
	b := buttonRects(int(r.Right), int(r.Bottom), []int{px(30, sc), px(30, sc)}, sc)[0]
	lp := uintptr(b.L+4) | uintptr(b.T+4)<<16
	w32.PostMessage(newest, w32.WM_MOUSEMOVE, 0, lp)
	w32.PostMessage(newest, w32.WM_LBUTTONDOWN, 1, lp)
	w32.PostMessage(newest, w32.WM_LBUTTONUP, 0, lp)
	select {
	case got := <-ran:
		if got != "restart" {
			t.Fatalf("ran %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the Restart action never ran")
	}
	assertFocus()
	if n := len(cardWindowsSoon(2)); n != 2 {
		t.Fatalf("%d cards after the click, want 2", n)
	}

	Close("smoke-save")
	for i := 0; i < 70 && Visible(); i++ { // the plain card's lifetime + fade
		time.Sleep(100 * time.Millisecond)
		assertFocus()
	}
	if Visible() {
		t.Error("cards should have faded out and closed")
	}
}

// cardWindowsSoon waits briefly for the window thread to reach want cards.
func cardWindowsSoon(want int) []w32.HWND {
	var got []w32.HWND
	for i := 0; i < 20; i++ {
		if got = cardWindows(); len(got) == want {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	return got
}
