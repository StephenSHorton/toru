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

// AudioInput describes a raw PCM stream ffmpeg should mux alongside the
// video, exactly as the arg builder needs it.
type AudioInput struct {
	PipePath   string // \\.\pipe\… ffmpeg opens this as a file
	SampleFmt  string // ffmpeg -f value: "f32le" | "s16le"
	SampleRate int
	Channels   int
}

// audioSource is one live audio capture session (WASAPI loopback / process
// loopback on Windows; see audio_windows.go, audio_process_windows.go).
type audioSource interface {
	Input() AudioInput
	Stop()
}

// AudioSession is one application currently producing audio — a row in the
// per-app capture picker.
type AudioSession struct {
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
}

// AudioConfig is the user's audio-source selection. EVERY field is an
// explicit opt-in; the zero value records NO audio whatsoever.
type AudioConfig struct {
	System    bool     `json:"system"`    // whole system mix ("what you hear")
	AppPIDs   []uint32 `json:"appPids"`   // capture ONLY these process trees
	MicDevice string   `json:"micDevice"` // dshow device name; "" = no mic
}

// enabled reports whether any source is selected.
func (c AudioConfig) enabled() bool {
	return c.System || len(c.AppPIDs) > 0 || c.MicDevice != ""
}

// Recorder is the production video Capturer. Zero-value is not usable; build
// with NewRecorder.
type Recorder struct {
	mu   sync.Mutex
	seq  int
	recs map[string]*recording

	// audioConfig selects which audio sources to capture. The ZERO VALUE
	// records no audio — every source is a privacy-sensitive action the user
	// must OPT INTO via the overlay's Audio picker (SetAudioConfig). Source
	// failures degrade gracefully, never block the recording.
	audioConfig AudioConfig

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
	audio   []audioSource // empty when recording video-only
	done    chan struct{} // closed when cmd.Wait returns
	waitErr error         // valid only after done is closed
}

// NewRecorder returns a Recorder wired to the real screen enumeration and the
// ddagrab→gdigrab argument candidates.
func NewRecorder() *Recorder {
	return &Recorder{
		recs: map[string]*recording{},
		// audioConfig zero value: NO audio is recorded until the user opts in.
		screens:       enumScreens,
		argCandidates: defaultArgCandidates,
		grace:         startGrace,
		stopWait:      stopTimeout,
	}
}

// SetAudioConfig replaces the audio-source selection for FUTURE recordings
// (the user-facing opt-in; in-flight recordings are unaffected).
func (r *Recorder) SetAudioConfig(c AudioConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audioConfig = c
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
// first (only if the request's monitor is enumerable for the rebase AND its
// DXGI output index resolves), then the software gdigrab fallback.
//
// ddagrab is SKIPPED entirely when the DXGI index cannot be resolved: falling
// back to req.MonitorID there would silently record the WRONG monitor on
// machines where the two enumeration orders disagree — a corrupt-but-working
// capture is worse than the slower gdigrab path, which uses virtual-desktop
// coordinates and cannot miss.
func defaultArgCandidates(req CaptureRequest, screens []ScreenInfo, enc VideoEncoder, outPath string) [][]string {
	var out [][]string
	if screen, err := findScreen(screens, req.MonitorID); err == nil {
		if ddaIdx, ok := DDAOutputIndex(screen); ok {
			if dda, err := BuildVideoArgsDDA(req, screens, ddaIdx, enc, outPath); err == nil {
				out = append(out, dda)
			}
		}
	}
	out = append(out, BuildVideoArgsGDI(req, enc, outPath))
	return out
}

// DDAOutputIndex resolves the ddagrab output_idx for screen by matching its
// virtual-desktop bounds against the DXGI output enumeration. ok=false means
// no trustworthy mapping exists (use gdigrab instead — never guess).
func DDAOutputIndex(screen ScreenInfo) (int, bool) {
	return dxgiOutputIndexFor(DisplayBounds{
		Index: screen.ID, X: screen.X, Y: screen.Y, W: screen.W, H: screen.H,
	})
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

	// Audio sources: ALL opt-in (see SetAudioConfig) and best-effort. Loopback
	// pipes must exist BEFORE ffmpeg spawns (it opens them like files); any
	// source failing to init is skipped (video and the other sources proceed)
	// — never block the recording.
	r.mu.Lock()
	audioCfg := r.audioConfig
	r.mu.Unlock()
	var sources []audioSource
	if audioCfg.enabled() {
		if audioCfg.System {
			if a, audErr := startLoopbackAudio(fmt.Sprintf("toru-audio-sys-%d", time.Now().UnixNano())); audErr == nil {
				sources = append(sources, a)
			}
		}
		for _, pid := range audioCfg.AppPIDs {
			if a, audErr := startProcessLoopbackAudio(pid, fmt.Sprintf("toru-audio-app%d-%d", pid, time.Now().UnixNano())); audErr == nil {
				sources = append(sources, a)
			}
		}
	}
	stopSources := func() {
		for _, s := range sources {
			s.Stop()
		}
	}

	candidates := r.argCandidates(req, r.screens(), enc, outPath)
	if len(sources) > 0 || audioCfg.MicDevice != "" {
		inputs := make([]AudioInput, len(sources))
		for i, s := range sources {
			inputs[i] = s.Input()
		}
		for i := range candidates {
			candidates[i] = injectAudioMix(candidates[i], inputs, audioCfg.MicDevice)
		}
	}

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
		// Every backend failed; don't leave a partial file or live pumps behind.
		stopSources()
		_ = os.Remove(outPath)
		return "", fmt.Errorf("start recording: all capture backends failed: %w", errors.Join(attemptErrs...))
	}
	rec.audio = sources

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
	// Kill-on-job-close guard: a force-killed/crashed app must never leave an
	// orphaned ffmpeg silently recording the screen.
	tieToProcessLifetime(cmd)

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
		for _, s := range rec.audio {
			s.Stop()
		}
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
	for _, s := range rec.audio {
		s.Stop()
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
