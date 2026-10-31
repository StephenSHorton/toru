package vid

import (
	"errors"
	"fmt"

	"github.com/StephenSHorton/toru/internal/capture"
)

// edit.go holds the video editor's request types (docs/video-editor.md). The
// frontend gets them through the VideoService bindings.

// Segment is one kept range of a source, in output order.
type Segment struct {
	Source  int `json:"source"`  // index into EditRequest.Sources
	StartMs int `json:"startMs"` // source time
	EndMs   int `json:"endMs"`
}

// TrackLevel sets one source audio track's level in the export.
type TrackLevel struct {
	Source int  `json:"source"` // index into Sources
	Index  int  `json:"index"`  // audio stream index in that source (0 = first)
	Volume int  `json:"volume"` // percent, 0..200
	Muted  bool `json:"muted"`
}

// TextOverlay is one piece of text drawn over the output.
type TextOverlay struct {
	Text    string  `json:"text"`
	X       float64 `json:"x"`                // centre of the text, 0..1 of output width (after crop)
	Y       float64 `json:"y"`                // centre, 0..1 of output height
	Size    string  `json:"size"`             // "s" | "m" | "l" = 4% | 6% | 9% of output height; used when FontPx is 0
	FontPx  int     `json:"fontPx,omitempty"` // font size in output pixels (after crop); 0 = from Size
	Font    string  `json:"font,omitempty"`   // installed font's name (InstalledFonts); "" or missing = DefaultTextFont
	StartMs int     `json:"startMs"`          // OUTPUT time
	EndMs   int     `json:"endMs"`
}

// EditRequest is everything one export needs.
type EditRequest struct {
	Sources  []string      `json:"sources"`          // absolute paths; Sources[0] sets size/fps
	Segments []Segment     `json:"segments"`         // output order, at least one
	Crop     *capture.Rect `json:"crop,omitempty"`   // pixels of the Sources[0]-sized frame
	Tracks   []TrackLevel  `json:"tracks,omitempty"` // a source with none listed keeps its mix track at 100%
	Texts    []TextOverlay `json:"texts,omitempty"`
	Preset   string        `json:"preset"`           // "original" | "1080p" | "720p" | "discord" | "custom"
	Height   int           `json:"height,omitempty"` // custom only: output height (even), width keeps aspect
	Kbps     int           `json:"kbps,omitempty"`   // custom only: video bitrate
	OutPath  string        `json:"outPath"`          // from the Save As dialog; required
}

// MediaInfo is what ProbeMedia reports about one source.
type MediaInfo struct {
	DurationMs int          `json:"durationMs"`
	Width      int          `json:"width"`
	Height     int          `json:"height"`
	FPS        float64      `json:"fps"`
	HasAudio   bool         `json:"hasAudio"`
	Tracks     []AudioTrack `json:"tracks"`
	// ColorSpace is the video's YUV matrix tag as ffprobe names it (bt709,
	// bt470bg, smpte170m, ...), "" when untagged. The preview needs it: the
	// browser picks the matrix by frame height, ffmpeg by this tag.
	ColorSpace string `json:"colorSpace"`
}

// AudioTrack is one audio stream of a source.
type AudioTrack struct {
	Index int    `json:"index"`
	Title string `json:"title"` // stream title/handler_name; "" if none
	IsMix bool   `json:"isMix"` // the default mixed track when separate tracks exist
}

// Validate checks req against the probed sources (info[i] describes
// req.Sources[i]) so BuildEditArgs only ever sees a request ffmpeg can run.
func (req EditRequest) Validate(info []MediaInfo) error {
	if len(req.Sources) == 0 {
		return errors.New("edit: no sources")
	}
	if len(info) != len(req.Sources) {
		return fmt.Errorf("edit: %d sources but %d probes", len(req.Sources), len(info))
	}
	if info[0].Width < 2 || info[0].Height < 2 {
		return errors.New("edit: first source has no video size")
	}
	if len(req.Segments) == 0 {
		return errors.New("edit: no segments")
	}
	for i, s := range req.Segments {
		if s.Source < 0 || s.Source >= len(req.Sources) {
			return fmt.Errorf("edit: segment %d: no source %d", i, s.Source)
		}
		if s.StartMs < 0 || s.EndMs <= s.StartMs {
			return fmt.Errorf("edit: segment %d: bad range %d..%d ms", i, s.StartMs, s.EndMs)
		}
		if d := info[s.Source].DurationMs; d > 0 && s.StartMs >= d {
			return fmt.Errorf("edit: segment %d starts at %d ms, past the source's end (%d ms)", i, s.StartMs, d)
		}
	}
	for _, t := range req.Tracks {
		if t.Source < 0 || t.Source >= len(req.Sources) {
			return fmt.Errorf("edit: track level for missing source %d", t.Source)
		}
		if !hasTrack(info[t.Source], t.Index) {
			return fmt.Errorf("edit: source %d has no audio track %d", t.Source, t.Index)
		}
		if t.Volume < 0 || t.Volume > 200 {
			return fmt.Errorf("edit: volume %d%% out of 0..200", t.Volume)
		}
	}
	if c := req.Crop; c != nil {
		if c.W < 2 || c.H < 2 || c.X < 0 || c.Y < 0 || c.X+c.W > info[0].Width || c.Y+c.H > info[0].Height {
			return fmt.Errorf("edit: crop %dx%d+%d+%d is outside the %dx%d frame", c.W, c.H, c.X, c.Y, info[0].Width, info[0].Height)
		}
	}
	switch req.Preset {
	case "original", "1080p", "720p", "discord":
	case "custom":
		if req.Height < 2 || req.Height%2 != 0 {
			return fmt.Errorf("edit: custom height %d must be even and at least 2", req.Height)
		}
		if req.Kbps < 0 {
			return fmt.Errorf("edit: custom bitrate %d kbps", req.Kbps)
		}
	default:
		return fmt.Errorf("edit: unknown preset %q", req.Preset)
	}
	if req.OutPath == "" {
		return errors.New("edit: no output path")
	}
	for _, src := range req.Sources {
		if sameFile(src, req.OutPath) {
			return errors.New("edit: the output would overwrite a source")
		}
	}
	return nil
}

func hasTrack(m MediaInfo, index int) bool {
	for _, t := range m.Tracks {
		if t.Index == index {
			return true
		}
	}
	return false
}
