package capture

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// replay.go is the INSTANT REPLAY buffer: one long-lived ffmpeg keeps the last
// N seconds of a monitor as short segment files on disk, so "save the last
// minute" is a stream-copy stitch of segments that already exist — no
// re-encode, no RAM ring, no work at save time beyond a file copy.
//
// Gamer-first decisions that shape this file:
//   - ddagrab hands D3D11 textures STRAIGHT to a hardware encoder (NVENC/AMF
//     accept D3D11 frames): at native resolution a frame never touches system
//     memory (measured ~0.3% total CPU at 3440x1440@60 on a 9800X3D + RTX 5080).
//     Downscaling has no working GPU path in the FFmpeg builds we target
//     (scale_d3d11 fails to configure its output behind ddagrab; hwmap to CUDA
//     fails too), so a non-native height falls back to hwdownload + swscale,
//     which costs roughly one CPU core. The UI says so.
//   - HARDWARE encoders only. If none probes usable the buffer refuses to start
//     instead of silently burning CPU on a software encoder mid-game.
//   - H.264/HEVC segments are MPEG-TS, which can be byte-concatenated with no
//     timestamp seams. AV1 can't ride in MPEG-TS, so AV1 segments are Matroska
//     joined with the concat demuxer (a few ms of seam per segment).
//   - A supervisor restarts ffmpeg when it dies (display mode change, UAC
//     secure desktop, Discord restarting) and keeps the earlier segments; each
//     ffmpeg run gets its own file prefix so runs never collide.

const (
	// replaySegmentSec is the segment (and keyframe) length. It bounds both how
	// far a saved clip may overshoot the requested length and how long Save waits
	// for the in-progress segment to close.
	replaySegmentSec = 2

	// replayAudioKbps is the default AAC bitrate per audio track.
	replayAudioKbps = 160
)

// Replay codec ids (ReplayConfig.Codec).
const (
	ReplayCodecH264 = "h264"
	ReplayCodecHEVC = "hevc"
	ReplayCodecAV1  = "av1"
)

// replayEncoders lists the hardware encoders tried, in order, per codec.
var replayEncoders = map[string][]string{
	ReplayCodecH264: {"h264_nvenc", "h264_amf", "h264_qsv"},
	ReplayCodecHEVC: {"hevc_nvenc", "hevc_amf", "hevc_qsv"},
	ReplayCodecAV1:  {"av1_nvenc", "av1_amf", "av1_qsv"},
}

// ReplayAudio selects the replay's audio sources and how they're mixed.
// Desktop is the system mix with Discord split out (process-loopback EXCLUDE
// mode on Discord's tree) so Discord can be kept, dropped or levelled on its
// own. The zero value records no audio.
type ReplayAudio struct {
	Desktop   bool   `json:"desktop"`
	Discord   bool   `json:"discord"`
	MicDevice string `json:"micDevice"` // dshow device name; "" = no mic

	// Per-source volume in percent (100 = unchanged); <= 0 means 100.
	DesktopVolume int `json:"desktopVolume"`
	DiscordVolume int `json:"discordVolume"`
	MicVolume     int `json:"micVolume"`

	// SeparateTracks also writes each source as its own audio track after the
	// mixed one, so an editor can mute or level them later.
	SeparateTracks bool `json:"separateTracks"`
	BitrateKbps    int  `json:"bitrateKbps"` // AAC per track; <= 0 = replayAudioKbps
}

// Audio track titles, in track order after the mix.
const (
	replayTrackMix     = "All audio"
	replayTrackDesktop = "Game & desktop"
	replayTrackDiscord = "Discord"
	replayTrackMic     = "Microphone"
)

// replayAudioSrc is one source in the replay's audio graph: a loopback pipe,
// the mic (dshow), or silence standing in for a source that isn't available
// right now (Discord not running). The placeholder keeps the track layout
// identical across runs so segments from before and after still concatenate.
type replayAudioSrc struct {
	title  string
	volume int
	in     *AudioInput // loopback pipe; nil with mic == "" = silence
	mic    string      // dshow device name
}

