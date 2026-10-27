// Package stream is Toru's own screen sharing: a room of friends (found
// through public relays with no accounts, see room.go) where anyone can go
// live and the others watch peer to peer over WebRTC, at whatever
// resolution, frame rate and bitrate the streamer picks.
package stream

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

// Codecs a stream can use. Every viewer's Toru (WebView2) decodes both.
const (
	CodecH264 = "h264"
	CodecAV1  = "av1"
)

// Call noise suppression modes. Standard is Chromium's own (WebRTC's) noise
// suppressor; AI is DeepFilterNet3 running in the call window, which also
// takes out sudden sounds like key presses.
const (
	NoiseOff      = "off"
	NoiseStandard = "standard"
	NoiseAI       = "ai"
)

// Limits on what Settings accepts. They are sanity bounds, not quality caps.
const (
	MinFPS         = 1
	MaxFPS         = 500
	MinBitrateKbps = 500
	MaxBitrateKbps = 500_000
	MinSide        = 128
	MaxWidth       = 7680
	MaxHeight      = 4320
	MaxNameLen     = 32
)

// Settings is the user's stream setup. JSON tags are the stable on-disk and
// TS field names.
type Settings struct {
	Name     string `json:"name"`     // shown to the room
	RoomCode string `json:"roomCode"` // "" = not in a room

	MonitorID   int    `json:"monitorId"`   // capture.ScreenInfo.ID; -1 = primary
	Width       int    `json:"width"`       // output size; 0 x 0 = the monitor's own
	Height      int    `json:"height"`      //
	FPS         int    `json:"fps"`         // capture + encode framerate
	AutoBitrate bool   `json:"autoBitrate"` // follow size × fps × codec (AutoBitrateKbps)
	BitrateKbps int    `json:"bitrateKbps"` // constant video bitrate when AutoBitrate is off
	Codec       string `json:"codec"`       // CodecH264 | CodecAV1
	Cursor      bool   `json:"cursor"`      // draw the mouse pointer
	KeyframeSec int    `json:"keyframeSec"` // a full frame this often; new viewers wait up to this long

	Audio     bool `json:"audio"`     // stream desktop audio (never Toru's own sound)
	AudioKbps int  `json:"audioKbps"` // Opus bitrate

	NotifyLive bool `json:"notifyLive"` // notify when someone in the room goes live
	// ShowPreview opens a window watching our own stream while live. Off by
	// default: decoding your own stream costs GPU time for nothing.
	ShowPreview bool `json:"showPreview"`

	// Voice call. Devices are WebView2 device ids; "" = the system default.
	NoiseMode     string `json:"noiseMode"` // NoiseOff | NoiseStandard | NoiseAI
	MicDevice     string `json:"micDevice"`
	SpeakerDevice string `json:"speakerDevice"`
	// NoiseSuppression is the 0.11.0 on/off switch, kept in step with
	// NoiseMode (on = not off) so an older Toru reading this file still
	// works. A file with only this field gets its NoiseMode from it.
	NoiseSuppression bool `json:"noiseSuppression"`

	// InstallID is this install's room peer id: 16 random bytes in hex, made
	// on first run and kept, so other members (their call volumes, LAN pins)
	// know us across restarts. Never set from the page.
	InstallID string `json:"installId"`
	// PeerVolumes is how loud each other person in the call plays here, in
	// percent (0 = muted for me, 100 = as sent, up to MaxPeerVolume), keyed
	// by their peer id. 100 is not stored.
	PeerVolumes map[string]int `json:"peerVolumes"`

	// Relay (TURN) for calls, streams and LAN when two PCs can't reach each
	// other directly. Both empty = STUN only, direct connections. Edited in
	// stream.json by hand: CredentialURL hands out short-lived TURN REST
	// credentials ({username, credential, ttl, uris}); TurnURLs, when set,
	// replace the uris it returns.
	TurnURLs      []string `json:"turnUrls"`
	CredentialURL string   `json:"credentialUrl"`

	// LAN (docs/lan.md). LanOn is the switch, kept across restarts; the
	// two policy toggles are its Advanced settings. LanSalt is the last
	// Readdress salt sent after an address collision, so the next one moves on.
	LanOn            bool   `json:"lanOn"`
	LanAllowServices bool   `json:"lanAllowServices"`
	LanMDNS          bool   `json:"lanMdns"`
	LanSalt          uint32 `json:"lanSalt"`
}

