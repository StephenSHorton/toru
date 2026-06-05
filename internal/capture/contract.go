// Package capture is the SHARED CORE seam between the two developers.
//
// It defines the ONE data contract that crosses the screenshot <-> video
// boundary (see the structs below) and the single Capture() entrypoint that
// dispatches on Mode. Neither the screenshot editor (Developer 1) nor the
// video/trim editor (Developer 2) imports the other; both import ONLY this
// package's contract and subscribe to the "capture:done" event.
//
// CONTRACT FREEZE RULE: changes to the structs in this file require sign-off
// from BOTH developers and must keep `go build ./internal/capture` green.
package capture

// Rect is ALWAYS in virtual-desktop PHYSICAL pixels.
//
// Origin = primary-monitor top-left. Monitors positioned to the LEFT of or
// ABOVE the primary monitor therefore have NEGATIVE X/Y.
//
// Coordinate contract (THE single most important seam detail):
//   - ffmpeg `gdigrab`  consumes X/Y DIRECTLY (its offset_x/offset_y are
//     virtual-desktop coordinates; negatives are allowed). No conversion.
//   - ffmpeg `ddagrab`  does NOT. Its offset_x/offset_y are MONITOR-RELATIVE
//     and the monitor is selected by output_idx. The SAME Rect cannot feed
//     both unchanged.
//
// Therefore internal/capture/args.go is the SOLE owner of rebasing: for the
// ddagrab path it sets output_idx = MonitorID and emits
// offset_x = Rect.X - screen.X, offset_y = Rect.Y - screen.Y.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// CaptureRequest is what the overlay emits on commit. It is the entire input
// to the shared Capture() seam.
type CaptureRequest struct {
	Mode          string  `json:"mode"`                // "screenshot" | "video"
	Sub           string  `json:"sub"`                 // "region" | "window" | "fullscreen"
	MonitorID     int     `json:"monitorId"`           // index; == ddagrab output_idx
	Rect          Rect    `json:"rect"`                // physical px, virtual-desktop origin
	DPIScale      float64 `json:"dpiScale"`            // scale factor of the owning monitor
	IncludeCursor bool    `json:"includeCursor"`       // draw_mouse / DXGI cursor compose
	CountdownSec  int     `json:"countdownSec"`        // 0 | 5 | 10 (video only)
	MicDevice     string  `json:"micDevice,omitempty"` // v1.1 audio
	CopyOnCommit  bool    `json:"copyOnCommit"`        // copy to clipboard instead of save
}

// CaptureResult is what Capture() returns and what the "capture:done" event
// carries. The `Mode` field is what routes the result to Developer 1's editor
// (screenshot) vs Developer 2's editor (video).
type CaptureResult struct {
	Mode      string `json:"mode"`
	ImagePath string `json:"imagePath,omitempty"` // set when Mode == "screenshot"
	VideoPath string `json:"videoPath,omitempty"` // set when Mode == "video" (on stop)
	HandleID  string `json:"handleId,omitempty"`  // long-lived recording handle
	Rect      Rect   `json:"rect"`
	MonitorID int    `json:"monitorId"`
	Cancelled bool   `json:"cancelled"`
}

// TrimRequest is Developer 2's input to the video trimmer.
type TrimRequest struct {
	VideoPath string `json:"videoPath"`
	StartMs   int    `json:"startMs"`
	EndMs     int    `json:"endMs"`
	Precise   bool   `json:"precise"` // true = re-encode (frame-accurate); false = -c copy
	OutPath   string `json:"outPath"`
}

// ScreenInfo describes one monitor. Overlay.ListScreens() is the SINGLE source
// of truth for screen enumeration that both halves trust.
type ScreenInfo struct {
	ID          int     `json:"id"` // index; == ddagrab output_idx
	X           int     `json:"x"`  // physical px, virtual-desktop origin (may be negative)
	Y           int     `json:"y"`
	W           int     `json:"w"`
	H           int     `json:"h"`
	ScaleFactor float64 `json:"scaleFactor"`
	IsPrimary   bool    `json:"isPrimary"`
}

// Capture mode/sub constants (avoid magic strings across the seam).
const (
	ModeScreenshot = "screenshot"
	ModeVideo      = "video"

	SubRegion     = "region"
	SubWindow     = "window"
	SubFullscreen = "fullscreen"
)
