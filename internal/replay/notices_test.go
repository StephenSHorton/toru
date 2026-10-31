package replay

import (
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestTransition(t *testing.T) {
	zero := capture.ReplayConfig{}
	replayCfg := capture.ReplayConfig{Dir: "seg", KeepSeconds: 60}
	session := capture.ReplayConfig{Dir: "rec", Persist: true, Tag: encodeTag(ModeBackground, "Hades", "hevc")}
	restarted := session
	restarted.FPS = 30
	cases := []struct {
		name           string
		from, to       capture.ReplayConfig
		started, saved bool
	}{
		{"stays off", zero, zero, false, false},
		{"unchanged", session, session, false, false},
		{"instant replay on", zero, replayCfg, true, false},
		{"session on", zero, session, true, false},
		{"session ends", session, zero, false, true},
		{"session to instant replay", session, replayCfg, false, true},
		{"instant replay off", replayCfg, zero, false, false},
		{"restart for a new config", session, restarted, false, false},
	}
	for _, c := range cases {
		started, saved := transition(c.from, c.to)
		if started != c.started || saved != c.saved {
			t.Errorf("%s: got started=%v saved=%v, want %v %v", c.name, started, saved, c.started, c.saved)
		}
	}
}

func TestStartedNote(t *testing.T) {
	s := &ReplayService{}
	s.SetHotkeyLabel(func(action string) string {
		return map[string]string{"replaySave": "Alt+F10", "recordToggle": "Ctrl+F11"}[action]
	})
	cases := []struct {
		cfg         capture.ReplayConfig
		title, body string
	}{
		{capture.ReplayConfig{KeepSeconds: 60},
			"Replay & recording is on", "Instant replay · Alt+F10 saves the last 1 min as a clip"},
		{capture.ReplayConfig{Persist: true, Tag: encodeTag(ModeManual, "", "h264")},
			"Hotkey recording started", "Press Ctrl+F11 again to stop"},
		{capture.ReplayConfig{Persist: true, Tag: encodeTag(ModeBackground, "Hades", "hevc")},
			"Recording Hades", "Background recording · saved as a Game session"},
		{capture.ReplayConfig{Persist: true, Tag: encodeTag(ModeBackground, "", "hevc")},
			"Background recording started", "Recording your screen as a Game session"},
	}
	for _, c := range cases {
		n := s.startedNote(c.cfg)
		if n.Title != c.title || n.Body != c.body || n.ID != recordingNoticeID {
			t.Errorf("startedNote(%q) = %q / %q (id %q)", c.cfg.Tag, n.Title, n.Body, n.ID)
		}
	}
	// Unbound hotkeys fall back to words.
	s.SetHotkeyLabel(func(string) string { return "" })
	if n := s.startedNote(capture.ReplayConfig{Persist: true, Tag: encodeTag(ModeManual, "", "h264")}); n.Body != "Press the record hotkey again to stop" {
		t.Errorf("unbound: %q", n.Body)
	}
}