// replayAudioSources lists the configured sources in track order. It is the
// single source of truth for the track layout (see replayTrackTitles).
func replayAudioSources(a ReplayAudio) []replayAudioSrc {
	var out []replayAudioSrc
	if a.Desktop {
		out = append(out, replayAudioSrc{title: replayTrackDesktop, volume: a.DesktopVolume})
	}
	if a.Discord {
		out = append(out, replayAudioSrc{title: replayTrackDiscord, volume: a.DiscordVolume})
	}
	if a.MicDevice != "" {
		out = append(out, replayAudioSrc{title: replayTrackMic, volume: a.MicVolume, mic: a.MicDevice})
	}
	return out
}

// replayTrackTitles returns the saved clip's audio track titles in order.
func replayTrackTitles(a ReplayAudio) []string {
	srcs := replayAudioSources(a)
	switch {
	case len(srcs) == 0:
		return nil
	case len(srcs) == 1:
		return []string{srcs[0].title}
	case !a.SeparateTracks:
		return []string{replayTrackMix}
	}
	titles := []string{replayTrackMix}
	for _, src := range srcs {
		titles = append(titles, src.title)
	}
	return titles
}

// ReplayConfig is everything the buffer needs to run.
type ReplayConfig struct {
	MonitorID     int    // ScreenInfo.ID; <0 or unknown = primary monitor
	FPS           int    // capture + encode framerate
	Height        int    // output height in px; 0 (or >= native) = native, zero-copy
	BitrateKbps   int    // constant video bitrate
	Codec         string // ReplayCodecH264 | ReplayCodecHEVC | ReplayCodecAV1
	IncludeCursor bool
	KeepSeconds   int    // window kept on disk == the longest clip Save can produce; 0 = unlimited (Persist only)
	Dir           string // segment directory; wiped on Start and Stop unless Persist
	Audio         ReplayAudio

	// Persist keeps segments across Start/Stop (session recording): runs are
	// numbered by start time, each gets a sidecar (r<run>.json: start time,
	// tag) and a segment timing list (r<run>.csv), and pruning is by age
	// (KeepSeconds) and total size (MaxBytes) instead of a fixed count.
	// Runs the protect callback (SetProtect) claims are never pruned.
	Persist  bool
	MaxBytes int64  // Persist: total segment bytes kept; 0 = unlimited
	Tag      string // Persist: free-form label stored in each run's sidecar
}

// ReplayStatus is the buffer's live state for the UI.
type ReplayStatus struct {
	Running     bool   `json:"running"`     // supervisor active (may be between restarts)
	Recording   bool   `json:"recording"`   // an ffmpeg is currently capturing
	Encoder     string `json:"encoder"`     // e.g. "h264_nvenc"
	ZeroCopy    bool   `json:"zeroCopy"`    // frames stay on the GPU end to end
	BufferedSec int    `json:"bufferedSec"` // seconds currently available to save
	Error       string `json:"error"`       // last failure, "" when healthy
	DiskBytes   int64  `json:"diskBytes"`   // Persist: bytes of segments on disk
}

// ReplayBuffer owns the replay ffmpeg and its segment directory. Zero value is
// not usable; build with NewReplayBuffer.
type ReplayBuffer struct {
	mu       sync.Mutex
	cfg      ReplayConfig
	stop     chan struct{} // nil when not running
	done     chan struct{}
	status   ReplayStatus
	run      int
	onStatus func(ReplayStatus)
	protect  func(run int) bool // Persist: runs that must never be pruned
	diskUsed int64              // Persist: last measured segment bytes

	// segMu is held while pruning and while Save reads segments, so the pruner
	// never deletes a file a stitch is about to read.
	segMu sync.Mutex
}

// NewReplayBuffer returns an idle buffer.
func NewReplayBuffer() *ReplayBuffer { return &ReplayBuffer{} }

// SetOnStatus registers a callback fired (off the caller's goroutine is not
// guaranteed) whenever the status changes.
func (b *ReplayBuffer) SetOnStatus(fn func(ReplayStatus)) {
	b.mu.Lock()
	b.onStatus = fn
	b.mu.Unlock()
}

// SetProtect registers which runs pruning must keep (manual recordings,
// sessions the user chose to keep). Persist mode only.
func (b *ReplayBuffer) SetProtect(fn func(run int) bool) {
	b.mu.Lock()
	b.protect = fn
	b.mu.Unlock()
}

