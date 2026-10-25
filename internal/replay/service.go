package replay

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// Go -> JS events.
const (
	EventStatus = "replay:status" // payload: capture.ReplayStatus
	EventSaved  = "replay:saved"  // payload: saved clip path
	EventError  = "replay:error"  // payload: error message from a failed save
)

// Monitor is one row of the Settings page's monitor picker.
type Monitor struct {
	ID      int    `json:"id"`
	Label   string `json:"label"`
	W       int    `json:"w"`
	H       int    `json:"h"`
	Primary bool   `json:"primary"`
}

// ReplayService is the Wails-bound instant-replay API.
//
// JS binding name: ReplayService.*
type ReplayService struct {
	buf *capture.ReplayBuffer

	mu       sync.Mutex
	settings Settings
	onChange func(Settings)
	// clipOpener shows a clip in Toru's editor (see SetClipOpener).
	clipOpener func(path, servedURL string)
	// sessionOpener shows a recording in the timeline viewer.
	sessionOpener func(id int)

	saving atomic.Bool

	// FFmpeg download state (see ffmpeg.go).
	ffMu sync.Mutex
	ff   FFmpegState

	// Recorder state (guarded by mu): the game a background recording is
	// following, whether a manual recording is on, and the config the buffer
	// was last started with.
	game       string
	gamePID    uint32
	manualOn   bool
	runningCfg capture.ReplayConfig
	watchStop  chan struct{}
	protect    protectCache
}

// NewService loads the persisted settings. Nothing runs until Init.
func NewService() *ReplayService {
	s := &ReplayService{buf: capture.NewReplayBuffer(), settings: loadSettings()}
	s.buf.SetOnStatus(func(st capture.ReplayStatus) { emit(EventStatus, st) })
	s.buf.SetProtect(s.isProtected)
	return s
}

// SetOnChange registers a callback fired after the settings change (and once
// from Init). main uses it to arm/disarm the hotkey and refresh the tray.
//
//wails:ignore
func (s *ReplayService) SetOnChange(fn func(Settings)) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Init starts the buffer if the user has it enabled. Called once from main
// after the app is running; a start failure surfaces in GetStatus().Error.
//
//wails:ignore
func (s *ReplayService) Init() {
	// No FFmpeg (the installer doesn't ship it): fetch it once in the
	// background; recording starts when it lands.
	s.DownloadFFmpeg()
	_ = s.reconcile()
	s.notify(s.GetSettings())
}

// Shutdown stops the buffer (instant-replay segments are deleted; session
// recordings stay).
//
//wails:ignore
func (s *ReplayService) Shutdown() {
	s.stopGameWatch()
	s.buf.Stop()
}

// GetSettings returns the current settings.
func (s *ReplayService) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings normalizes, persists and applies next. The buffer only
// restarts when a capture-relevant field changed (not for e.g. the save
// folder). It returns the resulting status; an error means the settings were
// saved but the buffer could not start (no hardware encoder, …).
func (s *ReplayService) SetSettings(next Settings) (capture.ReplayStatus, error) {
	next = next.Normalize()
	s.mu.Lock()
	prev := s.settings
	s.settings = next
	if next.Mode != prev.Mode || !next.Enabled {
		s.manualOn = false // a manual recording ends when its mode does
	}
	s.mu.Unlock()
	if err := saveSettings(next); err != nil {
		return s.buf.Status(), fmt.Errorf("save settings: %w", err)
	}
	err := s.reconcile()
	s.notify(next)
	return s.buf.Status(), err
}

