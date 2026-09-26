package capture

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// share.go is the live-stream encode path. It reuses the recording grab
// (ddagrab, then gdigrab) but muxes a stream instead of a file.
//
// Two transports, one ffmpeg:
//   - HLS + hardware H.264 when nvenc, Quick Sync, or AMF probes usable.
//     Browsers and TV players speak this. libx264 is never selected: same
//     AVC-patent rule as recordings (encoders.go).
//   - MJPEG over a pipe when no hardware H.264 encoder exists. Every browser
//     can show it, including ones that cannot play HLS. Heavier on the
//     network, and video-only.

const (
	ShareHLS   = "hls"
	ShareMJPEG = "mjpeg"

	shareFPS      = 30
	shareMJPEGFPS = 15
	shareMaxW     = 1920
	mjpegMaxW     = 1280

	shareGrace    = 1200 * time.Millisecond
	shareStopWait = 5 * time.Second
	shareProbe    = 4 * time.Second
)

// ShareSession is one live ffmpeg producing either an HLS directory or a
// stream of JPEG frames.
type ShareSession struct {
	kind     string
	dir      string
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stderr   *tailBuffer
	audio    []audioSource
	done     chan struct{}
	waitErr  error
	stopWait time.Duration

	frame atomic.Value // frameSlot
	gen   atomic.Uint64
}

type frameSlot struct {
	gen  uint64
	jpeg []byte
}

// PrepareShare checks that ffmpeg exists and warms the hardware-encoder
// probe. Call it before hiding the capture overlay: a missing encoder can
// take a few seconds, and the pill should stay up while that happens.
func PrepareShare() error {
	if _, err := LocateFFmpeg(); err != nil {
		return fmt.Errorf("screen sharing needs ffmpeg: %w", err)
	}
	_, _ = SelectShareEncoder(640, 360)
	return nil
}

// SelectShareEncoder returns the first usable hardware H.264 encoder.
// ok is false when this PC has none — the caller falls back to MJPEG.
func SelectShareEncoder(w, h int) (VideoEncoder, bool) {
	for _, name := range hwH264Encoders {
		if encoderUsableWithin(name, shareProbe) {
			return shareH264(name, w, h), true
		}
	}
	return VideoEncoder{}, false
}

func shareH264(name string, w, h int) VideoEncoder {
	br := shareBitrate(w, h)
	args := []string{"-b:v", br, "-maxrate", br, "-bufsize", br, "-bf", "0", "-g", strconv.Itoa(shareFPS)}
	switch name {
	case "h264_nvenc":
		args = append([]string{"-preset", "p1"}, args...)
	case "h264_qsv":
		args = append([]string{"-preset", "veryfast"}, args...)
	case "h264_amf":
		args = append([]string{"-quality", "speed"}, args...)
	}
	return VideoEncoder{Name: name, Ext: ".ts", Args: args}
}

// shareBitrate targets ~0.05 bits per pixel at 30fps, clamped so a 1080p
// share stays in the range a typical Wi-Fi link can carry (2.5–12 Mbps).
func shareBitrate(w, h int) string {
	bps := int64(w) * int64(h) * shareFPS / 20
	const minBps, maxBps = 2_500_000, 12_000_000
	if bps < minBps {
		bps = minBps
	}
	if bps > maxBps {
		bps = maxBps
	}
	return fmt.Sprintf("%dk", bps/1000)
}

func evenDown(n int) int {
	if n < 2 {
		return 2
	}
	if n%2 != 0 {
		return n - 1
	}
	return n
}

// shareScale downscales grabs wider than shareMaxW. Empty means the grab
// size is already fine. Height -2 keeps yuv420 even.
func shareScale(w int) string {
	if w <= shareMaxW {
		return ""
	}
	return "scale=1920:-2"
}