// Status returns the current status with BufferedSec computed from disk.
func (b *ReplayBuffer) Status() ReplayStatus {
	b.mu.Lock()
	st := b.status
	cfg := b.cfg
	b.mu.Unlock()
	if st.Running && cfg.Dir != "" {
		n := len(listReplaySegments(cfg.Dir)) - 1 // newest is still being written
		st.BufferedSec = max(n, 0) * replaySegmentSec
		if cfg.KeepSeconds > 0 {
			st.BufferedSec = min(st.BufferedSec, cfg.KeepSeconds)
		}
	}
	if cfg.Persist {
		b.mu.Lock()
		st.DiskBytes = b.diskUsed
		b.mu.Unlock()
	}
	return st
}

func (b *ReplayBuffer) setStatus(update func(*ReplayStatus)) {
	b.mu.Lock()
	update(&b.status)
	st := b.status
	cb := b.onStatus
	b.mu.Unlock()
	if cb != nil {
		cb(st)
	}
}

// Start (re)starts the buffer with cfg. It returns an error only for problems
// a retry can't fix (no ffmpeg, no hardware encoder, unmappable monitor);
// capture failures after that are retried by the supervisor and surface in
// Status().Error.
func (b *ReplayBuffer) Start(cfg ReplayConfig) error {
	b.Stop()

	if cfg.FPS <= 0 || cfg.BitrateKbps <= 0 || (cfg.KeepSeconds <= 0 && !cfg.Persist) || cfg.Dir == "" {
		return fmt.Errorf("replay: invalid config (fps=%d kbps=%d keep=%d dir=%q)", cfg.FPS, cfg.BitrateKbps, cfg.KeepSeconds, cfg.Dir)
	}
	bin, err := LocateFFmpeg()
	if err != nil {
		return err
	}
	enc, err := replayEncoderFor(cfg.Codec)
	if err != nil {
		b.setStatus(func(s *ReplayStatus) { *s = ReplayStatus{Error: err.Error()} })
		return err
	}
	screen, ddaIdx, err := replayScreen(enumScreens(), cfg.MonitorID)
	if err != nil {
		b.setStatus(func(s *ReplayStatus) { *s = ReplayStatus{Error: err.Error()} })
		return err
	}
	if cfg.Height >= screen.H {
		cfg.Height = 0 // never upscale; native is also the zero-copy path
	}
	if cfg.Persist {
		if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
			return fmt.Errorf("replay: recordings dir: %w", err)
		}
	} else if err := resetReplayDir(cfg.Dir); err != nil {
		return fmt.Errorf("replay: segment dir: %w", err)
	}

	b.mu.Lock()
	b.cfg = cfg
	b.stop = make(chan struct{})
	b.done = make(chan struct{})
	stop, done := b.stop, b.done
	b.mu.Unlock()
	b.setStatus(func(s *ReplayStatus) { *s = ReplayStatus{Running: true, Encoder: enc} })

	go b.supervise(bin, enc, ddaIdx, cfg, stop, done)
	return nil
}

// Stop ends the buffer and deletes its segments. Idempotent.
func (b *ReplayBuffer) Stop() {
	b.mu.Lock()
	stop, done, dir, persist := b.stop, b.done, b.cfg.Dir, b.cfg.Persist
	b.stop, b.done = nil, nil
	b.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
	if !persist {
		b.segMu.Lock()
		_ = resetReplayDir(dir)
		b.segMu.Unlock()
	}
	b.setStatus(func(s *ReplayStatus) { *s = ReplayStatus{} })
}

// Running reports whether the buffer is active.
func (b *ReplayBuffer) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stop != nil
}