// reconcile starts, restarts or stops the buffer to match the settings and
// recorder state, and runs the game watcher when background recording
// follows games. Restarts only happen when the capture config changed.
func (s *ReplayService) reconcile() error {
	st := s.GetSettings()
	if st.Enabled && st.Mode == ModeBackground && st.GamesOnly {
		s.startGameWatch()
	} else {
		s.stopGameWatch()
	}

	s.mu.Lock()
	game, manual := s.game, s.manualOn
	if !st.Enabled || st.Mode != ModeBackground || !st.GamesOnly {
		game = ""
	}
	want := st.Enabled && (st.Mode == ModeReplay ||
		(st.Mode == ModeBackground && (!st.GamesOnly || game != "")) ||
		(st.Mode == ModeManual && manual))
	cfg := toConfigFor(st, game)
	same := cfg == s.runningCfg
	// A new storage limit applies to the running recorder without a restart.
	limitsOnly := !same && cfg.Persist && withMaxBytes(cfg, 0) == withMaxBytes(s.runningCfg, 0)
	s.mu.Unlock()

	if !want {
		s.buf.Stop()
		s.mu.Lock()
		s.runningCfg = capture.ReplayConfig{}
		s.mu.Unlock()
		return nil
	}
	if same && s.buf.Running() {
		return nil
	}
	if limitsOnly && s.buf.Running() {
		s.buf.SetMaxBytes(cfg.MaxBytes)
		s.mu.Lock()
		s.runningCfg = cfg
		s.mu.Unlock()
		return nil
	}
	if cfg.Dir == "" {
		return fmt.Errorf("replay: no storage directory")
	}
	err := s.buf.Start(cfg)
	s.mu.Lock()
	s.runningCfg = cfg
	if err != nil {
		s.runningCfg = capture.ReplayConfig{}
	}
	s.mu.Unlock()
	return err
}

func (s *ReplayService) notify(st Settings) {
	s.mu.Lock()
	cb := s.onChange
	s.mu.Unlock()
	if cb != nil {
		cb(st)
	}
}

// toConfig maps settings onto the buffer config. Fields that don't affect
// capture (save folder, sound) are deliberately absent so comparing two
// configs answers "does the buffer need a restart?".
func toConfig(st Settings) capture.ReplayConfig { return toConfigFor(st, "") }

// toConfigFor is toConfig for a recording that follows game (background
// mode); a different game means a new run, i.e. a new session.
func toConfigFor(st Settings, game string) capture.ReplayConfig {
	cfg := baseConfig(st)
	if st.Mode == ModeReplay {
		return cfg
	}
	cfg.Dir = recordingsDirFor(st)
	cfg.Persist = true
	cfg.MaxBytes = int64(st.MaxDiskGB) << 30
	cfg.MinFreeBytes = minFreeBytes
	if preset, ok := qualityPresets[st.RecordingQuality]; ok {
		// Presets record HEVC: the same picture as H.264 in about half the
		// space. Without a hardware HEVC encoder they fall back to H.264 at
		// a bitrate that keeps the quality.
		cfg.FPS = min(cfg.FPS, 60)
		if hevcAvailable() {
			cfg.Codec, cfg.BitrateKbps = capture.ReplayCodecHEVC, preset.hevcKbps
		} else {
			cfg.Codec, cfg.BitrateKbps = capture.ReplayCodecH264, preset.h264Kbps
		}
	} else {
		cfg.FPS, cfg.BitrateKbps = st.RecordingFPS, st.RecordingBitrateMbps*1000
	}
	// No age limit: sessions are kept whole until the storage limit needs
	// their space, then the oldest goes (manual recordings are protected).
	cfg.KeepSeconds = 0
	cfg.WholeSessions = true
	cfg.Tag = encodeTag(st.Mode, game, cfg.Codec)
	return cfg
}

func withMaxBytes(c capture.ReplayConfig, n int64) capture.ReplayConfig {
	c.MaxBytes = n
	return c
}

// qualityPresets are the recording-quality presets' bitrates (kbps).
var qualityPresets = map[string]struct{ hevcKbps, h264Kbps int }{
	QualitySaver:    {5000, 8000},
	QualityStandard: {8000, 12000},
	QualityHigh:     {15000, 24000},
}

// hevcAvailable reports whether a hardware HEVC encoder works (cached probe).
// A var so tests don't depend on the machine's GPU.
var hevcAvailable = func() bool { return capture.ReplayEncoderAvailable(capture.ReplayCodecHEVC) }

