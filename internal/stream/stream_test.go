package stream

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pion/rtp"
)

func TestCodesRoundTrip(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c := NewCode()
		if len(c) != 19 || strings.Count(c, "-") != 3 {
			t.Fatalf("bad shape %q", c)
		}
		if got, ok := NormalizeCode(strings.ToLower(strings.ReplaceAll(c, "-", " "))); !ok || got != c {
			t.Fatalf("NormalizeCode(%q) = %q, %v", c, got, ok)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
	}
	for _, bad := range []string{"", "ABCD", "ABCD-EFGH-JKMN-PQR0", "ABCD-EFGH-JKMN-PQRSX", "ABCD-EFGH-JKMN-PQR!"} {
		if _, ok := NormalizeCode(bad); ok {
			t.Errorf("NormalizeCode(%q) accepted", bad)
		}
	}
}

func TestRoomSecretsDiffer(t *testing.T) {
	k1, t1, err := roomSecrets("ABCD-EFGH-JKMN-PQRS")
	if err != nil {
		t.Fatal(err)
	}
	k2, t2, _ := roomSecrets("ABCD-EFGH-JKMN-PQRT")
	if k1 == k2 || t1 == t2 {
		t.Fatal("different codes share a key or topic")
	}
	if strings.Contains(t1, "ABCD") {
		t.Fatal("topic leaks the code")
	}
}

func TestNormalizeSettings(t *testing.T) {
	s := Normalize(Settings{Width: 1921, Height: 1081, FPS: 9999, BitrateKbps: 1, Codec: "vp9", KeyframeSec: 99})
	if s.Width != 1920 || s.Height != 1080 {
		t.Errorf("size %dx%d, want even 1920x1080", s.Width, s.Height)
	}
	if s.FPS != MaxFPS || s.BitrateKbps != MinBitrateKbps || s.Codec != CodecH264 || s.KeyframeSec != 10 {
		t.Errorf("not clamped: %+v", s)
	}
	if n := Normalize(Settings{Width: 1920}); n.Width != 0 || n.Height != 0 {
		t.Errorf("half a size should mean native, got %dx%d", n.Width, n.Height)
	}
	if n := Normalize(Settings{RoomCode: "nope"}); n.RoomCode != "" {
		t.Errorf("bad room code kept: %q", n.RoomCode)
	}
}

func TestRewriterSplicesRuns(t *testing.T) {
	rw := rewriter{clock: 90000}
	now := time.Unix(100, 0)
	h := rtp.Header{SequenceNumber: 65534, Timestamp: 1000}
	rw.rewrite(&h, now)
	if h.SequenceNumber != 65534 || h.Timestamp != 1000 {
		t.Fatalf("first run must pass through, got %d/%d", h.SequenceNumber, h.Timestamp)
	}
	h = rtp.Header{SequenceNumber: 65535, Timestamp: 2500}
	rw.rewrite(&h, now.Add(16*time.Millisecond))

	// A new encoder run with unrelated numbering, half a second later.
	rw.reset()
	h = rtp.Header{SequenceNumber: 7, Timestamp: 4_000_000_000}
	rw.rewrite(&h, now.Add(516*time.Millisecond))
	if h.SequenceNumber != 0 { // 65535 + 1 wraps
		t.Errorf("seq after restart = %d, want 0", h.SequenceNumber)
	}
	if want := uint32(2500 + 45000 + 1); h.Timestamp != want {
		t.Errorf("ts after restart = %d, want %d", h.Timestamp, want)
	}
	// Gaps inside a run survive.
	h = rtp.Header{SequenceNumber: 9, Timestamp: 4_000_001_500}
	rw.rewrite(&h, now.Add(532*time.Millisecond))
	if h.SequenceNumber != 2 {
		t.Errorf("gap lost: seq %d, want 2", h.SequenceNumber)
	}
}

