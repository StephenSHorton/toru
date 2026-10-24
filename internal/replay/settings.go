// Package replay is the instant-replay feature around capture.ReplayBuffer:
// the user's settings (persisted to %AppData%\toru\replay.json), the
// Wails-bound ReplayService the Settings page drives, and the save action
// behind the "Save instant replay" hotkey and tray item.
package replay

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/StephenSHorton/toru/internal/capture"
)

// Settings is the user's instant-replay configuration. JSON tags are the
// stable on-disk + TS field names.
type Settings struct {
	Enabled       bool   `json:"enabled"`       // run the buffer in the background
	DurationSec   int    `json:"durationSec"`   // clip length the hotkey saves (= the window kept)
	MonitorID     int    `json:"monitorId"`     // capture.ScreenInfo.ID; -1 = primary
	FPS           int    `json:"fps"`           // 30 | 60 | 120 | 144
	Height        int    `json:"height"`        // 0 = native (zero-copy) | 2160 | 1440 | 1080 | 720
	BitrateMbps   int    `json:"bitrateMbps"`   // constant video bitrate
	Codec         string `json:"codec"`         // capture.ReplayCodec*
	IncludeCursor bool   `json:"includeCursor"` // draw the mouse pointer
	DesktopAudio  bool   `json:"desktopAudio"`  // system mix (minus Discord unless DiscordAudio)
	DiscordAudio  bool   `json:"discordAudio"`  // Discord's process tree
	MicDevice     string `json:"micDevice"`     // dshow mic name; "" = off
	// Per-source volume in percent (0 mutes, 100 = as captured, max 200).
	DesktopVolume int `json:"desktopVolume"`
	DiscordVolume int `json:"discordVolume"`
	MicVolume     int `json:"micVolume"`
	// SeparateTracks adds each source as its own audio track after the mix.
	SeparateTracks   bool   `json:"separateTracks"`
	AudioBitrateKbps int    `json:"audioBitrateKbps"` // AAC per track
	SaveDir          string `json:"saveDir"`          // "" = DefaultSaveDir()
	// GameFolders files each clip under a folder named after the game in
	// focus (DesktopFolder when none), like ShadowPlay.
	GameFolders bool `json:"gameFolders"`
	SoundOnSave bool `json:"soundOnSave"` // short chime when a clip lands
	// ToastOnSave shows a small corner message that never takes focus.
	ToastOnSave bool `json:"toastOnSave"`
}

// Option lists the UI offers. Normalize snaps anything else to the nearest
// sane value so a hand-edited replay.json can't wedge the buffer.
var (
	DurationOptions  = []int{15, 30, 60, 120, 300, 600, 1200}
	FPSOptions       = []int{30, 60, 120, 144}
	HeightOptions    = []int{0, 2160, 1440, 1080, 720}
	CodecOptions     = []string{capture.ReplayCodecH264, capture.ReplayCodecHEVC, capture.ReplayCodecAV1}
	AudioKbpsOptions = []int{96, 128, 160, 192, 256, 320}
)

const (
	minDurationSec = 5
	maxDurationSec = 1200 // 20 minutes
	minBitrateMbps = 2
	maxBitrateMbps = 150
	maxVolume      = 200
)

// DefaultSettings is the out-of-box configuration: off until the user opts in,
// one minute at native resolution / 60fps / 20 Mbps H.264 (plays inline in
// Discord), desktop + Discord audio, no mic.
func DefaultSettings() Settings {
	return Settings{
		Enabled:          false,
		DurationSec:      60,
		MonitorID:        -1,
		FPS:              60,
		Height:           0,
		BitrateMbps:      20,
		Codec:            capture.ReplayCodecH264,
		IncludeCursor:    true,
		DesktopAudio:     true,
		DiscordAudio:     true,
		DesktopVolume:    100,
		DiscordVolume:    100,
		MicVolume:        100,
		AudioBitrateKbps: 160,
		GameFolders:      true,
		ToastOnSave:      true,
		SoundOnSave:      true,
	}
}

// Normalize clamps every field into its supported range.
func (s Settings) Normalize() Settings {
	d := DefaultSettings()
	s.DurationSec = min(max(s.DurationSec, minDurationSec), maxDurationSec)
	if !slices.Contains(FPSOptions, s.FPS) {
		s.FPS = d.FPS
	}
	if !slices.Contains(HeightOptions, s.Height) {
		s.Height = d.Height
	}
	s.BitrateMbps = min(max(s.BitrateMbps, minBitrateMbps), maxBitrateMbps)
	if !slices.Contains(CodecOptions, s.Codec) {
		s.Codec = d.Codec
	}
	s.MicDevice = strings.TrimSpace(s.MicDevice)
	s.DesktopVolume = min(max(s.DesktopVolume, 0), maxVolume)
	s.DiscordVolume = min(max(s.DiscordVolume, 0), maxVolume)
	s.MicVolume = min(max(s.MicVolume, 0), maxVolume)
	if !slices.Contains(AudioKbpsOptions, s.AudioBitrateKbps) {
		s.AudioBitrateKbps = d.AudioBitrateKbps
	}
	s.SaveDir = strings.TrimSpace(s.SaveDir)
	return s
}

// settingsMu serializes replay.json load-modify-save.
var settingsMu sync.Mutex

func settingsPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "replay.json"), nil
}

// loadSettings returns the persisted settings, or the defaults when the file
// is missing or unreadable. Missing fields keep their default values.
func loadSettings() Settings {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	s := DefaultSettings()
	path, err := settingsPath()
	if err != nil {
		return s
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	// Tolerate a UTF-8 BOM: Notepad and PowerShell 5's Set-Content add one,
	// and encoding/json rejects it.
	raw = bytes.TrimPrefix(raw, []byte("\uFEFF"))
	if json.Unmarshal(raw, &s) != nil {
		return DefaultSettings()
	}
	return s.Normalize()
}

// saveSettings writes s atomically (tmp + rename).
func saveSettings(s Settings) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	path, err := settingsPath()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "replay-*.json.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// segmentDir is where the rolling segments live: %LOCALAPPDATA%\toru\replay
// (a cache dir — it is wiped whenever the buffer starts or stops).
func segmentDir() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "toru", "replay"), nil
}

// DefaultSaveDir is where saved clips go unless the user picks a folder:
// <Videos>\Toru Replays. Clips deliberately do NOT go into the capture
// Library, which keeps only its newest few items and deletes older files.
func DefaultSaveDir() string {
	return filepath.Join(videosDir(), "Toru Replays")
}
