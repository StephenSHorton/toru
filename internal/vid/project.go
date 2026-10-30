package vid

import (
	"errors"
	"fmt"
	"math"

	"github.com/StephenSHorton/toru/internal/capture"
)

// project.go holds the editor's export request (docs/video-editor-v2.md and
// docs/video-editor-v3.md, Data model): the frontend's Project
// (frontend/src/trim/project.ts, toExportRequest) with every time in whole
// milliseconds.

// MediaJSON is one source file of a project, as MediaService registered it.
type MediaJSON struct {
	ID       string       `json:"id"`
	Path     string       `json:"path"`     // absolute
	URL      string       `json:"url"`      // preview only
	Kind     string       `json:"kind"`     // "video" | "image" | "audio"
	Duration int          `json:"duration"` // ms; 0 for images
	Width    int          `json:"width"`    // 0 for audio
	Height   int          `json:"height"`   //
	FPS      float64      `json:"fps"`      // 0 for image/audio
	HasAudio bool         `json:"hasAudio"` //
	Tracks   []AudioTrack `json:"tracks"`   // audio streams, titles, mix flag
}

// ClipJSON is one clip of a project as the frontend sends it
// (frontend/src/trim/project.ts, toExportRequest): the TS Clip with every
// time in whole milliseconds (keyframe times and speed-curve points too).
type ClipJSON struct {
	ID        string        `json:"id"`
	Media     string        `json:"media"`   // Media.id
	Start     int           `json:"start"`   // timeline position, ms
	In        int           `json:"in"`      // source range, ms
	Out       int           `json:"out"`     //
	Speed     float64       `json:"speed"`   // 0.25..4
	Volume    float64       `json:"volume"`  // 0..2
	Muted     bool          `json:"muted"`   //
	FadeIn    int           `json:"fadeIn"`  // ms
	FadeOut   int           `json:"fadeOut"` // ms
	Levels    []TrackLevel  `json:"levels,omitempty"`
	Transform TransformJSON `json:"transform"`
	Chroma    *ChromaJSON   `json:"chroma,omitempty"` // nil = off (v2)
	Filters   FiltersJSON   `json:"filters"`          // v2

	// v3 (docs/video-editor-v3.md); absent in v2 projects.
	Effects     []EffectJSON              `json:"effects,omitempty"`     // effects[0] runs first
	Anim        map[string][]KeyframeJSON `json:"anim,omitempty"`        // AnimTarget → keys
	SpeedCurve  *SpeedCurveJSON           `json:"speedCurve,omitempty"`  // overrides Speed
	Hold        bool                      `json:"hold,omitempty"`        // freeze frame: source frame In for Out-In ms, silent
	Reversed    bool                      `json:"reversed,omitempty"`    // source range plays backwards
	BlendFrames string                    `json:"blendFrames,omitempty"` // "" | "off" | "mix" | "smooth"
}

// TransformJSON places a clip on the canvas: centre (0..1 of the canvas),
// scale (1 = fit inside the canvas), opacity and (v3) rotation in degrees
// clockwise.
type TransformJSON struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Scale    float64 `json:"scale"`
	Opacity  float64 `json:"opacity"`
	Rotation float64 `json:"rotation,omitempty"`
}

// EffectJSON is one effect in a clip's stack (TS EffectInstance). Type is a
// registry id (internal/fx/defs/<type>.json); params missing from Params take
// the definition's default. A param value is a JSON number, bool, string
// ("#rrggbb", an enum id, a path) or [x, y].
type EffectJSON struct {
	ID      string                    `json:"id"`
	Type    string                    `json:"type"`
	Enabled bool                      `json:"enabled"`
	Params  map[string]any            `json:"params"`
	Keys    map[string][]KeyframeJSON `json:"keys,omitempty"`   // per param, sorted by T
	Group   string                    `json:"group,omitempty"`  // saved filter it came from
	Amount  *float64                  `json:"amount,omitempty"` // group intensity 0..1; nil = 1
}

// KeyframeJSON is one keyframe: T in ms from the clip's start on the
// timeline, the value there, and the ease to the next keyframe ("linear",
// "in", "out", "inOut", "hold").
type KeyframeJSON struct {
	T    int    `json:"t"`
	V    any    `json:"v"`
	Ease string `json:"ease"`
}

// SpeedCurveJSON is a speed ramp: speed piecewise linear in source time,
// points at S ms from the clip's In with speed V (0.1..10). Audio is
// "follow" (atempo, muted outside 0.25..4x) or "mute".
type SpeedCurveJSON struct {
	Points []SpeedPointJSON `json:"points"`
	Audio  string           `json:"audio"`
	Preset string           `json:"preset,omitempty"`
}

// SpeedPointJSON is one point of a speed curve.
type SpeedPointJSON struct {
	S int     `json:"s"` // ms from the clip's In, source time
	V float64 `json:"v"`
}

// TrackJSON is one timeline track. Clips are sorted by start and never
// overlap. A hidden video track draws nothing; a muted track plays no audio.
type TrackJSON struct {
	ID     string     `json:"id"`
	Kind   string     `json:"kind"` // "video" | "audio"
	Muted  bool       `json:"muted"`
	Hidden bool       `json:"hidden"`
	Locked bool       `json:"locked"`
	Clips  []ClipJSON `json:"clips"`
}

