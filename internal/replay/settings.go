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
	Enabled bool `json:"enabled"` // recording is on (in the chosen Mode)
	// Mode picks how Toru records (Steam's "Game Recording" modes plus
	// ShadowPlay-style instant replay):
	//   ModeReplay     keep only the last DurationSec; the hotkey saves it
	//   ModeBackground record whole sessions (by default only while a game
	//                  runs); when MaxDiskGB is reached the oldest whole
	//                  session is deleted; timeline
	//   ModeManual     record between presses of the record hotkey; kept
	//                  until deleted
	// The save-clip hotkey works in every mode.
	Mode          string `json:"mode"`
	MaxDiskGB     int    `json:"maxDiskGB"`     // cap for all recordings
	GamesOnly     bool   `json:"gamesOnly"`     // background: only while a game runs
	RecordingsDir string `json:"recordingsDir"` // "" = DefaultRecordingsDir()
	// RecordingQuality is the video quality for background/manual recording,
	// separate from instant replay's: whole sessions need a much lower
	// bitrate to fit on disk. QualityCustom uses the Video settings.
	RecordingQuality string `json:"recordingQuality"`
	// Custom recording quality (QualityCustom), separate from instant
	// replay's FPS/BitrateMbps.
	RecordingFPS         int `json:"recordingFps"`
	RecordingBitrateMbps int `json:"recordingBitrateMbps"`
	// RecordingNotices toasts when a background/manual recording starts and
	// stops and when a marker is added.
	RecordingNotices bool `json:"recordingNotices"`

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
	ModeOptions      = []string{ModeReplay, ModeBackground, ModeManual}
	DiskGBOptions    = []int{10, 25, 50, 100, 200, 500}
	QualityOptions   = []string{QualitySaver, QualityStandard, QualityHigh, QualityCustom}
)

// Recording qualities for background/manual recording (Settings.RecordingQuality).
const (
	QualitySaver    = "saver"    // 60 fps, HEVC 5 Mbps: ~2.3 GB per hour
	QualityStandard = "standard" // 60 fps, HEVC 8 Mbps: ~3.6 GB per hour
	QualityHigh     = "high"     // 60 fps, HEVC 15 Mbps: ~6.8 GB per hour
	QualityCustom   = "custom"   // RecordingFPS / RecordingBitrateMbps
)

// minFreeBytes is the free space recordings never eat into on their drive.
const minFreeBytes = 10 << 30

// Recording modes (Settings.Mode).
const (
	ModeReplay     = "replay"
	ModeBackground = "background"
	ModeManual     = "manual"
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
		Enabled:              false,
		Mode:                 ModeReplay,
		MaxDiskGB:            25,
		RecordingQuality:     QualityStandard,
		RecordingFPS:         60,
		RecordingBitrateMbps: 20,
		GamesOnly:            true,
		RecordingNotices:     true,
		DurationSec:          60,
		MonitorID:            -1,
		FPS:                  60,
		Height:               0,
		BitrateMbps:          20,
		Codec:                capture.ReplayCodecH264,
		IncludeCursor:        true,
		DesktopAudio:         true,
		DiscordAudio:         true,
		DesktopVolume:        100,
		DiscordVolume:        100,
		MicVolume:            100,
		AudioBitrateKbps:     160,
		GameFolders:          true,
		ToastOnSave:          true,
		SoundOnSave:          true,
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
	if !slices.Contains(ModeOptions, s.Mode) {
		s.Mode = d.Mode
	}
	if !slices.Contains(DiskGBOptions, s.MaxDiskGB) {
		s.MaxDiskGB = d.MaxDiskGB
	}
	if !slices.Contains(QualityOptions, s.RecordingQuality) {
		s.RecordingQuality = d.RecordingQuality
	}
	if !slices.Contains(FPSOptions, s.RecordingFPS) {
		s.RecordingFPS = d.RecordingFPS
	}
	if s.RecordingBitrateMbps == 0 {
		s.RecordingBitrateMbps = d.RecordingBitrateMbps
	}
	s.RecordingBitrateMbps = min(max(s.RecordingBitrateMbps, minBitrateMbps), maxBitrateMbps)
	// Session recordings stream into the timeline as HLS, which can't carry
	// AV1 in MPEG-TS segments.
	if s.Mode != ModeReplay && s.Codec == capture.ReplayCodecAV1 {
		s.Codec = capture.ReplayCodecH264
	}
	s.RecordingsDir = strings.TrimSpace(s.RecordingsDir)
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

// DefaultRecordingsDir is where background/manual recordings live:
// %LOCALAPPDATA%\toru\recordings. Deliberately not under Videos, which is
// often synced to OneDrive; background footage churns many GB.
func DefaultRecordingsDir() string {
	cache, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(cache, "toru", "recordings")
}

func recordingsDirFor(st Settings) string {
	if st.RecordingsDir != "" {
		return st.RecordingsDir
	}
	return DefaultRecordingsDir()
}

// DefaultSaveDir is where saved clips go unless the user picks a folder:
// <Videos>\Toru Replays. Clips deliberately do NOT go into the capture
// Library, which keeps only its newest few items and deletes older files.
func DefaultSaveDir() string {
	return filepath.Join(videosDir(), "Toru Replays")
}
