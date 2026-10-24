//go:build windows

package replay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// TestRealBackgroundRecordingSmoke records the real desktop in background
// mode through the service, then checks the session, plays its HLS playlist
// with ffprobe over HTTP (as the timeline player would) and cuts a clip.
// Opt-in: TORU_SMOKE=1.
func TestRealBackgroundRecordingSmoke(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1")
	}
	cfg := t.TempDir()
	t.Setenv("AppData", cfg)
	rec, clips := t.TempDir(), t.TempDir()
	s := NewService()
	st := DefaultSettings()
	st.Enabled, st.Mode, st.GamesOnly = true, ModeBackground, false
	st.FPS, st.BitrateMbps, st.RecordingsDir, st.SaveDir = 60, 10, rec, clips
	st.DiscordAudio, st.MicDevice, st.RecordingNotices = false, "", false
	if _, err := s.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	time.Sleep(4 * time.Second)
	if err := s.AddMarker(); err != nil {
		t.Fatal(err)
	}
	// Changing the storage limit mid-recording must not restart it.
	st.MaxDiskGB = 100
	if _, err := s.SetSettings(st); err != nil {
		t.Fatal(err)
	}
	time.Sleep(6 * time.Second)

	list := s.ListSessions()
	if len(list) != 1 || !list[0].Live || list[0].DurationSec < 6 || len(list[0].Markers) < 1 || len(list[0].Runs) != 1 {
		t.Fatalf("sessions: %+v", list)
	}
	t.Logf("live session: %.1fs, %d markers, %d MB", list[0].DurationSec, len(list[0].Markers), list[0].SizeBytes>>20)
	if rs := s.GetRecorderState(); !rs.Recording || rs.DiskBytes == 0 {
		t.Errorf("recorder state: %+v", rs)
	}

	st.Enabled = false
	_, _ = s.SetSettings(st)
	list = s.ListSessions()
	if len(list) != 1 || list[0].Live {
		t.Fatalf("after stop: %+v", list)
	}

	srv := httptest.NewServer(s.ClipMiddleware()(http.NotFoundHandler()))
	defer srv.Close()
	probe, _ := capture.LocateFFprobe()
	out, err := exec.Command(probe, "-v", "error", "-show_entries", "format=duration:stream=codec_name,width,height",
		"-of", "compact", srv.URL+list[0].PlaylistURL).CombinedOutput()
	if err != nil || (!strings.Contains(string(out), "hevc") && !strings.Contains(string(out), "h264")) {
		t.Fatalf("HLS playlist unplayable: %v\n%s", err, out)
	}
	t.Logf("ffprobe over HLS: %s", strings.ReplaceAll(strings.TrimSpace(string(out)), "\n", " | "))

	clip, err := s.CreateClip(list[0].ID, 2, 6)
	if err != nil {
		t.Fatal(err)
	}
	ms, err := capture.ProbeDurationMs(context.Background(), clip)
	t.Logf("clip %s: %dms", clip, ms)
	if err != nil || ms < 3500 || ms > 8500 {
		t.Errorf("clip duration %dms: %v", ms, err)
	}
	s.Shutdown()
}