func TestVideoArgs(t *testing.T) {
	s := Normalize(Settings{FPS: 144, BitrateKbps: 40000, Codec: CodecAV1, Width: 1920, Height: 1080, Cursor: true})
	args := videoArgs(1, s, 2560, 1440, "av1_nvenc", false, "rtp://x")
	joined := strings.Join(args, " ")
	for _, want := range []string{
		"ddagrab=output_idx=1:framerate=144:draw_mouse=1,hwdownload,format=bgra,scale=1920:1080",
		"-c:v av1_nvenc", "-tune ull", "-preset p2", "-bf 0", "-b:v 40000k", "-g 288", "-strict experimental", "-f rtp",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if args[len(args)-1] != "rtp://x" {
		t.Errorf("url must be last: %v", args)
	}
	zc := videoArgs(0, Normalize(Settings{Codec: CodecH264}), 1920, 1080, "h264_nvenc", true, "rtp://x")
	if slices.ContainsFunc(zc, func(a string) bool { return strings.Contains(a, "hwdownload") }) {
		t.Errorf("zero-copy graph downloads frames: %v", zc)
	}
	if !slices.Contains(zc, "high") {
		t.Errorf("h264 should use the high profile: %v", zc)
	}
}

// The frontend's autoBitrateKbps (StreamPage.tsx) mirrors AutoBitrateKbps;
// keep both giving these numbers.
func TestAutoBitrateKbps(t *testing.T) {
	for _, c := range []struct {
		w, h, fps int
		codec     string
		want      int
	}{
		{1920, 1080, 60, CodecH264, 12442},
		{1920, 1080, 120, CodecH264, 24883}, // double the fps, double the rate
		{1920, 1080, 60, CodecAV1, 7465},
		{2560, 1440, 144, CodecH264, 53084},
		{1280, 720, 30, CodecH264, 2765},
		{854, 480, 30, CodecH264, MinAutoKbps},
		{3840, 2160, 240, CodecH264, MaxAutoKbps},
		{0, 0, 60, CodecH264, 12442}, // size unknown: assume 1080p
	} {
		if got := AutoBitrateKbps(c.w, c.h, c.fps, c.codec); got != c.want {
			t.Errorf("AutoBitrateKbps(%d, %d, %d, %s) = %d, want %d", c.w, c.h, c.fps, c.codec, got, c.want)
		}
	}
	s := DefaultSettings()
	if !s.AutoBitrate {
		t.Fatal("new installs should default to Auto bitrate")
	}
	if got := s.VideoKbps(1920, 1080); got != 12442 {
		t.Errorf("auto VideoKbps = %d, want 12442", got)
	}
	s.AutoBitrate, s.BitrateKbps = false, 33000
	if got := s.VideoKbps(1920, 1080); got != 33000 {
		t.Errorf("manual VideoKbps = %d, want 33000", got)
	}
}

func TestAutoBitrateArgs(t *testing.T) {
	s := DefaultSettings() // native size, 60 fps, H.264, Auto
	joined := strings.Join(videoArgs(0, s, 1920, 1080, "h264_nvenc", true, "rtp://x"), " ")
	if !strings.Contains(joined, "-b:v 12442k -maxrate 12442k -bufsize 3110k") {
		t.Errorf("native auto rate wrong: %s", joined)
	}
	s.Width, s.Height, s.FPS = 1280, 720, 120
	joined = strings.Join(videoArgs(0, s, 1920, 1080, "h264_nvenc", false, "rtp://x"), " ")
	if !strings.Contains(joined, "-b:v 11059k") {
		t.Errorf("scaled auto rate should follow the output size: %s", joined)
	}
}

func TestSettingsMigration(t *testing.T) {
	for _, c := range []struct {
		name     string
		json     string
		wantAuto bool
		wantKbps int
	}{
		{"old file at the old default moves to Auto", `{"fps":144,"bitrateKbps":20000}`, true, 20000},
		{"old file with a hand-picked rate stays manual", `{"fps":60,"bitrateKbps":35000}`, false, 35000},
		{"old file without a bitrate moves to Auto", `{"fps":60}`, true, 20000},
		{"old file with a zero bitrate moves to Auto", `{"fps":60,"bitrateKbps":0}`, true, 20000},
		{"new file keeps manual", `{"autoBitrate":false,"bitrateKbps":20000}`, false, 20000},
		{"new file keeps Auto", `{"autoBitrate":true,"bitrateKbps":35000}`, true, 35000},
		{"garbage gives the defaults", `not json`, true, 20000},
	} {
		s := parseSettings([]byte(c.json))
		if s.AutoBitrate != c.wantAuto || s.BitrateKbps != c.wantKbps {
			t.Errorf("%s: auto=%v kbps=%d, want auto=%v kbps=%d", c.name, s.AutoBitrate, s.BitrateKbps, c.wantAuto, c.wantKbps)
		}
	}
}

func TestSameEncode(t *testing.T) {
	auto := DefaultSettings() // Auto, native, 60 fps, H.264
	manual := auto
	manual.AutoBitrate, manual.BitrateKbps = false, AutoBitrateKbps(1920, 1080, 60, CodecH264)
	if !sameEncode(auto, manual, 1920, 1080) {
		t.Error("Auto -> Manual at the same rate should not restart the encoder")
	}
	manual.BitrateKbps += 1000
	if sameEncode(auto, manual, 1920, 1080) {
		t.Error("a new manual rate must restart the encoder")
	}
	faster := auto
	faster.FPS = 120
	if sameEncode(auto, faster, 1920, 1080) {
		t.Error("a new frame rate must restart the encoder")
	}
	ignored := auto
	ignored.BitrateKbps = 35000 // not used while Auto is on
	if !sameEncode(auto, ignored, 1920, 1080) {
		t.Error("the manual value is unused in Auto")
	}
}