// RecordingPresets returns each recording-quality preset's real frame rate,
// codec and bitrate on this PC (HEVC when the GPU can encode it), for the
// Settings estimates.
func (s *ReplayService) RecordingPresets() []RecordingPreset {
	st := s.GetSettings()
	out := []RecordingPreset{}
	for _, q := range QualityOptions {
		tmp := st
		tmp.Mode, tmp.RecordingQuality = ModeBackground, q
		cfg := toConfigFor(tmp, "")
		out = append(out, RecordingPreset{Quality: q, FPS: cfg.FPS, Codec: cfg.Codec, Mbps: cfg.BitrateKbps / 1000})
	}
	return out
}

// RecordingPreset is one recording-quality option as it will actually record.
type RecordingPreset struct {
	Quality string `json:"quality"`
	FPS     int    `json:"fps"`
	Codec   string `json:"codec"`
	Mbps    int    `json:"mbps"`
}

func baseConfig(st Settings) capture.ReplayConfig {
	dir, _ := segmentDir()
	return capture.ReplayConfig{
		MonitorID:     st.MonitorID,
		FPS:           st.FPS,
		Height:        st.Height,
		BitrateKbps:   st.BitrateMbps * 1000,
		Codec:         st.Codec,
		IncludeCursor: st.IncludeCursor,
		KeepSeconds:   st.DurationSec,
		Dir:           dir,
		// A source at 0% volume is simply off.
		Audio: capture.ReplayAudio{
			Desktop:        st.DesktopAudio && st.DesktopVolume > 0,
			Discord:        st.DiscordAudio && st.DiscordVolume > 0,
			MicDevice:      micIf(st.MicVolume > 0, st.MicDevice),
			DesktopVolume:  st.DesktopVolume,
			DiscordVolume:  st.DiscordVolume,
			MicVolume:      st.MicVolume,
			SeparateTracks: st.SeparateTracks,
			BitrateKbps:    st.AudioBitrateKbps,
			MicNoise:       st.MicNoise,
		},
	}
}

func micIf(on bool, device string) string {
	if on {
		return device
	}
	return ""
}

// GetStatus returns the buffer's live status.
func (s *ReplayService) GetStatus() capture.ReplayStatus { return s.buf.Status() }

// SaveReplay saves the configured duration (Settings.DurationSec) to the save
// folder and returns the clip's path. This is what the hotkey and tray run.
// A press while a save is already in flight is rejected rather than queued.
func (s *ReplayService) SaveReplay() (string, error) {
	if !s.saving.CompareAndSwap(false, true) {
		return "", fmt.Errorf("already saving a replay")
	}
	defer s.saving.Store(false)

	st := s.GetSettings()
	// Look at what's focused now, at the key press: by the time the clip is
	// stitched the user may have tabbed out.
	game := ""
	if app, ok := foreground(); ok {
		game = gameFolderFor(app)
	}
	what := "Last " + formatClipLength(st.DurationSec)
	if game != "" {
		what = game + " · " + what
	}
	if st.ToastOnSave {
		showToast("Saving clip…", what, true)
	}
	path, err := s.save(st, game)
	if st.SoundOnSave {
		playSaveSound(err == nil)
	}
	if st.ToastOnSave {
		if err != nil {
			showToast("Couldn't save clip", err.Error(), false)
		} else {
			showToast("Clip saved", what, true)
		}
	}
	if err != nil {
		emit(EventError, err.Error())
		return "", err
	}
	emit(EventSaved, path)
	if s.recordingSession() {
		s.addMarker(MarkerClip, filepath.Base(path))
	}
	return path, nil
}

// save writes the clip. game is the focused game's folder name ("" = none):
// it prefixes the file name, and with GameFolders on picks the sub-folder.
func (s *ReplayService) save(st Settings, game string) (string, error) {
	dir, prefix := clipLocation(st, game)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("replay folder: %w", err)
	}
	path := uniquePath(filepath.Join(dir, prefix+" "+time.Now().Format("2006-01-02 15.04.05")), ".mp4")
	if err := s.buf.Save(st.DurationSec, path); err != nil {
		return "", err
	}
	return path, nil
}

