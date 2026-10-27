package replay

import (
	"time"

	"github.com/StephenSHorton/toru/internal/toast"
)

// saveToastID makes a save's result card replace its "Saving clip…" card.
const saveToastID = "replay-save"

// savingToast shows that a clip save started. The result (savedToast)
// replaces it; the long expiry only matters if that never comes.
func savingToast(what string) {
	toast.Post(toast.Note{ID: saveToastID, Title: "Saving clip…", Body: what,
		Kind: toast.Info, Silent: true, Expire: 30 * time.Second})
}

// savedToast shows how a clip save ended. It is silent: the sound is
// SoundOnSave's (playSaveSound), which plays whether or not this card does.
func savedToast(what string, err error) {
	n := toast.Note{ID: saveToastID, Title: "Clip saved", Body: what, Kind: toast.Success, Silent: true}
	if err != nil {
		n.Title, n.Body, n.Kind = "Couldn't save clip", err.Error(), toast.Error
	}
	toast.Post(n)
}

// playSaveSound plays the notification success/fail sound on its own, so it
// is heard with the save toast off and in exclusive-fullscreen games, which
// no card can draw over.
func playSaveSound(ok bool) {
	if ok {
		toast.PlaySound(toast.Success)
	} else {
		toast.PlaySound(toast.Error)
	}
}