// StartShare spawns ffmpeg for req. dir receives the HLS playlist when the
// hardware encoder is usable; otherwise the session emits JPEG frames and
// dir is left unused. audio follows the same opt-in as recording. A bad
// audio device is retried once without audio before falling back to MJPEG,
// so a microphone name ffmpeg rejects cannot take the whole share down.
func StartShare(req CaptureRequest, audio AudioConfig, dir string) (*ShareSession, error) {
	if req.Rect.W < 2 || req.Rect.H < 2 {
		return nil, fmt.Errorf("share: invalid rect %dx%d", req.Rect.W, req.Rect.H)
	}
	req.Rect.W = evenDown(req.Rect.W)
	req.Rect.H = evenDown(req.Rect.H)

	bin, err := LocateFFmpeg()
	if err != nil {
		return nil, fmt.Errorf("share: %w", err)
	}
	screens := enumScreens()

	var hlsErr error
	enc, hlsOK := SelectShareEncoder(req.Rect.W, req.Rect.H)
	if hlsOK {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("share dir: %w", err)
		}
		playlist := filepath.Join(dir, "index.m3u8")
		seg := filepath.ToSlash(filepath.Join(dir, "seg_%05d.ts"))
		sources := openAudioSources(audio)
		attempts := shareHLSCandidates(req, screens, enc, playlist, seg)
		if len(sources) > 0 || audio.MicDevice != "" {
			inputs := audioInputs(sources)
			for i := range attempts {
				attempts[i] = injectAudioMixCodec(attempts[i], inputs, audio.MicDevice, "aac", "160k")
			}
		}
		sess, attemptErr := runShareAttempts(bin, attempts, ShareHLS, dir, sources, shareGrace, shareStopWait)
		if sess != nil {
			return sess, nil
		}
		stopAudioSources(sources)
		// Audio is the usual reason a healthy grab still dies at startup
		// (bad mic name, loopback pipe). Retry the same video with no audio
		// before giving up on H.264.
		if len(sources) > 0 || audio.MicDevice != "" {
			silent := shareHLSCandidates(req, screens, enc, playlist, seg)
			sess, silentErr := runShareAttempts(bin, silent, ShareHLS, dir, nil, shareGrace, shareStopWait)
			if sess != nil {
				return sess, nil
			}
			attemptErr = errors.Join(attemptErr, silentErr)
		}
		// Hardware H.264 was probed usable but the grab still failed (no
		// Desktop Duplication, encoder rejected the tuning). MJPEG below is
		// the transport that does not need it.
		hlsErr = attemptErr
	}

	attempts := shareMJPEGCandidates(req, screens)
	sess, attemptErr := runShareAttempts(bin, attempts, ShareMJPEG, "", nil, shareGrace, shareStopWait)
	if sess != nil {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		return sess, nil
	}
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
	if !hlsOK {
		return nil, fmt.Errorf("share: no hardware H.264 encoder, and the picture stream failed: %w", attemptErr)
	}
	return nil, fmt.Errorf("share: all capture backends failed: %w", errors.Join(hlsErr, attemptErr))
}

func audioInputs(sources []audioSource) []AudioInput {
	out := make([]AudioInput, len(sources))
	for i, s := range sources {
		out[i] = s.Input()
	}
	return out
}

func shareHLSCandidates(req CaptureRequest, screens []ScreenInfo, enc VideoEncoder, playlist, seg string) [][]string {
	var out [][]string
	if screen, err := findScreen(screens, req.MonitorID); err == nil {
		if ddaIdx, ok := DDAOutputIndex(screen); ok {
			if dda, err := BuildShareArgsDDA(req, screens, ddaIdx, enc, playlist, seg); err == nil {
				out = append(out, dda)
			}
		}
	}
	out = append(out, BuildShareArgsGDI(req, enc, playlist, seg))
	return out
}

func shareMJPEGCandidates(req CaptureRequest, screens []ScreenInfo) [][]string {
	var out [][]string
	if screen, err := findScreen(screens, req.MonitorID); err == nil {
		if ddaIdx, ok := DDAOutputIndex(screen); ok {
			if dda, err := BuildShareMJPEGArgsDDA(req, screens, ddaIdx); err == nil {
				out = append(out, dda)
			}
		}
	}
	out = append(out, BuildShareMJPEGArgsGDI(req))
	return out
}