// MaxPeerVolume is the loudest a call member can be turned up, in percent.
const MaxPeerVolume = 200

// fileOnly copies the fields the page never sets (identity, volumes, relay
// and LAN) from cur into in, so a page save can't wipe them.
func (in Settings) fileOnly(cur Settings) Settings {
	in.InstallID = cur.InstallID
	in.PeerVolumes = cur.PeerVolumes
	in.TurnURLs, in.CredentialURL = cur.TurnURLs, cur.CredentialURL
	in.LanOn, in.LanAllowServices, in.LanMDNS, in.LanSalt = cur.LanOn, cur.LanAllowServices, cur.LanMDNS, cur.LanSalt
	return in
}

// liveSettings is the part of Settings a running stream is built from.
type liveSettings struct {
	MonitorID, Width, Height, FPS int
	AutoBitrate                   bool
	BitrateKbps                   int
	Codec                         string
	Cursor                        bool
	KeyframeSec                   int
	Audio                         bool
	AudioKbps                     int
}

func (s Settings) live() liveSettings {
	return liveSettings{
		MonitorID: s.MonitorID, Width: s.Width, Height: s.Height, FPS: s.FPS,
		AutoBitrate: s.AutoBitrate, BitrateKbps: s.BitrateKbps, Codec: s.Codec, Cursor: s.Cursor,
		KeyframeSec: s.KeyframeSec, Audio: s.Audio, AudioKbps: s.AudioKbps,
	}
}

// validInstallID reports whether id looks like one InstallID (32 hex digits).
func validInstallID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

// validPeerID bounds the peer ids accepted from the page and the room.
func validPeerID(id string) bool {
	return id != "" && len(id) <= 64
}

// cleanTurnURLs keeps the well-formed stun:, turn: and turns: entries.
func cleanTurnURLs(in []string) []string {
	out := []string{}
	for _, u := range in {
		u = strings.TrimSpace(u)
		l := strings.ToLower(u)
		if strings.HasPrefix(l, "turn:") || strings.HasPrefix(l, "turns:") || strings.HasPrefix(l, "stun:") {
			out = append(out, u)
		}
	}
	return out
}

// legacyDefaultKbps was the fixed default before Auto bitrate existed. It is
// still the manual value a new install starts from.
const legacyDefaultKbps = 20_000

// Auto bitrate: bits per pixel per frame for each codec (AV1 holds the same
// picture on about 40% fewer bits), clamped to a range that stays watchable
// at tiny sizes without asking a home upload for the impossible.
const (
	autoBPPH264    = 0.1
	autoBPPAV1     = 0.06
	MinAutoKbps    = 1_500
	MaxAutoKbps    = 150_000
	fallbackWidth  = 1920 // output size while the monitor isn't known
	fallbackHeight = 1080
)

// AutoBitrateKbps is the bitrate Auto picks for an output size, frame rate
// and codec: pixels × fps × bits per pixel, so doubling the frame rate
// doubles the bitrate. The frontend mirrors it (autoBitrateKbps in
// StreamPage.tsx); TestAutoBitrateKbps pins the numbers both must give.
func AutoBitrateKbps(w, h, fps int, codec string) int {
	if w <= 0 || h <= 0 {
		w, h = fallbackWidth, fallbackHeight
	}
	bpp := autoBPPH264
	if codec == CodecAV1 {
		bpp = autoBPPAV1
	}
	kbps := int(math.Round(float64(w) * float64(h) * float64(max(fps, 1)) * bpp / 1000))
	return clamp(kbps, MinAutoKbps, MaxAutoKbps)
}

// VideoKbps is the bitrate to encode at for an output of w × h, the resolved
// size (see OutputSize).
func (s Settings) VideoKbps(w, h int) int {
	if s.AutoBitrate {
		return AutoBitrateKbps(w, h, s.FPS, s.Codec)
	}
	return s.BitrateKbps
}

// OutputSize resolves Width/Height against the captured monitor's size.
func (s Settings) OutputSize(screenW, screenH int) (int, int) {
	if s.Width == 0 || s.Height == 0 {
		return screenW, screenH
	}
	return s.Width, s.Height
}

