package replay

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestNormalizeClampsEverything(t *testing.T) {
	got := Settings{DurationSec: 99999, FPS: 75, Height: 900, BitrateMbps: 0, Codec: "vp8", MicDevice: "  Mic  "}.Normalize()
	if got.DurationSec != maxDurationSec || got.FPS != 60 || got.Height != 0 ||
		got.BitrateMbps != minBitrateMbps || got.Codec != capture.ReplayCodecH264 || got.MicDevice != "Mic" {
		t.Errorf("Normalize = %+v", got)
	}
	loud := Settings{DesktopVolume: 900, MicVolume: -5, AudioBitrateKbps: 17}.Normalize()
	if loud.DesktopVolume != maxVolume || loud.MicVolume != 0 || loud.AudioBitrateKbps != 160 {
		t.Errorf("audio Normalize = %+v", loud)
	}
	if d := DefaultSettings(); d.Normalize() != d {
		t.Errorf("defaults must already be normal: %+v", d)
	}
	if DefaultSettings().Enabled {
		t.Error("instant replay must be OFF by default (it records the screen in the background)")
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("AppData", cfg)
	t.Setenv("XDG_CONFIG_HOME", cfg)
	t.Setenv("HOME", cfg)

	if got := loadSettings(); got != DefaultSettings() {
		t.Fatalf("missing file should load defaults, got %+v", got)
	}
	want := DefaultSettings()
	want.Enabled, want.DurationSec, want.Codec, want.MicDevice = true, 300, capture.ReplayCodecAV1, "Mic (USB)"
	if err := saveSettings(want); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(); got != want {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}

	// A partial hand-written file keeps defaults for missing fields.
	path, _ := settingsPath()
	if err := os.WriteFile(path, []byte(`{"enabled":true,"fps":30}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := loadSettings()
	if !got.Enabled || got.FPS != 30 || got.DurationSec != 60 || !got.DesktopAudio {
		t.Errorf("partial file = %+v", got)
	}
	// A BOM (Notepad, PowerShell 5) must not reset the settings to defaults.
	if err := os.WriteFile(path, []byte("\uFEFF{\"enabled\":true}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !loadSettings().Enabled {
		t.Error("a UTF-8 BOM made the settings file unreadable")
	}
	if filepath.Base(path) != "replay.json" {
		t.Errorf("settings path = %s", path)
	}
}

func TestRestartOnlyForCaptureFields(t *testing.T) {
	a := DefaultSettings()
	b := a
	b.SaveDir, b.SoundOnSave = `D:\Clips`, false
	if toConfig(a) != toConfig(b) {
		t.Error("save folder / sound changes must not restart the buffer")
	}
	b.BitrateMbps = 40
	if toConfig(a) == toConfig(b) {
		t.Error("a bitrate change must restart the buffer")
	}
	c := a
	c.MicDevice, c.MicVolume, c.DiscordVolume = "Mic", 0, 0
	if cfg := toConfig(c); cfg.Audio.MicDevice != "" || cfg.Audio.Discord || !cfg.Audio.Desktop {
		t.Errorf("0%% volume must switch that source off: %+v", cfg.Audio)
	}
}

func TestUniquePath(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "Replay")
	if p := uniquePath(base, ".mp4"); p != base+".mp4" {
		t.Fatalf("first = %s", p)
	}
	_ = os.WriteFile(base+".mp4", nil, 0o644)
	if p := uniquePath(base, ".mp4"); p != base+" (2).mp4" {
		t.Errorf("second = %s", p)
	}
}

func TestSaveSoundsAreValidWAV(t *testing.T) {
	for name, wav := range map[string][]byte{"saved": savedSound, "failed": failedSound} {
		if len(wav) < 1000 || string(wav[:4]) != "RIFF" || string(wav[8:16]) != "WAVEfmt " ||
			string(wav[36:40]) != "data" || int(binary.LittleEndian.Uint32(wav[40:44])) != len(wav)-44 ||
			binary.LittleEndian.Uint32(wav[24:28]) != 44100 {
			t.Errorf("%s sound is not a PCM WAV (%d bytes)", name, len(wav))
		}
	}
	if got := formatClipLength(90); got != "1:30" {
		t.Errorf("formatClipLength(90) = %q", got)
	}
}