// BuildShareArgsDDA builds the GPU grab for an HLS playlist. Offsets are
// monitor-relative, same rebase as BuildVideoArgsDDA. ddaIdx is the DXGI
// output index, not MonitorID.
func BuildShareArgsDDA(req CaptureRequest, screens []ScreenInfo, ddaIdx int, enc VideoEncoder, playlist, segPattern string) ([]string, error) {
	screen, err := findScreen(screens, req.MonitorID)
	if err != nil {
		return nil, err
	}
	relX := req.Rect.X - screen.X
	relY := req.Rect.Y - screen.Y
	graph := fmt.Sprintf(
		"ddagrab=output_idx=%d:framerate=%d:video_size=%dx%d:offset_x=%d:offset_y=%d:draw_mouse=%s,hwdownload,format=bgra",
		ddaIdx, shareFPS, req.Rect.W, req.Rect.H, relX, relY, boolToInt(req.IncludeCursor),
	)
	if f := shareScale(req.Rect.W); f != "" {
		graph += "," + f
	}
	args := []string{"-y", "-filter_complex", graph, "-c:v", enc.Name}
	args = append(args, enc.Args...)
	args = append(args, "-pix_fmt", "yuv420p", "-force_key_frames", "expr:gte(t,n_forced*1)")
	args = append(args, hlsMux(playlist, segPattern)...)
	return args, nil
}

// BuildShareArgsGDI builds the software grab for an HLS playlist. gdigrab
// offsets are virtual-desktop coordinates, used as-is.
func BuildShareArgsGDI(req CaptureRequest, enc VideoEncoder, playlist, segPattern string) []string {
	args := []string{
		"-y",
		"-f", "gdigrab",
		"-framerate", strconv.Itoa(shareFPS),
		"-offset_x", strconv.Itoa(req.Rect.X),
		"-offset_y", strconv.Itoa(req.Rect.Y),
		"-video_size", fmt.Sprintf("%dx%d", req.Rect.W, req.Rect.H),
		"-draw_mouse", boolToInt(req.IncludeCursor),
		"-i", "desktop",
	}
	if f := shareScale(req.Rect.W); f != "" {
		args = append(args, "-vf", f)
	}
	args = append(args, "-c:v", enc.Name)
	args = append(args, enc.Args...)
	args = append(args, "-pix_fmt", "yuv420p", "-force_key_frames", "expr:gte(t,n_forced*1)")
	args = append(args, hlsMux(playlist, segPattern)...)
	return args
}

func hlsMux(playlist, segPattern string) []string {
	return []string{
		"-f", "hls",
		"-hls_time", "1",
		"-hls_list_size", "4",
		"-hls_flags", "delete_segments+append_list+omit_endlist+independent_segments",
		"-hls_allow_cache", "0",
		"-hls_segment_filename", segPattern,
		playlist,
	}
}

// BuildShareMJPEGArgsDDA builds the GPU grab that writes a JPEG frame pipe
// (image2pipe). The HTTP server splits that pipe into multipart frames.
func BuildShareMJPEGArgsDDA(req CaptureRequest, screens []ScreenInfo, ddaIdx int) ([]string, error) {
	screen, err := findScreen(screens, req.MonitorID)
	if err != nil {
		return nil, err
	}
	relX := req.Rect.X - screen.X
	relY := req.Rect.Y - screen.Y
	graph := fmt.Sprintf(
		"ddagrab=output_idx=%d:framerate=%d:video_size=%dx%d:offset_x=%d:offset_y=%d:draw_mouse=%s,hwdownload,format=bgra,scale='min(%d,iw)':-2",
		ddaIdx, shareMJPEGFPS, req.Rect.W, req.Rect.H, relX, relY, boolToInt(req.IncludeCursor), mjpegMaxW,
	)
	return []string{
		"-y", "-filter_complex", graph,
		"-q:v", "8", "-r", strconv.Itoa(shareMJPEGFPS),
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	}, nil
}

