package transfer

import (
	"os"
	"path/filepath"
	"sync"

	toast "github.com/StephenSHorton/go-toast"
)

// Notification card IDs: a retry replaces the last card for that side instead
// of stacking a second one.
const (
	noteSend    = "transfer-send"
	noteReceive = "transfer-receive"
)

// notifier turns the transfer events into native cards for the moments worth
// hearing about while the hub is behind a game or closed to the tray: a send
// the receiver finished, a file that arrived, and either side failing. Stop/cancel are the user's own doing
// and get no card; neither does the code being issued (the user is reading it
// off the page). It watches the emitter rather than living in Manager so the
// loopback/network tests, which drive real transfers to "done", never open a
// card or play a sound.
type notifier struct {
	mu sync.Mutex
	// send and recv are the last state emitted per side, so a card goes up on
	// the edge into a terminal phase and never twice for the same state.
	send SendState
	recv ReceiveState
	post func(toast.Note)
}

func newNotifier() *notifier { return &notifier{post: toast.Post} }

// observe sees every Manager event (outside Manager's lock).
func (n *notifier) observe(name string, data any) {
	var note toast.Note
	var ok bool
	n.mu.Lock()
	switch name {
	case EventSend:
		st, is := data.(SendState)
		if !is || st == n.send {
			break
		}
		n.send = st
		note, ok = sendNote(st)
	case EventReceive:
		st, is := data.(ReceiveState)
		if !is || st == n.recv {
			break
		}
		n.recv = st
		note, ok = receiveNote(st)
	}
	post := n.post
	n.mu.Unlock()
	if ok && post != nil {
		post(note)
	}
}

// sendNote is the card for a send that just ended, if it deserves one.
func sendNote(st SendState) (toast.Note, bool) {
	switch st.Phase {
	case SendDone:
		return toast.Note{ID: noteSend, Title: "Sent " + st.Name, Kind: toast.Success}, true
	case SendError:
		return toast.Note{ID: noteSend, Title: "Couldn't send " + st.Name, Body: st.Error, Kind: toast.Error}, true
	}
	return toast.Note{}, false
}

// receiveNote is the card for a receive that just ended. A finished one offers
// Show in Explorer (the page's button), and a click on the card does the same.
func receiveNote(st ReceiveState) (toast.Note, bool) {
	switch st.Phase {
	case RecvDone:
		out, dir := st.OutPath, st.Dir
		show := func() { showReceived(out, dir) }
		return toast.Note{
			ID:      noteReceive,
			Title:   "Received " + filepath.Base(out),
			Body:    "Saved to " + dir,
			Kind:    toast.Success,
			Actions: []toast.Action{{Label: "Show in Explorer", Run: show}},
			OnClick: show,
		}, true
	case RecvError:
		title := "Couldn't receive"
		if st.Name != "" {
			title += " " + st.Name
		}
		return toast.Note{ID: noteReceive, Title: title, Body: st.Error, Kind: toast.Error}, true
	}
	return toast.Note{}, false
}

// showReceived selects out in Explorer, or opens dir when out has since been
// moved or deleted (the card can outlive the file).
func showReceived(out, dir string) {
	if _, err := os.Stat(out); err == nil {
		_ = revealInExplorer(out)
		return
	}
	if dir != "" {
		_ = openFolder(dir)
	}
}
