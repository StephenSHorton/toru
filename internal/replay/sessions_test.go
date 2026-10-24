package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

// makeRun writes a real 2s-segmented recording run like the engine does:
// segments, ffmpeg's csv segment list, and the sidecar.
func makeRun(t *testing.T, dir string, run int, startMs int64, secs int, tag string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	err := capture.RunFFmpeg(ctx, []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=s=320x180:r=30", "-f", "lavfi", "-i", "sine",
		"-t", fmt.Sprint(secs), "-c:v", "mpeg2video", "-g", "60", "-force_key_frames", "expr:gte(t,n_forced*2)",
		"-c:a", "aac", "-f", "segment", "-segment_time", "2", "-segment_format", "mpegts",
		"-segment_list", filepath.Join(dir, fmt.Sprintf("r%04d.csv", run)), "-segment_list_type", "csv",
		filepath.Join(dir, fmt.Sprintf("r%04d-%%08d.ts", run))})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(capture.RunInfo{Run: run, StartMs: startMs, Tag: tag})
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("r%04d.json", run)), raw, 0o644)
}

func TestSessionsTimelineClipKeepDelete(t *testing.T) {
	if _, err := capture.LocateFFmpeg(); err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	cfg := t.TempDir()
	t.Setenv("AppData", cfg)
	t.Setenv("LocalAppData", cfg)
	rec, clips := t.TempDir(), t.TempDir()
	s := &ReplayService{buf: capture.NewReplayBuffer(), settings: Settings{
		Mode: ModeBackground, BackgroundMinutes: 120, RecordingsDir: rec, SaveDir: clips, GameFolders: true,
	}}

	base := time.Now().Add(-time.Hour).UnixMilli()
	// Two runs of the same game 2s apart = one session; a manual run later = another.
	makeRun(t, rec, 1000, base, 6, encodeTag(ModeBackground, "VALORANT"))
	makeRun(t, rec, 1008, base+8000, 4, encodeTag(ModeBackground, "VALORANT"))
	makeRun(t, rec, 2000, base+600_000, 4, encodeTag(ModeManual, ""))
	s.addMarkerAt(base+9000, MarkerUser, "")        // 1s into the second run
	s.addMarkerAt(base+10*3600_000, MarkerUser, "") // outside every session

	list := s.ListSessions()
	if len(list) != 2 {
		t.Fatalf("sessions = %+v", list)
	}
	manual, val := list[0], list[1]
	if val.Game != "VALORANT" || len(val.Runs) != 2 || val.Mode != ModeBackground || manual.Mode != ModeManual {
		t.Fatalf("grouping wrong: %+v / %+v", val, manual)
	}
	if val.DurationSec < 9.5 || val.DurationSec > 10.5 {
		t.Errorf("duration = %.2f, want ~10", val.DurationSec)
	}
	if len(val.Markers) != 1 || val.Markers[0].OffsetSec < 6.5 || val.Markers[0].OffsetSec > 7.5 {
		t.Errorf("marker should sit ~7s in (6s first run + 1s): %+v", val.Markers)
	}
	if val.DeleteAtMs == 0 || manual.DeleteAtMs != 0 {
		t.Errorf("background sessions expire, manual ones don't: %d / %d", val.DeleteAtMs, manual.DeleteAtMs)
	}
	if !s.isProtected(2000) || s.isProtected(1000) {
		t.Error("manual runs are protected from pruning, background runs are not")
	}

	// Playlist: one discontinuity between the runs, VOD, segment URLs.
	h := s.ClipMiddleware()(http.NotFoundHandler())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", val.PlaylistURL, nil))
	pl := res.Body.String()
	if res.Code != 200 || strings.Count(pl, "#EXT-X-DISCONTINUITY") != 1 || !strings.Contains(pl, "#EXT-X-ENDLIST") ||
		strings.Count(pl, "/__rec/seg/r") != 5 {
		t.Fatalf("playlist (%d):\n%s", res.Code, pl)
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", "/__rec/seg/r1000-00000000.ts", nil))
	if res.Code != 200 || res.Header().Get("Content-Type") != "video/mp2t" {
		t.Errorf("segment: %d %q", res.Code, res.Header().Get("Content-Type"))
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", "/__rec/seg/..%2Fkept.json", nil))
	if res.Code != 404 {
		t.Errorf("traversal: %d", res.Code)
	}

	// Clip across the run boundary.
	out, err := s.CreateClip(val.ID, 5, 7)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(filepath.Dir(out)) != "VALORANT" {
		t.Errorf("clip should land in the game folder: %s", out)
	}
	ms, err := capture.ProbeDurationMs(context.Background(), out)
	if err != nil || ms < 3500 || ms > 4500 {
		t.Errorf("clip duration %dms (segments 2-3 = 4s): %v", ms, err)
	}

	if err := s.KeepSession(val.ID, true); err != nil {
		t.Fatal(err)
	}
	if !s.isProtected(1008) || !s.ListSessions()[1].Kept || s.ListSessions()[1].DeleteAtMs != 0 {
		t.Error("kept session should be protected and not expire")
	}
	if err := s.DeleteSession(val.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.ListSessions(); len(got) != 1 || got[0].ID != 2000 {
		t.Errorf("after delete: %+v", got)
	}
	if s.isProtected(1008) {
		t.Error("deleted runs drop out of kept")
	}
}

func TestModesNormalizeAndConfig(t *testing.T) {
	st := Settings{Mode: ModeBackground, Codec: "av1", BackgroundMinutes: 7, MaxDiskGB: 3, FPS: 144, BitrateMbps: 65}.Normalize()
	if st.Codec != "h264" || st.BackgroundMinutes != 120 || st.MaxDiskGB != 25 || st.RecordingQuality != QualityStandard {
		t.Errorf("normalize: %+v", st)
	}
	if (Settings{Mode: "steam"}).Normalize().Mode != ModeReplay {
		t.Error("unknown mode falls back to instant replay")
	}
	st.RecordingsDir = `D:\Rec`
	bg := toConfigFor(st, "VALORANT")
	if !bg.Persist || bg.Dir != `D:\Rec` || bg.KeepSeconds != 7200 || bg.MaxBytes != 25<<30 || bg.Tag != "background|VALORANT" ||
		bg.BitrateKbps != 12000 || bg.MinFreeBytes != minFreeBytes {
		t.Errorf("background cfg: %+v", bg)
	}
	if toConfigFor(st, "VALORANT") == toConfigFor(st, "Apex Legends") {
		t.Error("a different game must restart the buffer (new session)")
	}
	if bg.FPS != 60 {
		t.Errorf("standard quality caps fps at 60, got %d", bg.FPS)
	}
	st.RecordingQuality = QualityCustom
	if c := toConfigFor(st, ""); c.FPS != 144 || c.BitrateKbps != 65000 {
		t.Errorf("custom quality uses the Video settings: %+v", c)
	}
	st.Mode = ModeManual
	if m := toConfigFor(st, ""); m.KeepSeconds != 0 || m.Tag != "manual|" {
		t.Errorf("manual cfg: %+v", m)
	}
	if r := toConfig(Settings{Mode: ModeReplay, FPS: 144, BitrateMbps: 65}.Normalize()); r.FPS != 144 || r.BitrateKbps != 65000 {
		t.Errorf("instant replay keeps its own quality: %+v", r)
	}
	if r := toConfig(DefaultSettings()); r.Persist {
		t.Error("instant replay must not persist")
	}
}
