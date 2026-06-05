// Package thumbnail is the SHARED post-capture floating thumbnail shell. It
// appears bottom-right after every capture; click opens the right editor, ignore
// auto-saves, drag does an OS drag-and-drop of the file. It is parameterized by
// media type (image vs video) so both halves reuse it.
package thumbnail

// MediaType distinguishes the thumbnail's behavior/preview.
type MediaType string

const (
	Image MediaType = "image"
	Video MediaType = "video"
)

// Request describes a thumbnail to show.
type Request struct {
	MediaType MediaType
	Path      string
	AutoSave  bool // true => save to default folder if ignored
}

// Show displays the floating thumbnail for path.
//
// TODO(shared): create a small frameless always-on-top Wails window anchored
// bottom-right that loads /thumbnail with the path, auto-dismiss after ~5s.
func Show(_ Request) error { return nil }
