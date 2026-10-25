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
	// Like the recorder: each segment file's mtime is when that segment ended.
	for name, t := range capture.SegmentTimes(dir, run) {
		end := time.UnixMilli(startMs + int64(t[1]*1000))
		_ = os.Chtimes(filepath.Join(dir, name), end, end)
	}
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
		Mode: ModeBackground, RecordingsDir: rec, SaveDir: clips, GameFolders: true,
	}}

	base := time.Now().Add(-time.Hour).UnixMilli()
	// Two runs of the same game 2s apart = one session; a manual run later = another.
	makeRun(t, rec, 1000, base, 6, encodeTag(ModeBackground, "VALORANT", "h264"))
	makeRun(t, rec, 1008, base+8000, 4, encodeTag(ModeBackground, "VALORANT", "h264"))
	makeRun(t, rec, 2000, base+600_000, 4, encodeTag(ModeManual, "", "h264"))
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
	h.ServeHTTP(res, httptest.NewRequest("GET", "/__rec/seg/..%2Fmarkers.jsonl", nil))
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

	// The footage budget trims a session from the front. The playlist's
	// MEDIA-SEQUENCE follows the first surviving segment, so the player keeps
	// its timeline anchored, and offsets move down by the removed footage.
	if !strings.Contains(pl, "#EXT-X-MEDIA-SEQUENCE:0\n") {
		t.Errorf("untrimmed playlist should start at sequence 0:\n%s", pl)
	}
	if err := os.Remove(filepath.Join(rec, "r1000-00000000.ts")); err != nil {
		t.Fatal(err)
	}
	res = httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest("GET", val.PlaylistURL, nil))
	if pl = res.Body.String(); !strings.Contains(pl, "#EXT-X-MEDIA-SEQUENCE:1\n") || strings.Count(pl, "/__rec/seg/r") != 4 {
		t.Errorf("after trimming one segment, sequence 1 with 4 segments:\n%s", pl)
	}
	trimmed, err := s.GetSession(val.ID)
	if err != nil || trimmed.DurationSec > val.DurationSec-1.5 || trimmed.StartMs <= val.StartMs {
		t.Errorf("trimmed session: %+v (was %.2fs from %d): %v", trimmed, val.DurationSec, val.StartMs, err)
	}

	// A session answers to any of its runs, not only the first.
	if byLater, err := s.GetSession(1008); err != nil || byLater.ID != 1000 || len(byLater.Runs) != 2 {
		t.Errorf("session should resolve by a later run too: %+v, %v", byLater, err)
	}
	// Once the whole first run is gone, the next run is the session: the old
	// ID is in no session any more (the viewer falls back to the runs it last
	// saw), and the new one carries the remaining footage.
	if err := capture.DeleteRun(rec, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSession(1000); err == nil {
		t.Error("a fully deleted first run is no session")
	}
	rest, err := s.GetSession(1008)
	if err != nil || rest.ID != 1008 || len(rest.Runs) != 1 || rest.DurationSec < 3.5 {
		t.Fatalf("remaining run should be the session: %+v, %v", rest, err)
	}

	if err := s.DeleteSession(1008); err != nil {
		t.Fatal(err)
	}
	if got := s.ListSessions(); len(got) != 1 || got[0].ID != 2000 {
		t.Errorf("after delete: %+v", got)
	}
}

func TestModesNormalizeAndConfig(t *testing.T) {
	hevcAvailable = func() bool { return true }
	defer func() { hevcAvailable = func() bool { return capture.ReplayEncoderAvailable(capture.ReplayCodecHEVC) } }()
	st := Settings{Mode: ModeBackground, Codec: "av1", MaxDiskGB: 3, FPS: 144, BitrateMbps: 65}.Normalize()
	if st.Codec != "h264" || st.MaxDiskGB != 25 || st.RecordingQuality != QualityStandard {
		t.Errorf("normalize: %+v", st)
	}
	if (Settings{Mode: "steam"}).Normalize().Mode != ModeReplay {
		t.Error("unknown mode falls back to instant replay")
	}
	st.RecordingsDir = `D:\Rec`
	if whole := toConfigFor(st, "VALORANT"); !whole.WholeSessions || whole.KeepSeconds != 0 {
		t.Errorf("background keeps whole sessions until the limit: %+v", whole)
	}
	bg := toConfigFor(st, "VALORANT")
	if !bg.Persist || bg.Dir != `D:\Rec` || bg.KeepSeconds != 0 || !bg.WholeSessions || bg.MaxBytes != 25<<30 || bg.Tag != "background|VALORANT|hevc" ||
		bg.BitrateKbps != 8000 || bg.Codec != "hevc" || bg.MinFreeBytes != minFreeBytes {
		t.Errorf("background cfg: %+v", bg)
	}
	hevcAvailable = func() bool { return false }
	if fb := toConfigFor(st, "VALORANT"); fb.Codec != "h264" || fb.BitrateKbps != 12000 {
		t.Errorf("without HEVC, Standard falls back to H.264 at 12 Mbps: %+v", fb)
	}
	hevcAvailable = func() bool { return true }
	if toConfigFor(st, "VALORANT") == toConfigFor(st, "Apex Legends") {
		t.Error("a different game must restart the buffer (new session)")
	}
	if bg.FPS != 60 {
		t.Errorf("standard quality caps fps at 60, got %d", bg.FPS)
	}
	st.RecordingQuality, st.RecordingFPS, st.RecordingBitrateMbps = QualityCustom, 120, 30
	if c := toConfigFor(st, ""); c.FPS != 120 || c.BitrateKbps != 30000 {
		t.Errorf("custom recording quality uses its own fps/bitrate: %+v", c)
	}
	st.Mode = ModeManual
	if m := toConfigFor(st, ""); m.KeepSeconds != 0 || m.Tag != "manual||h264" {
		t.Errorf("manual cfg: %+v", m)
	}
	if r := toConfig(Settings{Mode: ModeReplay, FPS: 144, BitrateMbps: 65}.Normalize()); r.FPS != 144 || r.BitrateKbps != 65000 {
		t.Errorf("instant replay keeps its own quality: %+v", r)
	}
	if r := toConfig(DefaultSettings()); r.Persist {
		t.Error("instant replay must not persist")
	}
}
