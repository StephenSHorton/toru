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

	saving atomic.Bool
}

// NewService loads the persisted settings. Nothing runs until Init.
func NewService() *ReplayService {
	s := &ReplayService{buf: capture.NewReplayBuffer(), settings: loadSettings()}
	s.buf.SetOnStatus(func(st capture.ReplayStatus) { emit(EventStatus, st) })
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
	st := s.GetSettings()
	if st.Enabled {
		_ = s.apply(st)
	}
	s.notify(st)
}

// Shutdown stops the buffer and deletes its segments.
//
//wails:ignore
func (s *ReplayService) Shutdown() { s.buf.Stop() }

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
	s.mu.Unlock()
	if err := saveSettings(next); err != nil {
		return s.buf.Status(), fmt.Errorf("save settings: %w", err)
	}

	var err error
	switch {
	case !next.Enabled:
		s.buf.Stop()
	case !prev.Enabled || !s.buf.Running() || toConfig(prev) != toConfig(next):
		err = s.apply(next)
	}
	s.notify(next)
	return s.buf.Status(), err
}

func (s *ReplayService) apply(st Settings) error {
	cfg := toConfig(st)
	if cfg.Dir == "" {
		return fmt.Errorf("replay: no cache directory")
	}
	return s.buf.Start(cfg)
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
func toConfig(st Settings) capture.ReplayConfig {
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
		Audio: capture.ReplayAudio{
			Desktop:   st.DesktopAudio,
			Discord:   st.DiscordAudio,
			MicDevice: st.MicDevice,
		},
	}
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
	path, err := s.save(st)
	if st.SoundOnSave {
		playSaveSound(err == nil)
	}
	if err != nil {
		emit(EventError, err.Error())
		return "", err
	}
	emit(EventSaved, path)
	return path, nil
}

func (s *ReplayService) save(st Settings) (string, error) {
	dir := saveDirFor(st)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("replay folder: %w", err)
	}
	path := uniquePath(filepath.Join(dir, "Replay "+time.Now().Format("2006-01-02 15.04.05")), ".mp4")
	if err := s.buf.Save(st.DurationSec, path); err != nil {
		return "", err
	}
	return path, nil
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
