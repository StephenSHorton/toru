package export

import (
	"path/filepath"

	toast "github.com/StephenSHorton/go-toast"
)

// noticeID is shared by every copy notice, so copying again replaces the
// card on screen instead of stacking another one.
const noticeID = "clipboard"

// What a copy names on its notice when the caller doesn't say.
const (
	whatImage = "Screenshot"
	whatVideo = "Video"
	whatText  = "Link"
)

// notifyCopy posts the native "Copied to clipboard" card, or the error card
// when err is set. It is posted from Go, not the webview, because the window
// that asked for the copy is often hidden or closing (editor Done, the
// skip-editor screenshot path). file, when set, is added to the body so a
// video copy says which file it was.
func notifyCopy(what, file string, err error) {
	if err != nil {
		toast.Post(toast.Note{
			ID:    noticeID,
			Title: "Couldn't copy to clipboard",
			Body:  what + ": " + err.Error(),
			Kind:  toast.Error,
		})
		return
	}
	body := what
	if file != "" {
		body += " · " + filepath.Base(file)
	}
	toast.Post(toast.Note{
		ID:    noticeID,
		Title: "Copied to clipboard",
		Body:  body,
		Kind:  toast.Success,
	})
}
