package capture

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

// sampleFS holds the checked-in stub media. While the real capture pipeline is
// being built (Phase 0, in parallel, behind this seam), Capture() returns these
// samples so BOTH editors are fully runnable from day one against a real path.
//
//go:embed sample/sample.png sample/sample.mp4
var sampleFS embed.FS

// Capturer is the shared capture seam. There is exactly ONE implementation in
// production; the stub below lets the two editors develop independently.
//
// Developer split:
//   - The screenshot still path (DXGI via kbinani/screenshot) lands in still_dxgi.go.
//   - The video path (FFmpeg ddagrab/gdigrab) lands in args.go + recording.go.
//   - args.go OWNS the virtual-desktop -> monitor-relative ddagrab rebasing.
type Capturer interface {
	Capture(req CaptureRequest) (CaptureResult, error)
	StartRecording(req CaptureRequest) (handleID string, err error)
	StopRecording(handleID string) (CaptureResult, error)
}

// StubCapturer returns checked-in sample media. Swap for the real Capturer in
// main.go once the DXGI/FFmpeg paths are wired; the contract does not move, so
// neither editor is disturbed.
type StubCapturer struct {
	// OutDir is where stub artifacts are written. Empty => os.TempDir()/toru.
	OutDir string
}

func (s *StubCapturer) outDir() (string, error) {
	dir := s.OutDir
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "toru")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func (s *StubCapturer) writeSample(name, dst string) (string, error) {
	b, err := sampleFS.ReadFile("sample/" + name)
	if err != nil {
		return "", fmt.Errorf("read embedded sample %q: %w", name, err)
	}
	if err := os.WriteFile(dst, b, 0o644); err != nil {
		return "", fmt.Errorf("write sample to %q: %w", dst, err)
	}
	return dst, nil
}

// Capture implements the screenshot path (and the synchronous video-less path).
// STUB: writes the embedded sample.png and returns its path.
func (s *StubCapturer) Capture(req CaptureRequest) (CaptureResult, error) {
	dir, err := s.outDir()
	if err != nil {
		return CaptureResult{}, err
	}
	res := CaptureResult{Mode: req.Mode, Rect: req.Rect, MonitorID: req.MonitorID}
	switch req.Mode {
	case ModeScreenshot:
		p, err := s.writeSample("sample.png", filepath.Join(dir, "toru-stub.png"))
		if err != nil {
			return CaptureResult{}, err
		}
		res.ImagePath = p
	case ModeVideo:
		// For video the overlay should call StartRecording/StopRecording; this
		// branch exists so a direct Capture() in video mode still yields a path.
		p, err := s.writeSample("sample.mp4", filepath.Join(dir, "toru-stub.mp4"))
		if err != nil {
			return CaptureResult{}, err
		}
		res.VideoPath = p
	default:
		return CaptureResult{}, fmt.Errorf("unknown capture mode %q", req.Mode)
	}
	return res, nil
}

// StartRecording STUB: returns a fixed handle. The real implementation spawns a
// long-lived ffmpeg (ddagrab->gdigrab) writing to a temp file.
func (s *StubCapturer) StartRecording(req CaptureRequest) (string, error) {
	if req.Mode != ModeVideo {
		return "", fmt.Errorf("StartRecording requires Mode=video, got %q", req.Mode)
	}
	return "stub-recording-0", nil
}

// StopRecording STUB: writes the embedded sample.mp4 and returns its path.
// The real implementation writes 'q' to ffmpeg stdin for a clean moov atom.
func (s *StubCapturer) StopRecording(handleID string) (CaptureResult, error) {
	dir, err := s.outDir()
	if err != nil {
		return CaptureResult{}, err
	}
	p, err := s.writeSample("sample.mp4", filepath.Join(dir, "toru-stub.mp4"))
	if err != nil {
		return CaptureResult{}, err
	}
	return CaptureResult{Mode: ModeVideo, VideoPath: p, HandleID: handleID}, nil
}

// compile-time assertion that the stub satisfies the seam.
var _ Capturer = (*StubCapturer)(nil)
