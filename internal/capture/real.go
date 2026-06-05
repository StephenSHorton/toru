package capture

import "fmt"

// RealCapturer is the production Capturer. The still (screenshot) path uses the
// real OS capture (captureStill, via kbinani/screenshot on Windows); the video
// path is delegated to an embedded Capturer (the StubCapturer for now, until
// the FFmpeg ddagrab/gdigrab pipeline lands behind the same seam).
//
// It satisfies the frozen Capturer interface, so swapping it in main.go does
// not disturb either editor or the contract.
type RealCapturer struct {
	video Capturer // video = the StubCapturer for now
}

// NewRealCapturer builds a RealCapturer that delegates video capture/recording
// to the provided Capturer (typically &StubCapturer{}).
func NewRealCapturer(video Capturer) *RealCapturer {
	return &RealCapturer{video: video}
}

// Capture dispatches on Mode: real still capture for screenshots, delegated to
// the embedded video Capturer for video.
func (c *RealCapturer) Capture(req CaptureRequest) (CaptureResult, error) {
	switch req.Mode {
	case ModeScreenshot:
		p, err := captureStill(req)
		if err != nil {
			return CaptureResult{}, err
		}
		return CaptureResult{
			Mode:      ModeScreenshot,
			ImagePath: p,
			Rect:      req.Rect,
			MonitorID: req.MonitorID,
		}, nil
	case ModeVideo:
		return c.video.Capture(req)
	default:
		return CaptureResult{}, fmt.Errorf("unknown capture mode %q", req.Mode)
	}
}

// StartRecording delegates to the embedded video Capturer.
func (c *RealCapturer) StartRecording(req CaptureRequest) (string, error) {
	return c.video.StartRecording(req)
}

// StopRecording delegates to the embedded video Capturer.
func (c *RealCapturer) StopRecording(handleID string) (CaptureResult, error) {
	return c.video.StopRecording(handleID)
}

// compile-time assertion that RealCapturer satisfies the seam (all 3 methods).
var _ Capturer = (*RealCapturer)(nil)