// formatClipLength renders a clip length for the toast: "30s", "1 min", "1:30".
func formatClipLength(sec int) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%ds", sec)
	case sec%60 == 0:
		return fmt.Sprintf("%d min", sec/60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

// clipLocation returns the folder and file-name prefix for a clip.
func clipLocation(st Settings, game string) (dir, prefix string) {
	dir, prefix = saveDirFor(st), "Replay"
	if game != "" {
		prefix = game
	}
	if st.GameFolders {
		folder := game
		if folder == "" {
			folder = DesktopFolder
		}
		dir = filepath.Join(dir, folder)
	}
	return dir, prefix
}

// uniquePath returns base+ext, or base (2)+ext, … if that file exists.
func uniquePath(base, ext string) string {
	p := base + ext
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = fmt.Sprintf("%s (%d)%s", base, i, ext)
	}
}

func saveDirFor(st Settings) string {
	if st.SaveDir != "" {
		return st.SaveDir
	}
	return DefaultSaveDir()
}

// ListMonitors returns the monitors the buffer can capture.
func (s *ReplayService) ListMonitors() []Monitor {
	out := []Monitor{}
	for _, d := range capture.EnumDisplays() {
		primary := d.X == 0 && d.Y == 0
		label := fmt.Sprintf("Display %d · %d×%d", d.Index+1, d.W, d.H)
		if primary {
			label += " (primary)"
		}
		out = append(out, Monitor{ID: d.Index, Label: label, W: d.W, H: d.H, Primary: primary})
	}
	return out
}

// ListMicrophones returns the microphone device names.
func (s *ReplayService) ListMicrophones() []string {
	mics := capture.ListMicrophones()
	if mics == nil {
		return []string{}
	}
	return mics
}

// DiscordRunning reports whether a Discord client is running right now.
func (s *ReplayService) DiscordRunning() bool { return capture.DiscordPID() != 0 }

// EncoderSupport reports which codecs have a usable hardware encoder. The
// first call runs tiny probe encodes (~1s); results are cached after that.
func (s *ReplayService) EncoderSupport() map[string]bool {
	out := map[string]bool{}
	for _, c := range CodecOptions {
		out[c] = capture.ReplayEncoderAvailable(c)
	}
	return out
}

// GetSaveDir returns the folder clips are saved to.
func (s *ReplayService) GetSaveDir() string { return saveDirFor(s.GetSettings()) }

// PickSaveDir opens a folder picker and, if the user chooses one, saves clips
// there from now on. Returns the new folder, or "" if cancelled.
func (s *ReplayService) PickSaveDir() (string, error) {
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("replay: no running application")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle("Choose where instant replays are saved").
		SetDirectory(s.GetSaveDir())
	if win := app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	path, err := dlg.PromptForSingleSelection()
	if err != nil || path == "" {
		return "", nil // cancel is not an error for the frontend
	}
	st := s.GetSettings()
	st.SaveDir = path
	if _, err := s.SetSettings(st); err != nil {
		return "", err
	}
	return path, nil
}

// ResetSaveDir goes back to <Videos>\Toru Replays.
func (s *ReplayService) ResetSaveDir() error {
	st := s.GetSettings()
	st.SaveDir = ""
	_, err := s.SetSettings(st)
	return err
}

// OpenSaveDir reveals the save folder in Explorer (creating it if needed).
func (s *ReplayService) OpenSaveDir() error {
	dir := s.GetSaveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return openFolder(dir)
}

func openFolder(dir string) error {
	if runtime.GOOS == "windows" {
		return exec.Command("explorer.exe", dir).Start()
	}
	return exec.Command("xdg-open", dir).Start()
}

func emit(name string, data any) {
	if app := application.Get(); app != nil {
		app.Event.Emit(name, data)
	}
}