// DefaultSettings is the first-run setup: the main monitor as-is at 60 fps.
func DefaultSettings() Settings {
	return Settings{
		Name:             defaultName(),
		MonitorID:        -1,
		FPS:              60,
		AutoBitrate:      true,
		BitrateKbps:      legacyDefaultKbps,
		Codec:            CodecH264,
		Cursor:           true,
		KeyframeSec:      2,
		Audio:            true,
		AudioKbps:        160,
		NotifyLive:       true,
		NoiseMode:        NoiseStandard,
		NoiseSuppression: true,
	}
}

func defaultName() string {
	if n := strings.TrimSpace(os.Getenv("USERNAME")); n != "" {
		return clipName(n)
	}
	return "Toru user"
}

func clipName(n string) string {
	n = strings.Join(strings.Fields(n), " ")
	for utf8.RuneCountInString(n) > MaxNameLen {
		_, size := utf8.DecodeLastRuneInString(n)
		n = n[:len(n)-size]
	}
	return n
}

// Normalize clamps every field into range. Width and height are rounded down
// to even numbers (the encoders need 4:2:0); either one being 0 means native.
func Normalize(s Settings) Settings {
	d := DefaultSettings()
	if s.Name = clipName(s.Name); s.Name == "" {
		s.Name = d.Name
	}
	if s.RoomCode != "" {
		if c, ok := NormalizeCode(s.RoomCode); ok {
			s.RoomCode = c
		} else {
			s.RoomCode = ""
		}
	}
	if s.MonitorID < -1 {
		s.MonitorID = -1
	}
	if s.Width <= 0 || s.Height <= 0 {
		s.Width, s.Height = 0, 0
	} else {
		s.Width = clamp(s.Width, MinSide, MaxWidth) &^ 1
		s.Height = clamp(s.Height, MinSide, MaxHeight) &^ 1
	}
	if s.FPS == 0 {
		s.FPS = d.FPS
	}
	s.FPS = clamp(s.FPS, MinFPS, MaxFPS)
	if s.BitrateKbps == 0 {
		s.BitrateKbps = d.BitrateKbps
	}
	s.BitrateKbps = clamp(s.BitrateKbps, MinBitrateKbps, MaxBitrateKbps)
	if s.Codec != CodecAV1 {
		s.Codec = CodecH264
	}
	if s.KeyframeSec == 0 {
		s.KeyframeSec = d.KeyframeSec
	}
	s.KeyframeSec = clamp(s.KeyframeSec, 1, 10)
	if s.AudioKbps == 0 {
		s.AudioKbps = d.AudioKbps
	}
	s.AudioKbps = clamp(s.AudioKbps, 32, 510)
	switch s.NoiseMode {
	case NoiseOff, NoiseStandard, NoiseAI:
	case "":
		s.NoiseMode = NoiseOff
		if s.NoiseSuppression {
			s.NoiseMode = NoiseStandard
		}
	default:
		s.NoiseMode = d.NoiseMode
	}
	s.NoiseSuppression = s.NoiseMode != NoiseOff
	if !validInstallID(s.InstallID) {
		s.InstallID = "" // NewService makes a fresh one
	}
	s.InstallID = strings.ToLower(s.InstallID)
	vols := map[string]int{}
	for id, v := range s.PeerVolumes {
		if v = clamp(v, 0, MaxPeerVolume); validPeerID(id) && v != 100 {
			vols[id] = v
		}
	}
	s.PeerVolumes = vols
	s.TurnURLs = cleanTurnURLs(s.TurnURLs)
	s.CredentialURL = strings.TrimSpace(s.CredentialURL)
	return s
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// settingsMu serializes stream.json load-modify-save.
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
	return filepath.Join(dir, "stream.json"), nil
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
	data, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	return parseSettings(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")))
}

// parseSettings reads stream.json over the defaults. A file from before Auto
// bitrate has no autoBitrate key: it moves to Auto unless its bitrate differs
// from the old fixed default, which means someone picked it by hand.
func parseSettings(data []byte) Settings {
	s := DefaultSettings()
	s.NoiseMode = "" // a file from before noiseMode: Normalize derives it from noiseSuppression
	_ = json.Unmarshal(data, &s)
	var probe struct {
		AutoBitrate *bool `json:"autoBitrate"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.AutoBitrate == nil {
		// 0 is what Normalize turns into the default.
		s.AutoBitrate = s.BitrateKbps == legacyDefaultKbps || s.BitrateKbps == 0
	}
	return Normalize(s)
}

func saveSettings(s Settings) error {
	settingsMu.Lock()
	defer settingsMu.Unlock()
	path, err := settingsPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