// supervise runs ffmpeg until stop closes, restarting it when it dies or when
// Discord's process changes (the audio graph targets a specific PID).
func (b *ReplayBuffer) supervise(bin, enc string, ddaIdx int, cfg ReplayConfig, stop, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	watchDiscord := cfg.Audio.Desktop || cfg.Audio.Discord
	for {
		b.mu.Lock()
		b.run++
		if cfg.Persist {
			// Start-time run ids stay unique and ordered across restarts.
			b.run = max(b.run, int(time.Now().Unix()))
		}
		run := b.run
		b.mu.Unlock()

		discordPID := uint32(0)
		if watchDiscord {
			discordPID = DiscordPID()
		}
		sources, tracks := openReplayAudio(cfg.Audio, discordPID)

		rec, zeroCopy, err := b.spawnReplay(bin, enc, ddaIdx, cfg, run, tracks)
		if err != nil {
			stopAudioSources(sources)
			b.setStatus(func(s *ReplayStatus) { s.Recording = false; s.Error = err.Error() })
			select {
			case <-stop:
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 10*time.Second)
			continue
		}
		backoff = time.Second
		b.setStatus(func(s *ReplayStatus) { s.Recording = true; s.ZeroCopy = zeroCopy; s.Error = "" })

		tick := time.NewTicker(replaySegmentSec * time.Second)
		ticks := 0
		stopping := false
	watch:
		for {
			select {
			case <-stop:
				stopping = true
				break watch
			case <-rec.done:
				tail := rec.stderr.String()
				b.setStatus(func(s *ReplayStatus) { s.Recording = false; s.Error = "capture stopped: " + lastLine(tail) })
				break watch
			case <-tick.C:
				ticks++
				if !cfg.Persist || ticks%5 == 1 { // persistent dirs are big: prune every 10s
					b.prune(cfg)
				}
				// Re-target the audio graph when Discord starts, quits or restarts.
				if watchDiscord && ticks%5 == 0 && DiscordPID() != discordPID {
					break watch
				}
			}
		}
		tick.Stop()
		select {
		case <-rec.done:
		default:
			_ = rec.cmd.Process.Kill()
			<-rec.done
		}
		stopAudioSources(sources)
		if stopping {
			return
		}
		select {
		case <-stop:
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// spawnReplay starts one ffmpeg run, trying the zero-copy graph first (when
// eligible) and the hwdownload graph second. ddagrab occasionally fails its
// first duplication right after another one closed, so each graph gets a
// second attempt.
func (b *ReplayBuffer) spawnReplay(bin, enc string, ddaIdx int, cfg ReplayConfig, run int, audio []replayAudioSrc) (*recording, bool, error) {
	pattern := filepath.Join(cfg.Dir, fmt.Sprintf("r%04d-%%08d%s", run, replaySegmentExt(cfg.Codec)))
	var errs []error
	for _, zeroCopy := range replayGraphCandidates(cfg, enc) {
		args := BuildReplayArgs(ddaIdx, cfg, enc, zeroCopy, pattern, audio)
		if cfg.Persist {
			// Per-segment start/end times, so the timeline knows exactly where
			// each segment sits. The output pattern must stay the last arg.
			list := filepath.Join(cfg.Dir, fmt.Sprintf("r%04d.csv", run))
			args = append(args[:len(args)-1:len(args)-1], "-segment_list", list, "-segment_list_type", "csv", pattern)
			writeRunInfo(cfg.Dir, RunInfo{Run: run, StartMs: time.Now().UnixMilli(), Tag: cfg.Tag})
		}
		for attempt := 0; attempt < 2; attempt++ {
			rec, err := spawnReplayFFmpeg(bin, args)
			if err == nil {
				return rec, zeroCopy, nil
			}
			errs = append(errs, err)
		}
	}
	return nil, false, fmt.Errorf("replay: capture failed to start: %w", errors.Join(errs...))
}

// spawnReplayFFmpeg starts ffmpeg and waits out the startup grace window.
func spawnReplayFFmpeg(bin string, args []string) (*recording, error) {
	full := append([]string{"-hide_banner", "-loglevel", "warning", "-nostdin"}, args...)
	cmd := exec.Command(bin, full...)
	configureSysProcAttr(cmd)
	tail := &tailBuffer{max: stderrTailSize}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn ffmpeg: %w", err)
	}
	tieToProcessLifetime(cmd)
	rec := &recording{cmd: cmd, stderr: tail, done: make(chan struct{})}
	go func() {
		rec.waitErr = cmd.Wait()
		close(rec.done)
	}()
	select {
	case <-rec.done:
		return nil, fmt.Errorf("ffmpeg exited during startup: %v: %s", rec.waitErr, lastLine(tail.String()))
	case <-time.After(startGrace):
		return rec, nil
	}
}

// replayGraphCandidates returns the video graphs to try: zero-copy first when
// the output is native and the encoder takes D3D11 frames, then the
// hwdownload (CPU) graph.
func replayGraphCandidates(cfg ReplayConfig, enc string) []bool {
	if cfg.Height == 0 && (strings.HasSuffix(enc, "_nvenc") || strings.HasSuffix(enc, "_amf")) {
		return []bool{true, false}
	}
	return []bool{false}
}

// BuildReplayArgs builds the segmenting ffmpeg args: the ddagrab video graph
// plus the audio graph for audio (see replayAudioArgs). The output pattern is
// the last arg. Kept pure for golden tests.
func BuildReplayArgs(ddaIdx int, cfg ReplayConfig, enc string, zeroCopy bool, pattern string, audio []replayAudioSrc) []string {
	graph := fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:draw_mouse=%s",
		ddaIdx, cfg.FPS, boolToInt(cfg.IncludeCursor))
	if !zeroCopy {
		graph += ",hwdownload,format=bgra"
		if cfg.Height > 0 {
			graph += fmt.Sprintf(",scale=-2:%d:flags=bilinear", cfg.Height)
		}
		if strings.HasSuffix(enc, "_qsv") {
			graph += ",format=nv12" // QSV won't take BGRA system frames
		}
	}
	audioIn, audioGraph, audioMaps := replayAudioArgs(audio, cfg.Audio)
	args := append([]string{"-y"}, audioIn...)
	if len(audio) > 0 {
		args = append(args, "-filter_complex", graph+"[v];"+audioGraph, "-map", "[v]")
		args = append(args, audioMaps...)
	} else {
		args = append(args, "-filter_complex", graph)
	}
	args = append(args, replayEncoderArgs(enc, cfg.BitrateKbps, cfg.FPS)...)
	if len(audio) > 0 {
		kbps := cfg.Audio.BitrateKbps
		if kbps <= 0 {
			kbps = replayAudioKbps
		}
		args = append(args, "-c:a", "aac", "-b:a", fmt.Sprintf("%dk", kbps))
	}
	args = append(args, "-f", "segment", "-segment_time", strconv.Itoa(replaySegmentSec))
	if cfg.Codec == ReplayCodecAV1 {
		args = append(args, "-segment_format", "matroska", "-reset_timestamps", "1")
	} else {
		args = append(args, "-segment_format", "mpegts")
	}
	return append(args, pattern)
}

// replayAudioArgs builds the audio inputs, the filter graph (joined to the
// video graph with ";") and the -map args. Every source is levelled and
// normalized to 48 kHz stereo; several sources are summed into one mixed track
// (normalize=0 keeps each at its own level), and with SeparateTracks each is
// also mapped as its own track after the mix. Silence placeholders are rate
// limited (-re) so they don't race ahead of the live sources.
func replayAudioArgs(srcs []replayAudioSrc, a ReplayAudio) (inputs []string, graph string, maps []string) {
	if len(srcs) == 0 {
		return nil, "", nil
	}
	for _, src := range srcs {
		switch {
		case src.in != nil:
			inputs = append(inputs,
				"-f", src.in.SampleFmt,
				"-ar", strconv.Itoa(src.in.SampleRate),
				"-ac", strconv.Itoa(src.in.Channels),
				"-i", src.in.PipePath)
		case src.mic != "":
			inputs = append(inputs, "-f", "dshow", "-i", "audio="+src.mic)
		default:
			inputs = append(inputs, "-re", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo")
		}
	}

	separate := a.SeparateTracks && len(srcs) > 1
	chains := make([]string, 0, len(srcs)+1)
	var mixIn strings.Builder
	for i, src := range srcs {
		chain := fmt.Sprintf("[%d:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo", i)
		if v := src.volume; v > 0 && v != 100 {
			chain += fmt.Sprintf(",volume=%.2f", float64(v)/100)
		}
		switch {
		case len(srcs) == 1:
			chain += "[aout]"
		case separate:
			chain += fmt.Sprintf(",asplit=2[m%d][s%d]", i, i)
		default:
			chain += fmt.Sprintf("[m%d]", i)
		}
		chains = append(chains, chain)
		fmt.Fprintf(&mixIn, "[m%d]", i)
	}
	if len(srcs) > 1 {
		chains = append(chains, fmt.Sprintf("%samix=inputs=%d:duration=longest:normalize=0[aout]", mixIn.String(), len(srcs)))
	}
	maps = []string{"-map", "[aout]"}
	if separate {
		for i := range srcs {
			maps = append(maps, "-map", fmt.Sprintf("[s%d]", i))
		}
	}
	return inputs, strings.Join(chains, ";"), maps
}

// replayEncoderArgs returns constant-bitrate, segment-aligned-keyframe args
// for a hardware encoder. A keyframe every segment boundary makes every
// segment independently decodable, which is what lets Save stream-copy.
func replayEncoderArgs(enc string, kbps, fps int) []string {
	rate := fmt.Sprintf("%dk", kbps)
	args := []string{"-c:v", enc}
	switch {
	case strings.HasSuffix(enc, "_nvenc"):
		args = append(args, "-preset", "p4", "-tune", "hq", "-rc", "cbr")
	case strings.HasSuffix(enc, "_amf"):
		args = append(args, "-quality", "speed", "-rc", "cbr")
	case strings.HasSuffix(enc, "_qsv"):
		args = append(args, "-preset", "veryfast")
	}
	return append(args,
		"-b:v", rate, "-maxrate", rate, "-bufsize", fmt.Sprintf("%dk", kbps*2),
		"-g", strconv.Itoa(fps*replaySegmentSec),
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", replaySegmentSec),
	)
}

// replayEncoderFor returns the first usable hardware encoder for codec.
func replayEncoderFor(codec string) (string, error) {
	names, ok := replayEncoders[codec]
	if !ok {
		return "", fmt.Errorf("replay: unknown codec %q", codec)
	}
	for _, n := range names {
		if EncoderUsable(n) {
			return n, nil
		}
	}
	return "", fmt.Errorf("replay: no hardware %s encoder on this PC (NVIDIA, AMD or Intel GPU required)", strings.ToUpper(codec))
}

// ReplayEncoderAvailable reports whether codec has a usable hardware encoder.
// Probes are cached, so the UI can call this freely after the first time.
func ReplayEncoderAvailable(codec string) bool {
	_, err := replayEncoderFor(codec)
	return err == nil
}

// replayScreen resolves the monitor to capture and its ddagrab output index.
func replayScreen(screens []ScreenInfo, id int) (ScreenInfo, int, error) {
	var pick *ScreenInfo
	for i := range screens {
		if screens[i].ID == id {
			pick = &screens[i]
			break
		}
	}
	if pick == nil {
		for i := range screens {
			if screens[i].IsPrimary {
				pick = &screens[i]
				break
			}
		}
	}
	if pick == nil && len(screens) > 0 {
		pick = &screens[0]
	}
	if pick == nil {
		return ScreenInfo{}, 0, fmt.Errorf("replay: no monitors found")
	}
	idx, ok := DDAOutputIndex(*pick)
	if !ok {
		return ScreenInfo{}, 0, fmt.Errorf("replay: can't map monitor %d to a GPU output", pick.ID)
	}
	return *pick, idx, nil
}

// openReplayAudio starts the loopback sources for a, given Discord's root PID
// (0 = not running), and returns them with the graph's sources in track order.
// Desktop excludes Discord whenever it's running, so Discord is its own source
// with its own volume and track. A source that is unavailable or fails to
// open becomes silence, never a substitute: Desktop never falls back to the
// full mix while Discord runs, so a user who opted out of Discord audio can't
// end up recording their call.
func openReplayAudio(a ReplayAudio, discordPID uint32) ([]audioSource, []replayAudioSrc) {
	name := func(tag string) string { return fmt.Sprintf("toru-replay-%s-%d", tag, time.Now().UnixNano()) }
	srcs := replayAudioSources(a)
	var live []audioSource
	for i := range srcs {
		var src audioSource
		var err error
		switch {
		case srcs[i].title == replayTrackDesktop && discordPID != 0:
			src, err = startExcludeProcessLoopbackAudio(discordPID, name("sys-nodiscord"))
		case srcs[i].title == replayTrackDesktop:
			src, err = startLoopbackAudio(name("sys"))
		case srcs[i].title == replayTrackDiscord && discordPID != 0:
			src, err = startProcessLoopbackAudio(discordPID, name("discord"))
		}
		if err != nil || src == nil {
			continue
		}
		in := src.Input()
		srcs[i].in = &in
		live = append(live, src)
	}
	return live, srcs
}

// prune deletes the oldest segments beyond the configured window (+1 spare and
// the in-progress segment).
func (b *ReplayBuffer) prune(cfg ReplayConfig) {
	if cfg.Persist {
		b.prunePersistent(cfg)
		return
	}
	keep := (cfg.KeepSeconds+replaySegmentSec-1)/replaySegmentSec + 2
	b.segMu.Lock()
	defer b.segMu.Unlock()
	segs := listReplaySegments(cfg.Dir)
	for len(segs) > keep {
		_ = os.Remove(segs[0].path)
		segs = segs[1:]
	}
}

// Save writes the last `seconds` of the buffer (rounded up to whole segments;
// <= 0 or more than the window means the whole window) to outPath as MP4. It
// first waits for the segment being written at call time to close, so the
// clip ends at the moment Save was called.
func (b *ReplayBuffer) Save(seconds int, outPath string) error {
	b.mu.Lock()
	running := b.stop != nil
	cfg := b.cfg
	done := b.done
	b.mu.Unlock()
	if !running {
		return fmt.Errorf("instant replay is off")
	}
	if seconds <= 0 || seconds > cfg.KeepSeconds {
		seconds = cfg.KeepSeconds
	}
	bin, err := LocateFFmpeg()
	if err != nil {
		return err
	}

	segs := listReplaySegments(cfg.Dir)
	if len(segs) == 0 {
		return fmt.Errorf("nothing buffered yet")
	}
	pressed := segs[len(segs)-1].name
	deadline := time.Now().Add((replaySegmentSec + 3) * time.Second)
	for time.Now().Before(deadline) {
		if cur := listReplaySegments(cfg.Dir); len(cur) > 0 && cur[len(cur)-1].name != pressed {
			break
		}
		select {
		case <-done:
			deadline = time.Now() // buffer stopped: take what's on disk
		case <-time.After(100 * time.Millisecond):
		}
	}

	b.segMu.Lock()
	defer b.segMu.Unlock()
	segs = listReplaySegments(cfg.Dir)
	end := -1
	for i, s := range segs {
		if s.name == pressed {
			end = i
		}
	}
	if end < 0 {
		return fmt.Errorf("replay segments disappeared (buffer restarted?)")
	}
	want := (seconds + replaySegmentSec - 1) / replaySegmentSec
	start := max(end+1-want, 0)
	return stitchReplay(bin, segs[start:end+1], outPath, replayTrackTitles(cfg.Audio))
}

// stitchReplay stream-copies segs into one MP4 at outPath. MPEG-TS segments of
// one ffmpeg run are byte-concatenated straight into ffmpeg's stdin (seamless);
// multiple runs, or Matroska (AV1) segments, go through the concat demuxer.
// Writes to a temp name first so outPath only ever appears complete.
func stitchReplay(bin string, segs []replaySegment, outPath string, trackTitles []string) error {
	if len(segs) == 0 {
		return fmt.Errorf("nothing buffered yet")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	tmpOut := outPath + ".part"
	// -map 0 keeps every audio track (the default picks one). Segments don't
	// carry track titles (MPEG-TS has none), so they're set here; the mix
	// is the default track players pick.
	tail := []string{"-map", "0", "-c", "copy"}
	for i, title := range trackTitles {
		disp := "0"
		if i == 0 {
			disp = "default"
		}
		// MP4 keeps "title" as the track name; editors (Premiere, Resolve,
		// VLC) show handler_name.
		tail = append(tail,
			fmt.Sprintf("-metadata:s:a:%d", i), "title="+title,
			fmt.Sprintf("-metadata:s:a:%d", i), "handler_name="+title,
			fmt.Sprintf("-disposition:a:%d", i), disp)
	}
	tail = append(tail, "-movflags", "+faststart", "-f", "mp4", tmpOut)
	var groups [][]replaySegment
	for _, s := range segs {
		if n := len(groups); n > 0 && groups[n-1][0].run == s.run {
			groups[n-1] = append(groups[n-1], s)
		} else {
			groups = append(groups, []replaySegment{s})
		}
	}

	var cmd *exec.Cmd
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()
	if strings.EqualFold(filepath.Ext(segs[0].path), ".ts") && len(groups) == 1 {
		readers := make([]io.Reader, 0, len(segs))
		for _, s := range segs {
			f, err := os.Open(s.path)
			if err != nil {
				return err
			}
			closers = append(closers, f)
			readers = append(readers, f)
		}
		cmd = exec.CommandContext(ctx, bin, append([]string{"-hide_banner", "-loglevel", "error", "-y", "-f", "mpegts", "-i", "pipe:0"}, tail...)...)
		cmd.Stdin = io.MultiReader(readers...)
	} else {
		tmpDir, err := os.MkdirTemp(filepath.Dir(segs[0].path), "stitch-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()
		var list strings.Builder
		list.WriteString("ffconcat version 1.0\n")
		for gi, g := range groups {
			parts := []string{}
			if strings.EqualFold(filepath.Ext(g[0].path), ".ts") {
				joined := filepath.Join(tmpDir, fmt.Sprintf("run%d.ts", gi))
				if err := concatFiles(g, joined); err != nil {
					return err
				}
				parts = append(parts, joined)
			} else {
				for _, s := range g {
					parts = append(parts, s.path)
				}
			}
			for _, p := range parts {
				fmt.Fprintf(&list, "file '%s'\n", strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`))
			}
		}
		listPath := filepath.Join(tmpDir, "list.ffconcat")
		if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
			return err
		}
		cmd = exec.CommandContext(ctx, bin, append([]string{"-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", listPath}, tail...)...)
	}
	configureSysProcAttr(cmd)
	stderr := &tailBuffer{max: stderrTailSize}
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpOut)
		return fmt.Errorf("save replay: %v: %s", err, lastLine(stderr.String()))
	}
	return os.Rename(tmpOut, outPath)
}

// concatFiles byte-concatenates segs into dst (MPEG-TS joins seamlessly).
func concatFiles(segs []replaySegment, dst string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	for _, s := range segs {
		in, err := os.Open(s.path)
		if err != nil {
			_ = out.Close()
			return err
		}
		_, err = io.Copy(out, in)
		_ = in.Close()
		if err != nil {
			_ = out.Close()
			return err
		}
	}
	return out.Close()
}

// replaySegment is one segment file on disk.
type replaySegment struct {
	name string
	path string
	run  int
}

var replaySegmentName = regexp.MustCompile(`^r(\d{4,})-\d{8}\.(ts|mkv)$`)

// listReplaySegments returns dir's segments oldest-first. Names are
// zero-padded run+index, so lexical order is chronological order.
func listReplaySegments(dir string) []replaySegment {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []replaySegment
	for _, e := range entries {
		m := replaySegmentName.FindStringSubmatch(e.Name())
		if m == nil || e.IsDir() {
			continue
		}
		run, _ := strconv.Atoi(m[1])
		out = append(out, replaySegment{name: e.Name(), path: filepath.Join(dir, e.Name()), run: run})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func replaySegmentExt(codec string) string {
	if codec == ReplayCodecAV1 {
		return ".mkv"
	}
	return ".ts"
}

// resetReplayDir creates dir and removes any segments or stitch leftovers in
// it (from this or an earlier session). It only deletes files it recognizes.
func resetReplayDir(dir string) error {
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := filepath.Join(dir, e.Name())
		switch {
		case e.IsDir() && strings.HasPrefix(e.Name(), "stitch-"):
			_ = os.RemoveAll(p)
		case !e.IsDir() && replaySegmentName.MatchString(e.Name()):
			_ = os.Remove(p)
		}
	}
	return nil
}

// lastLine returns the last non-empty line of s (ffmpeg's actionable error).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return "unknown error"
}
