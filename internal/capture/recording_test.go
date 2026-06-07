package capture

import (
	"strings"
	"testing"
	"time"
)

// lavfiRecorder returns a Recorder whose "capture backends" are lavfi test
// sources, so the FULL lifecycle (spawn, grace window, handle map, 'q' stop,
// file verification) runs against a real ffmpeg without needing a desktop.
// candidates[i] is one backend attempt, exactly like ddagrab vs gdigrab.
func lavfiRecorder(t *testing.T, candidates ...[]string) *Recorder {
	t.Helper()
	if _, err := LocateFFmpeg(); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	r := NewRecorder()
	r.captureAudio = false // deterministic lavfi runs; audio has its own tests
	r.grace = 300 * time.Millisecond
	r.stopWait = 15 * time.Second
	r.argCandidates = func(_ CaptureRequest, _ []ScreenInfo, _ VideoEncoder, outPath string) [][]string {
		out := make([][]string, len(candidates))
		for i, c := range candidates {
			out[i] = append(append([]string{}, c...), outPath)
		}
		return out
	}
	r.screens = func() []ScreenInfo { return twoMonitors() }
	return r
}

// lavfiArgs is an infinite synthetic input encoded with the same realtime VP9
// settings the real recorder uses (output path appended by lavfiRecorder).
func lavfiArgs() []string {
	return []string{
		"-y", "-f", "lavfi", "-i", "testsrc=size=128x128:rate=15",
		"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "200k",
	}
}

func videoReq() CaptureRequest {
	return CaptureRequest{
		Mode: ModeVideo, Sub: SubRegion, MonitorID: 0,
		Rect: Rect{X: 0, Y: 0, W: 128, H: 128},
	}
}

func TestStartRecordingRejectsBadRequests(t *testing.T) {
	r := NewRecorder()
	if _, err := r.StartRecording(CaptureRequest{Mode: ModeScreenshot, Rect: Rect{W: 10, H: 10}}); err == nil {
		t.Error("expected error for Mode=screenshot, got nil")
	}
	if _, err := r.StartRecording(CaptureRequest{Mode: ModeVideo, Rect: Rect{W: 0, H: 10}}); err == nil {
		t.Error("expected error for zero-width rect, got nil")
	}
}

func TestStopUnknownHandle(t *testing.T) {
	r := NewRecorder()
	if _, err := r.StopRecording("nope"); err == nil || !strings.Contains(err.Error(), "unknown recording handle") {
		t.Errorf("want unknown-handle error, got %v", err)
	}
}

func TestRecorderRejectsSynchronousCapture(t *testing.T) {
	r := NewRecorder()
	if _, err := r.Capture(videoReq()); err == nil {
		t.Error("expected error from synchronous Capture, got nil")
	}
}

// TestRecordingLifecycle is the full happy path: start, record ~1s, graceful
// 'q' stop, non-empty playable artifact, handle consumed.
func TestRecordingLifecycle(t *testing.T) {
	r := lavfiRecorder(t, lavfiArgs())

	handle, err := r.StartRecording(videoReq())
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	time.Sleep(1 * time.Second)

	res, err := r.StopRecording(handle)
	if err != nil {
		t.Fatalf("StopRecording: %v", err)
	}
	if res.Mode != ModeVideo || res.VideoPath == "" {
		t.Errorf("bad result: %+v", res)
	}
	if !strings.HasSuffix(res.VideoPath, ".webm") {
		t.Errorf("default codec policy must produce .webm, got %q", res.VideoPath)
	}
	if res.Rect != videoReq().Rect {
		t.Errorf("result must echo the request rect: %+v", res.Rect)
	}
	// Handle must be consumed.
	if _, err := r.StopRecording(handle); err == nil {
		t.Error("expected unknown-handle error on second stop")
	}
}

// TestRecordingBackendFallback: the first backend dies inside the grace
// window (bogus lavfi graph), the second succeeds — mirroring ddagrab
// failing in an RDP session and gdigrab taking over.
func TestRecordingBackendFallback(t *testing.T) {
	bogus := []string{"-y", "-f", "lavfi", "-i", "nosuchsource=broken", "-c:v", "libvpx-vp9"}
	r := lavfiRecorder(t, bogus, lavfiArgs())

	handle, err := r.StartRecording(videoReq())
	if err != nil {
		t.Fatalf("StartRecording should fall back to the second backend: %v", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := r.StopRecording(handle); err != nil {
		t.Fatalf("StopRecording after fallback: %v", err)
	}
}

// TestAllBackendsFailing: every backend dies inside the grace window — the
// start must fail with the ffmpeg stderr in the error, not hang or leak.
func TestAllBackendsFailing(t *testing.T) {
	bogus := []string{"-y", "-f", "lavfi", "-i", "nosuchsource=broken", "-c:v", "libvpx-vp9"}
	r := lavfiRecorder(t, bogus)

	_, err := r.StartRecording(videoReq())
	if err == nil {
		t.Fatal("expected StartRecording to fail when every backend dies")
	}
	if !strings.Contains(err.Error(), "all capture backends failed") {
		t.Errorf("error should name the failure mode, got: %v", err)
	}
}

// TestStopAfterPrematureExit: ffmpeg ends on its own (finite input) after the
// grace window; StopRecording must report the premature death, not success.
func TestStopAfterPrematureExit(t *testing.T) {
	// -re paces the synthetic input at native rate; without it ffmpeg encodes
	// the whole finite clip faster than the grace window and "dies at startup".
	finite := []string{
		"-y", "-re", "-f", "lavfi", "-i", "testsrc=size=128x128:rate=15:duration=1",
		"-c:v", "libvpx-vp9", "-deadline", "realtime", "-cpu-used", "8", "-b:v", "200k",
	}
	r := lavfiRecorder(t, finite)

	handle, err := r.StartRecording(videoReq())
	if err != nil {
		t.Fatalf("StartRecording: %v", err)
	}
	time.Sleep(1500 * time.Millisecond) // let the finite input run out

	if _, err := r.StopRecording(handle); err == nil || !strings.Contains(err.Error(), "ended prematurely") {
		t.Errorf("want premature-end error, got %v", err)
	}
}

func TestTailBufferKeepsTail(t *testing.T) {
	tb := &tailBuffer{max: 8}
	for _, s := range []string{"aaaa", "bbbb", "cccc"} {
		if _, err := tb.Write([]byte(s)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if got := tb.String(); got != "bbbbcccc" {
		t.Errorf("tailBuffer = %q, want %q", got, "bbbbcccc")
	}
}
