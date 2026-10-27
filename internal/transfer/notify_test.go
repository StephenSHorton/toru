package transfer

import (
	"strings"
	"testing"

	toast "github.com/StephenSHorton/go-toast"
)

// TestNotifierPostsOnTerminalEdges: progress and phase events post nothing, a
// done or error state posts once, and stop/cancel stay quiet.
func TestNotifierPostsOnTerminalEdges(t *testing.T) {
	var got []toast.Note
	n := &notifier{post: func(note toast.Note) { got = append(got, note) }}

	n.observe(EventSend, SendState{Active: true, Phase: SendWaiting, Name: "a.mp4", Code: "7-x-y"})
	n.observe(EventSend, SendState{Active: true, Phase: SendSending, Name: "a.mp4", Sent: 10, Total: 100})
	n.observe(EventSend, SendState{Active: true, Phase: SendSending, Name: "a.mp4", Sent: 50, Total: 100})
	done := SendState{Phase: SendDone, Name: "a.mp4", Sent: 100, Total: 100, Message: "Sent a.mp4"}
	n.observe(EventSend, done)
	n.observe(EventSend, done) // a repeat of the same state is not a new edge
	n.observe(EventSend, SendState{Phase: SendStopped, Name: "b.zip"})
	n.observe(EventReceive, ReceiveState{Phase: RecvCancelled})
	n.observe(EventOpen, nil)

	if len(got) != 1 || got[0].Title != "Sent a.mp4" || got[0].Kind != toast.Success || got[0].ID != noteSend {
		t.Fatalf("got %+v", got)
	}

	n.observe(EventReceive, ReceiveState{Active: true, Phase: RecvConnecting, Code: "7-x-y"})
	n.observe(EventReceive, ReceiveState{Phase: RecvError, Code: "7-x-y", Error: "wrong code"})
	if len(got) != 2 || got[1].Title != "Couldn't receive" || got[1].Body != "wrong code" || got[1].Kind != toast.Error {
		t.Fatalf("got %+v", got)
	}
}

func TestReceiveNoteOffersShowInExplorer(t *testing.T) {
	n, ok := receiveNote(ReceiveState{Phase: RecvDone, Name: "a.mp4", OutPath: `C:\Downloads\a (1).mp4`, Dir: `C:\Downloads`})
	if !ok {
		t.Fatal("no note for a finished receive")
	}
	if n.Title != "Received a (1).mp4" || !strings.HasPrefix(n.Body, "Saved to ") || n.Kind != toast.Success || n.ID != noteReceive {
		t.Fatalf("note %+v", n)
	}
	if len(n.Actions) != 1 || n.Actions[0].Label != "Show in Explorer" || n.Actions[0].Run == nil || n.OnClick == nil {
		t.Fatalf("actions %+v", n.Actions)
	}
	if n, _ := receiveNote(ReceiveState{Phase: RecvError, Name: "a.mp4", Error: "boom"}); n.Title != "Couldn't receive a.mp4" {
		t.Fatalf("error title %q", n.Title)
	}
	if n, _ := sendNote(SendState{Phase: SendError, Name: "a.mp4", Error: "boom"}); n.Title != "Couldn't send a.mp4" || n.Body != "boom" {
		t.Fatalf("send error %+v", n)
	}
}
