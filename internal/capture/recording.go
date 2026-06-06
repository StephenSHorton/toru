package capture

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// recording.go is the REAL video recording lifecycle (Developer 2). One
// long-lived ffmpeg process per recording:
//
//   StartRecording: try the GPU ddagrab path first; if ffmpeg dies within the
//     grace window (no D3D11 duplication in RDP/VM sessions, no usable GPU),
//     fall back to software gdigrab. args.go owns the coordinate rebasing
//     difference between the two; encoders.go owns the codec choice.
//
//   StopRecording: write 'q' to ffmpeg's stdin so the muxer finalizes cleanly
//     (intact moov atom / WebM cues), with a kill fallback on timeout.
//
// Recorder satisfies the frozen Capturer seam, so main.go swaps it in behind
// RealCapturer without disturbing either editor or the contract.

const (
	// startGrace is how long a freshly spawned ffmpeg must survive before we
	// trust the backend. ddagrab failures (no Desktop Duplication access)
	// surface within a few hundred ms; 1.2s adds margin for slow disks.
	startGrace = 1200 * time.Millisecond

	// stopTimeout bounds the graceful 'q' shutdown before we hard-kill. Muxer
	// finalization is normally <1s; 5s covers a flushing encoder under load.
	stopTimeout = 5 * time.Second

	// stderrTailSize is how much trailing ffmpeg stderr we keep for error
	// messages. ffmpeg's last lines carry the actionable failure reason.
	stderrTailSize = 4096
)

// Recorder is the production video Capturer. Zero-value is not usable; build
// with NewRecorder.
type Recorder struct {
	mu   sync.Mutex
	seq  int
	recs map[string]*recording

	// Seams below are defaulted in NewRecorder and overridden ONLY by tests
	// (they let the lifecycle run against lavfi inputs instead of a desktop).
	screens       func() []ScreenInfo
	argCandidates func(req CaptureRequest, screens []ScreenInfo, enc VideoEncoder, outPath string) [][]string
	grace         time.Duration
	stopWait      time.Duration
}

// recording is one live ffmpeg process.
type recording struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	outPath string
	req     CaptureRequest
	stderr  *tailBuffer
	done    chan struct{} // closed when cmd.Wait returns
	waitErr error         // valid only after done is closed
}

// NewRecorder returns a Recorder wired to the real screen enumeration and the
// ddagrab→gdigrab argument candidates.
func NewRecorder() *Recorder {
	return &Recorder{
		recs:          map[string]*recording{},
		screens:       enumScreens,
		argCandidates: defaultArgCandidates,
		grace:         startGrace,
		stopWait:      stopTimeout,
	}
}

// enumScreens adapts EnumDisplays to the contract's ScreenInfo. Only ID/X/Y
// matter for the ddagrab rebase; ScaleFactor/IsPrimary are display-chrome
// concerns owned by the overlay's enriched ListScreens.
func enumScreens() []ScreenInfo {
	displays := EnumDisplays()
	out := make([]ScreenInfo, 0, len(displays))
	for _, d := range displays {
		out = append(out, ScreenInfo{
			ID: d.Index, X: d.X, Y: d.Y, W: d.W, H: d.H,
			ScaleFactor: 1.0, IsPrimary: d.X == 0 && d.Y == 0,
		})
	}
	return out
}

// defaultArgCandidates returns the backend attempts in order: GPU ddagrab
// first (only if the request's monitor is enumerable for the rebase), then
// the software gdigrab fallback.
func defaultArgCandidates(req CaptureRequest, screens []ScreenInfo, enc VideoEncoder, outPath string) [][]string {
	var out [][]string
	if dda, err := BuildVideoArgsDDA(req, screens, enc, outPath); err == nil {
		out = append(out, dda)
	}
	out = append(out, BuildVideoArgsGDI(req, enc, outPath))
	return out
}

// Capture exists to satisfy the Capturer seam. The Recorder owns ONLY the
// long-lived video path; stills are RealCapturer's job, and synchronous video
// capture is not a flow the overlay produces (video always goes through
// StartRecording/StopRecording — see OverlayService.Commit).
func (r *Recorder) Capture(req CaptureRequest) (CaptureResult, error) {
	return CaptureResult{}, fmt.Errorf("recorder: synchronous Capture(%q) unsupported; use StartRecording/StopRecording", req.Mode)
}

