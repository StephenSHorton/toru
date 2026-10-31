package vid

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
)

func TestParseProbe(t *testing.T) {
	// A separate-tracks clip as Toru writes it: mix first and default, then
	// titled source tracks; plus a stray cover-art video stream.
	raw := `{"streams":[
		{"index":0,"codec_type":"video","width":2560,"height":1440,"color_space":"bt470bg","avg_frame_rate":"144/1","r_frame_rate":"144/1","duration":"12.5"},
		{"index":1,"codec_type":"audio","disposition":{"default":1},"tags":{"title":"All audio","handler_name":"All audio"}},
		{"index":2,"codec_type":"audio","disposition":{"default":0},"tags":{"handler_name":"Game & desktop"}},
		{"index":3,"codec_type":"audio","disposition":{"default":0},"tags":{"name":" Microphone ","handler_name":"SoundHandler"}},
		{"index":4,"codec_type":"video","width":320,"height":320,"avg_frame_rate":"0/0","r_frame_rate":"90000/1"}
	],"format":{"duration":"12.533"}}`
	got, err := parseProbe([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Width != 2560 || got.Height != 1440 || got.FPS != 144 || got.DurationMs != 12533 || !got.HasAudio || got.ColorSpace != "bt470bg" {
		t.Fatalf("info = %+v", got)
	}
	want := []AudioTrack{
		{Index: 0, Title: "All audio", IsMix: true},
		{Index: 1, Title: "Game & desktop"},
		{Index: 2, Title: "Microphone"},
	}
	if len(got.Tracks) != len(want) {
		t.Fatalf("tracks = %+v", got.Tracks)
	}
	for i := range want {
		if got.Tracks[i] != want[i] {
			t.Errorf("track %d = %+v, want %+v", i, got.Tracks[i], want[i])
		}
	}
}

func TestParseProbeSingleTrackAndFallbacks(t *testing.T) {
	// One generic-named track is just the audio; NTSC rate; duration only on
	// the stream.
	raw := `{"streams":[
		{"codec_type":"video","width":1920,"height":1080,"avg_frame_rate":"30000/1001","duration":"3.0"},
		{"codec_type":"audio","disposition":{"default":1},"tags":{"handler_name":"SoundHandler"}}
	],"format":{}}`
	got, err := parseProbe([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.DurationMs != 3000 || got.FPS < 29.96 || got.FPS > 29.98 {
		t.Fatalf("info = %+v", got)
	}
	if len(got.Tracks) != 1 || got.Tracks[0] != (AudioTrack{Index: 0}) {
		t.Fatalf("tracks = %+v", got.Tracks)
	}

	// No default disposition among several tracks: the first is the mix.
	raw = `{"streams":[{"codec_type":"video","width":8,"height":8,"avg_frame_rate":"60/1"},
		{"codec_type":"audio"},{"codec_type":"audio"}],"format":{"duration":"1"}}`
	if got, err = parseProbe([]byte(raw)); err != nil || !got.Tracks[0].IsMix || got.Tracks[1].IsMix {
		t.Fatalf("no-default mix: %+v, %v", got.Tracks, err)
	}

	// Silent video.
	raw = `{"streams":[{"codec_type":"video","width":8,"height":8,"avg_frame_rate":"60/1"}],"format":{"duration":"1"}}`
	if got, err = parseProbe([]byte(raw)); err != nil || got.HasAudio || len(got.Tracks) != 0 {
		t.Fatalf("silent: %+v, %v", got, err)
	}

	if _, err := parseProbe([]byte(`{"streams":[{"codec_type":"audio"}],"format":{"duration":"1"}}`)); err == nil {
		t.Fatal("audio-only file: want an error")
	}
}

func TestParseRate(t *testing.T) {
	for in, want := range map[string]float64{"60/1": 60, "0/0": 0, "": 0, "25": 25, "x/1": 0} {
		if got := parseRate(in); got != want {
			t.Errorf("parseRate(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEditedName(t *testing.T) {
	for in, want := range map[string]string{
		`C:\Users\me\Videos\Toru Replays\The Finals 2026-10-28.mp4`: "The Finals 2026-10-28-edited.mp4",
		`D:\rec\clip.webm`: "clip-edited.mp4",
		"":                 "video-edited.mp4",
	} {
		if got := editedName(in); got != want {
			t.Errorf("editedName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAudioName(t *testing.T) {
	for in, want := range map[string]string{
		`D:\rec\clip.mp4`: "clip-audio.m4a",
		"":                "audio.m4a",
	} {
		if got := audioName(in); got != want {
			t.Errorf("audioName(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		`D:\a\x.wav`: `D:\a\x.wav`,
		`D:\a\x.M4A`: `D:\a\x.M4A`,
		`D:\a\x`:     `D:\a\x.m4a`,
		`D:\a\x.mp3`: `D:\a\x.mp3.m4a`,
	} {
		if got := audioPath(in); got != want {
			t.Errorf("audioPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPartPath(t *testing.T) {
	out := filepath.Join("C:", "clips", "a-edited.mp4")
	p := partPath(out)
	if filepath.Dir(p) != filepath.Dir(out) || filepath.Ext(p) != ".mp4" || p == out {
		t.Fatalf("partPath(%q) = %q", out, p)
	}
}

func TestEditTextFilter(t *testing.T) {
	f, cleanup, err := editTextFilter(EditRequest{}, nil)
	if f != "" || err != nil {
		t.Fatalf("no texts: %q, %v", f, err)
	}
	cleanup() // a no-op, but always safe to call

	if _, err := os.Stat(DefaultTextFont); err != nil {
		t.Skipf("no Segoe UI Semibold: %v", err)
	}
	req := EditRequest{
		Texts: []TextOverlay{{Text: "hi", X: 0.5, Y: 0.5, Size: "m", StartMs: 0, EndMs: 1000}},
		Crop:  &capture.Rect{W: 1001, H: 501},
	}
	f, cleanup, err = editTextFilter(req, []MediaInfo{{Width: 1920, Height: 1080}})
	if err != nil {
		t.Fatal(err)
	}
	// Laid out on the crop (1000x500): centre 500,250, M = 6% of 500.
	for _, want := range []string{"fontsize=30:", "x=500-text_w/2", "y=250-text_h/2"} {
		if !strings.Contains(f, want) {
			t.Errorf("filter lacks %q: %s", want, f)
		}
	}
	dir := textDirOf(t, f)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("text dir: %v", err)
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("text dir %s still there after cleanup: %v", dir, err)
	}

	if _, _, err := editTextFilter(EditRequest{Texts: []TextOverlay{{Text: "x", Size: "xl", EndMs: 1}}}, []MediaInfo{{Width: 64, Height: 64}}); err == nil {
		t.Error("bad size accepted")
	}
}

// textDirOf is the temp dir of the first textfile= in a filter.
func textDirOf(t *testing.T, filter string) string {
	t.Helper()
	_, rest, ok := strings.Cut(filter, "textfile=")
	if !ok {
		t.Fatalf("no textfile in %s", filter)
	}
	path, _, _ := strings.Cut(rest, ":expansion")
	// The path is forward-slashed with only escape backslashes (C\\:/...).
	return filepath.Dir(filepath.FromSlash(strings.ReplaceAll(path, `\`, "")))
}

// TestEditServiceSmoke probes a generated separate-tracks clip, exports it
// with progress, and cancels a longer export, through real ffmpeg. It needs
// ffmpeg and ffprobe with lavfi and libx264; otherwise it skips.
//
//	go test ./internal/vid -run EditServiceSmoke -v
func TestEditServiceSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("encodes video; skipped with -short")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH: %v", bin, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	dir := t.TempDir()
	src := filepath.Join(dir, "clip.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=60:duration=3",
		"-f", "lavfi", "-i", "sine=f=440:duration=3",
		"-f", "lavfi", "-i", "sine=f=3000:duration=3",
		"-filter_complex", "[1:a][2:a]amix=inputs=2:normalize=0[mix]",
		"-map", "0:v", "-map", "[mix]", "-map", "1:a", "-map", "2:a",
		"-metadata:s:a:0", "title=All audio", "-metadata:s:a:1", "title=Game & desktop", "-metadata:s:a:2", "title=Microphone",
		"-disposition:a:0", "default", "-disposition:a:1", "0", "-disposition:a:2", "0",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", src,
	}); err != nil {
		t.Skipf("generate source (needs lavfi + libx264): %v", err)
	}

	s := New()
	info, err := s.ProbeMedia(src)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 640 || info.Height != 360 || info.FPS != 60 || info.DurationMs < 2900 || info.DurationMs > 3100 {
		t.Fatalf("probe = %+v", info)
	}
	titles := []string{}
	for _, tr := range info.Tracks {
		titles = append(titles, tr.Title)
	}
	if strings.Join(titles, "|") != "All audio|Game & desktop|Microphone" || !info.Tracks[0].IsMix || info.Tracks[1].IsMix {
		t.Fatalf("tracks = %+v", info.Tracks)
	}

	// Export: progress climbs to 1 and only the output is left behind (a
	// text overlay too when the font is there; its temp files go as well).
	var texts []TextOverlay
	if _, err := os.Stat(DefaultTextFont); err == nil {
		texts = []TextOverlay{{Text: "It's 5:00", X: 0.5, Y: 0.5, Size: "l", StartMs: 0, EndMs: 2000}}
	}
	textDirs := func() []string {
		m, _ := filepath.Glob(filepath.Join(os.TempDir(), "toru-edittext-*"))
		return m
	}
	before := len(textDirs())
	out := filepath.Join(dir, "out", "clip-edited.mp4")
	var progress []float64
	got, err := s.export(ctx, EditRequest{
		Sources:  []string{src},
		Segments: []Segment{{Source: 0, StartMs: 0, EndMs: 1000}, {Source: 0, StartMs: 2000, EndMs: 3000}},
		Tracks:   []TrackLevel{{Source: 0, Index: 1, Volume: 100}, {Source: 0, Index: 2, Muted: true}},
		Texts:    texts,
		Preset:   "720p",
		OutPath:  out,
	}, func(p float64) { progress = append(progress, p) })
	if err != nil {
		t.Fatal(err)
	}
	if got != out {
		t.Fatalf("export wrote %q, want %q", got, out)
	}
	if len(progress) < 2 || progress[0] != 0 || progress[len(progress)-1] != 1 {
		t.Fatalf("progress = %v", progress)
	}
	for i := 1; i < len(progress); i++ {
		if progress[i] < progress[i-1] {
			t.Fatalf("progress went backwards: %v", progress)
		}
	}
	if res, err := s.ProbeMedia(out); err != nil || res.DurationMs < 1900 || res.DurationMs > 2150 {
		t.Fatalf("exported %+v, %v", res, err)
	}
	assertOnly(t, filepath.Dir(out), "clip-edited.mp4")
	if after := len(textDirs()); after > before {
		t.Errorf("export left %d text temp dirs behind", after-before)
	}

	// Cancel: a long export stops, returns ErrExportCancelled and leaves
	// nothing in the output folder.
	long := filepath.Join(dir, "long.mp4")
	if err := capture.RunFFmpeg(ctx, []string{"-y",
		"-f", "lavfi", "-i", "testsrc=size=1280x720:rate=60:duration=60",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", long,
	}); err != nil {
		t.Fatalf("generate long source: %v", err)
	}
	cancelDir := filepath.Join(dir, "cancelled")
	done := make(chan error, 1)
	go func() {
		_, err := s.Export(EditRequest{
			Sources:  []string{long},
			Segments: []Segment{{Source: 0, StartMs: 0, EndMs: 60000}},
			Preset:   "original",
			OutPath:  filepath.Join(cancelDir, "long-edited.mp4"),
		})
		done <- err
	}()
	// Let ffmpeg get going (the part file appears) before cancelling.
	deadline := time.Now().Add(30 * time.Second)
	for {
		entries, _ := os.ReadDir(cancelDir)
		if len(entries) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := s.CancelExport(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrExportCancelled) {
			t.Fatalf("cancelled export returned %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("export didn't stop after CancelExport")
	}
	assertOnly(t, cancelDir)
}

// assertOnly fails unless dir holds exactly the named files.
func assertOnly(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	if strings.Join(got, "|") != strings.Join(names, "|") {
		t.Fatalf("%s holds %v, want %v", dir, got, names)
	}
}

func TestParseProbeUntaggedColorSpace(t *testing.T) {
	for _, cs := range []string{`"color_space":"unknown",`, ``} {
		got, err := parseProbe([]byte(`{"streams":[{"index":0,"codec_type":"video",` + cs + `"width":1920,"height":1080,"avg_frame_rate":"60/1"}],"format":{"duration":"1"}}`))
		if err != nil || got.ColorSpace != "" {
			t.Fatalf("%q: ColorSpace = %q, err %v (want untagged = \"\")", cs, got.ColorSpace, err)
		}
	}
}