// TextJSON is a v1 text overlay in output time (ms).
type TextJSON struct {
	ID    string  `json:"id"`
	Text  string  `json:"text"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	Size  string  `json:"size"`
	Start int     `json:"start"`
	End   int     `json:"end"`
}

// CanvasJSON is the output frame before crop and the preset's scale.
type CanvasJSON struct {
	Width  int     `json:"width"`
	Height int     `json:"height"`
	FPS    float64 `json:"fps"`
}

// ProjectJSON is the whole project.
type ProjectJSON struct {
	Version int           `json:"version"`
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Canvas  CanvasJSON    `json:"canvas"`
	Media   []MediaJSON   `json:"media"`
	Tracks  []TrackJSON   `json:"tracks"` // video tracks bottom→top, then audio tracks
	Texts   []TextJSON    `json:"texts"`
	Crop    *capture.Rect `json:"crop"` // canvas pixels; nil = none
}

// ProjectExport is one v2 export: the project, v1's preset fields and the
// output path from the Save As dialog.
type ProjectExport struct {
	Project ProjectJSON `json:"project"`
	Preset  string      `json:"preset"` // "original" | "1080p" | "720p" | "discord" | "custom"
	Height  int         `json:"height,omitempty"`
	Kbps    int         `json:"kbps,omitempty"`
	OutPath string      `json:"outPath"`
}

// defaultCanvasFPS is used when the canvas has no frame rate (a project
// started from an image or audio file).
const defaultCanvasFPS = 30

// ClipLengthMs is a clip's length on the timeline: its source range over
// its speed.
func ClipLengthMs(c ClipJSON) int {
	return int(math.Round(float64(c.Out-c.In) / clipSpeed(c)))
}

// ProjectDurationMs is where the last clip ends (project.ts projectDuration).
func ProjectDurationMs(p ProjectJSON) int {
	end := 0
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			end = max(end, c.Start+ClipLengthMs(c))
		}
	}
	return end
}

// ProjectFrameSize is the output frame before the preset's scale: the
// canvas, or the crop. Text overlays are laid out in it.
func ProjectFrameSize(p ProjectJSON) (w, h int) {
	if p.Crop != nil {
		return evenDim(p.Crop.W), evenDim(p.Crop.H)
	}
	return evenDim(p.Canvas.Width), evenDim(p.Canvas.Height)
}

// Validate checks req so BuildProjectArgs only sees a project ffmpeg can run.
func (req ProjectExport) Validate() error {
	p := req.Project
	if p.Canvas.Width < 2 || p.Canvas.Height < 2 {
		return fmt.Errorf("export: canvas %dx%d is too small", p.Canvas.Width, p.Canvas.Height)
	}
	media := map[string]MediaJSON{}
	for _, m := range p.Media {
		if m.Path == "" {
			return fmt.Errorf("export: media %q has no file", m.ID)
		}
		switch m.Kind {
		case "video", "image":
			if m.Width < 1 || m.Height < 1 {
				return fmt.Errorf("export: %s has no picture size", m.Path)
			}
		case "audio":
		default:
			return fmt.Errorf("export: media %q has unknown kind %q", m.ID, m.Kind)
		}
		media[m.ID] = m
	}
	clips := 0
	for _, t := range p.Tracks {
		if t.Kind != "video" && t.Kind != "audio" {
			return fmt.Errorf("export: track %q has unknown kind %q", t.ID, t.Kind)
		}
		for _, c := range t.Clips {
			m, ok := media[c.Media]
			if !ok {
				return fmt.Errorf("export: clip %q uses missing media %q", c.ID, c.Media)
			}
			if t.Kind == "video" && m.Kind == "audio" {
				return fmt.Errorf("export: audio %s is on a video track", m.Path)
			}
			if t.Kind == "audio" && !m.HasAudio && m.Kind != "audio" {
				return fmt.Errorf("export: %s has no audio for an audio track", m.Path)
			}
			if c.Start < 0 || c.In < 0 || c.Out <= c.In {
				return fmt.Errorf("export: clip %q has a bad range (start %d, %d..%d ms)", c.ID, c.Start, c.In, c.Out)
			}
			if ClipLengthMs(c) < 1 {
				return fmt.Errorf("export: clip %q is too short", c.ID)
			}
			clips++
		}
	}
	if clips == 0 {
		return errors.New("export: the timeline is empty")
	}
	if c := p.Crop; c != nil {
		if c.W < 2 || c.H < 2 || c.X < 0 || c.Y < 0 || c.X+c.W > p.Canvas.Width || c.Y+c.H > p.Canvas.Height {
			return fmt.Errorf("export: crop %dx%d+%d+%d is outside the %dx%d canvas", c.W, c.H, c.X, c.Y, p.Canvas.Width, p.Canvas.Height)
		}
	}
	switch req.Preset {
	case "original", "1080p", "720p", "discord":
	case "custom":
		if req.Height < 2 || req.Height%2 != 0 {
			return fmt.Errorf("export: custom height %d must be even and at least 2", req.Height)
		}
		if req.Kbps < 0 {
			return fmt.Errorf("export: custom bitrate %d kbps", req.Kbps)
		}
	default:
		return fmt.Errorf("export: unknown preset %q", req.Preset)
	}
	if req.OutPath == "" {
		return errors.New("export: no output path")
	}
	for _, m := range p.Media {
		if sameFile(m.Path, req.OutPath) {
			return errors.New("export: the output would overwrite a source")
		}
	}
	return nil
}

// textOverlays turns the project's texts into v1 TextOverlays for
// TextFilter.
func (p ProjectJSON) textOverlays() []TextOverlay {
	out := make([]TextOverlay, 0, len(p.Texts))
	for _, t := range p.Texts {
		out = append(out, TextOverlay{Text: t.Text, X: t.X, Y: t.Y, Size: t.Size, StartMs: t.Start, EndMs: t.End})
	}
	return out
}

func clipSpeed(c ClipJSON) float64 {
	if c.Speed <= 0 || math.IsNaN(c.Speed) {
		return 1
	}
	return clampF(c.Speed, 0.25, 4)
}