// BuildShareMJPEGArgsGDI is the software-grab JPEG pipe.
func BuildShareMJPEGArgsGDI(req CaptureRequest) []string {
	return []string{
		"-y",
		"-f", "gdigrab",
		"-framerate", strconv.Itoa(shareMJPEGFPS),
		"-offset_x", strconv.Itoa(req.Rect.X),
		"-offset_y", strconv.Itoa(req.Rect.Y),
		"-video_size", fmt.Sprintf("%dx%d", req.Rect.W, req.Rect.H),
		"-draw_mouse", boolToInt(req.IncludeCursor),
		"-i", "desktop",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':-2", mjpegMaxW),
		"-q:v", "8", "-r", strconv.Itoa(shareMJPEGFPS),
		"-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	}
}

func runShareAttempts(bin string, attempts [][]string, kind, dir string, audio []audioSource, grace, stopWait time.Duration) (*ShareSession, error) {
	var errs []error
	for _, args := range attempts {
		sess, err := spawnShare(bin, args, kind, dir, audio, grace, stopWait)
		if err == nil {
			return sess, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func spawnShare(bin string, args []string, kind, dir string, audio []audioSource, grace, stopWait time.Duration) (*ShareSession, error) {
	full := append([]string{"-hide_banner", "-loglevel", "warning"}, args...)
	cmd := exec.Command(bin, full...)
	configureSysProcAttr(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("share ffmpeg stdin: %w", err)
	}
	tail := &tailBuffer{max: stderrTailSize}
	cmd.Stderr = tail

	var stdout io.ReadCloser
	if kind == ShareMJPEG {
		stdout, err = cmd.StdoutPipe()
		if err != nil {
			_ = stdin.Close()
			return nil, fmt.Errorf("share ffmpeg stdout: %w", err)
		}
	} else {
		cmd.Stdout = io.Discard
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("share ffmpeg: %w", err)
	}
	tieToProcessLifetime(cmd)

	sess := &ShareSession{
		kind: kind, dir: dir, cmd: cmd, stdin: stdin, stderr: tail,
		audio: audio, done: make(chan struct{}), stopWait: stopWait,
	}
	if stdout != nil {
		go func() {
			_ = readMJPEG(stdout, func(frame []byte) {
				cp := make([]byte, len(frame))
				copy(cp, frame)
				n := sess.gen.Add(1)
				sess.frame.Store(frameSlot{gen: n, jpeg: cp})
			})
			_ = stdout.Close()
		}()
	}
	go func() {
		sess.waitErr = cmd.Wait()
		close(sess.done)
	}()

	select {
	case <-sess.done:
		// Audio pipes stay open: the caller retries the next grab backend
		// (ddagrab then gdigrab) against the same sources, and stops them
		// itself once every attempt has failed.
		return nil, fmt.Errorf("ffmpeg exited during share startup: %v\n%s", sess.waitErr, tail)
	case <-time.After(grace):
		return sess, nil
	}
}

// Kind is "hls" or "mjpeg".
func (s *ShareSession) Kind() string {
	if s == nil {
		return ""
	}
	return s.kind
}

// Dir is the HLS output directory. Empty for an MJPEG session.
func (s *ShareSession) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// Frame returns the latest JPEG and a generation that increments per frame.
// gen 0 means no frame yet.
func (s *ShareSession) Frame() (uint64, []byte) {
	if s == nil {
		return 0, nil
	}
	v := s.frame.Load()
	if v == nil {
		return 0, nil
	}
	sl := v.(frameSlot)
	return sl.gen, sl.jpeg
}

// Stop writes 'q' so ffmpeg exits, then removes the HLS directory. Safe to
// call once; a second call returns an error only if the session is nil.
func (s *ShareSession) Stop() error {
	if s == nil || s.cmd == nil {
		return nil
	}
	select {
	case <-s.done:
	default:
		_, _ = io.WriteString(s.stdin, "q")
		_ = s.stdin.Close()
		select {
		case <-s.done:
		case <-time.After(s.stopWait):
			_ = s.cmd.Process.Kill()
			<-s.done
		}
	}
	stopAudioSources(s.audio)
	s.audio = nil
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
		s.dir = ""
	}
	s.cmd = nil
	return nil
}

// readMJPEG splits an image2pipe MJPEG stream on SOI/EOI markers. Entropy
// data escapes 0xFF as 0xFF 0x00, so an EOI marker does not occur inside a
// scan. Frames larger than 8MB are dropped (a lost marker would otherwise
// grow the buffer without bound).
func readMJPEG(r io.Reader, emit func([]byte)) error {
	buf := make([]byte, 0, 256*1024)
	tmp := make([]byte, 32*1024)
	soi := []byte{0xFF, 0xD8}
	eoi := []byte{0xFF, 0xD9}
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			for {
				if len(buf) > 8<<20 {
					buf = buf[:0]
					break
				}
				start := bytes.Index(buf, soi)
				if start < 0 {
					if len(buf) > 1 {
						buf = buf[len(buf)-1:]
					} else {
						buf = buf[:0]
					}
					break
				}
				if start > 0 {
					buf = buf[start:]
				}
				rel := bytes.Index(buf[2:], eoi)
				if rel < 0 {
					break
				}
				end := 2 + rel + 2
				emit(buf[:end])
				rest := make([]byte, len(buf)-end)
				copy(rest, buf[end:])
				buf = rest
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}