// StartRecording spawns ffmpeg for req and returns a handle for StopRecording.
func (r *Recorder) StartRecording(req CaptureRequest) (string, error) {
	if req.Mode != ModeVideo {
		return "", fmt.Errorf("StartRecording requires Mode=%q, got %q", ModeVideo, req.Mode)
	}
	if req.Rect.W <= 0 || req.Rect.H <= 0 {
		return "", fmt.Errorf("StartRecording: invalid rect %dx%d (W/H must be > 0)", req.Rect.W, req.Rect.H)
	}
	bin, err := LocateFFmpeg()
	if err != nil {
		return "", err
	}

	enc := SelectEncoder(req)
	outPath, err := r.newOutPath(enc.Ext)
	if err != nil {
		return "", err
	}

	candidates := r.argCandidates(req, r.screens(), enc, outPath)
	var rec *recording
	var attemptErrs []error
	for _, args := range candidates {
		rec, err = r.spawn(bin, args, req, outPath)
		if err == nil {
			break
		}
		attemptErrs = append(attemptErrs, err)
	}
	if rec == nil {
		// Every backend failed; don't leave a partial file behind.
		_ = os.Remove(outPath)
		return "", fmt.Errorf("start recording: all capture backends failed: %w", errors.Join(attemptErrs...))
	}

	r.mu.Lock()
	r.seq++
	handle := fmt.Sprintf("rec-%d", r.seq)
	r.recs[handle] = rec
	r.mu.Unlock()
	return handle, nil
}

// spawn starts one ffmpeg attempt and watches it through the grace window:
// an exit inside the window means this backend cannot capture here (the
// caller then tries the next candidate).
func (r *Recorder) spawn(bin string, args []string, req CaptureRequest, outPath string) (*recording, error) {
	full := append([]string{"-hide_banner", "-loglevel", "warning"}, args...)
	cmd := exec.Command(bin, full...)
	configureSysProcAttr(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("spawn ffmpeg: stdin pipe: %w", err)
	}
	tail := &tailBuffer{max: stderrTailSize}
	cmd.Stderr = tail

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("spawn ffmpeg: %w", err)
	}

	rec := &recording{
		cmd: cmd, stdin: stdin, outPath: outPath, req: req,
		stderr: tail, done: make(chan struct{}),
	}
	go func() {
		rec.waitErr = cmd.Wait()
		close(rec.done)
	}()

	select {
	case <-rec.done:
		return nil, fmt.Errorf("ffmpeg exited during startup: %v\n%s", rec.waitErr, rec.stderr)
	case <-time.After(r.grace):
		return rec, nil
	}
}

// StopRecording finalizes the recording behind handleID: graceful 'q' first
// (clean container finalization), kill on timeout. The handle is consumed
// either way.
func (r *Recorder) StopRecording(handleID string) (CaptureResult, error) {
	r.mu.Lock()
	rec, ok := r.recs[handleID]
	delete(r.recs, handleID)
	r.mu.Unlock()
	if !ok {
		return CaptureResult{}, fmt.Errorf("unknown recording handle %q", handleID)
	}

	select {
	case <-rec.done:
		// ffmpeg died on its own mid-recording (disk full, device lost…).
		return CaptureResult{}, fmt.Errorf("recording %s ended prematurely: %v\n%s", handleID, rec.waitErr, rec.stderr)
	default:
	}

	// Graceful stop: 'q' on stdin lets the muxer write a complete trailer.
	// A failed write means the pipe broke under us — the kill path below
	// still runs, so the error is intentionally not fatal here.
	_, _ = io.WriteString(rec.stdin, "q")
	_ = rec.stdin.Close()

	killed := false
	select {
	case <-rec.done:
	case <-time.After(r.stopWait):
		killed = true
		_ = rec.cmd.Process.Kill()
		<-rec.done
	}

	// Trust the artifact, not the exit code: 'q' exits 0 on the builds we
	// target, but the contract with callers is "VideoPath is playable".
	fi, statErr := os.Stat(rec.outPath)
	if statErr != nil || fi.Size() == 0 {
		return CaptureResult{}, fmt.Errorf("recording %s produced no output (%v, exit: %v)\n%s",
			handleID, statErr, rec.waitErr, rec.stderr)
	}
	if killed {
		// The file exists but the muxer never finalized — surface it instead
		// of handing the trim editor a corrupt artifact.
		return CaptureResult{}, fmt.Errorf("recording %s did not stop within %s and was killed; output %q may be unplayable\n%s",
			handleID, r.stopWait, rec.outPath, rec.stderr)
	}

	return CaptureResult{
		Mode:      ModeVideo,
		VideoPath: rec.outPath,
		HandleID:  handleID,
		Rect:      rec.req.Rect,
		MonitorID: rec.req.MonitorID,
	}, nil
}

// newOutPath allocates a unique output path in the app temp dir, e.g.
// %TEMP%\toru\toru-rec-20260606-152233-1.webm.
func (r *Recorder) newOutPath(ext string) (string, error) {
	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("recording temp dir: %w", err)
	}
	r.mu.Lock()
	r.seq++
	n := r.seq
	r.mu.Unlock()
	name := fmt.Sprintf("toru-rec-%s-%d%s", time.Now().Format("20060102-150405"), n, ext)
	return filepath.Join(dir, name), nil
}

// tailBuffer is an io.Writer that keeps only the LAST max bytes written —
// ffmpeg's stderr can run for minutes, but only the tail explains a failure.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// compile-time assertion that the Recorder satisfies the frozen seam.
var _ Capturer = (*Recorder)(nil)
