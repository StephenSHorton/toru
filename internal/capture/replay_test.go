package capture

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayArgsZeroCopyNative(t *testing.T) {
	cfg := ReplayConfig{FPS: 60, BitrateKbps: 20000, Codec: ReplayCodecH264, IncludeCursor: true}
	args := BuildReplayArgs(oneSource(1), cfg, "h264_nvenc", true, `C:\seg\r0001-%08d.ts`, nil)
	got := joined(args)

	for _, want := range []string{
		"-filter_complex ddagrab=output_idx=1:framerate=60:draw_mouse=1 ",
		"-c:v h264_nvenc -preset p4 -tune hq -rc cbr",
		"-b:v 20000k -maxrate 20000k -bufsize 40000k",
		"-g 120 -force_key_frames expr:gte(t,n_forced*2)",
		"-f segment -segment_time 2 -segment_format mpegts",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "hwdownload") {
		t.Errorf("zero-copy graph must not download frames to system memory:\n%s", got)
	}
	if args[len(args)-1] != `C:\seg\r0001-%08d.ts` {
		t.Errorf("output pattern must be the last arg, got %q", args[len(args)-1])
	}
}

func TestReplayArgsDownscaleAndAV1(t *testing.T) {
	cfg := ReplayConfig{FPS: 30, Height: 1080, BitrateKbps: 8000, Codec: ReplayCodecAV1}
	got := joined(BuildReplayArgs(oneSource(0), cfg, "av1_qsv", false, "seg-%08d.mkv", nil))
	for _, want := range []string{
		"ddagrab=output_idx=0:framerate=30:draw_mouse=0,hwdownload,format=bgra,scale=-2:1080:flags=bilinear,format=nv12",
		"-g 60",
		"-segment_format matroska -reset_timestamps 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\n%s", want, got)
		}
	}
}

func TestReplayGraphCandidates(t *testing.T) {
	native := ReplayConfig{}
	scaled := ReplayConfig{Height: 1080}
	if c := replayGraphCandidates(native, "hevc_nvenc", 1); len(c) != 2 || !c[0] {
		t.Errorf("native NVENC should try zero-copy first, got %v", c)
	}
	if c := replayGraphCandidates(native, "h264_qsv", 1); len(c) != 1 || c[0] {
		t.Errorf("QSV can't take D3D11 frames from ddagrab, got %v", c)
	}
	if c := replayGraphCandidates(scaled, "h264_nvenc", 1); len(c) != 1 || c[0] {
		t.Errorf("downscale needs the CPU graph, got %v", c)
	}
}

func TestReplayAudioMixIsAAC(t *testing.T) {
	cfg := ReplayConfig{FPS: 60, BitrateKbps: 20000, Codec: ReplayCodecH264,
		Audio: ReplayAudio{Desktop: true, MicDevice: "Mic (USB)", MicVolume: 150}}
	srcs := replayAudioSources(cfg.Audio)
	srcs[0].in = &AudioInput{PipePath: `\\.\pipe\a`, SampleFmt: "f32le", SampleRate: 48000, Channels: 2}
	args := BuildReplayArgs(oneSource(0), cfg, "h264_nvenc", true, "seg-%08d.ts", srcs)
	got := joined(args)
	for _, want := range []string{
		"-y -f f32le -ar 48000 -ac 2 -i \\\\.\\pipe\\a -f dshow -i audio=Mic (USB) -filter_complex ddagrab=",
		"draw_mouse=0[v];[0:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo[m0];" +
			"[1:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,volume=1.50[m1];" +
			"[m0][m1]amix=inputs=2:duration=longest:normalize=0[aout] -map [v] -map [aout] -c:v",
		"-c:a aac -b:a 160k -f segment",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\n%s", want, got)
		}
	}
	if args[len(args)-1] != "seg-%08d.ts" {
		t.Errorf("output pattern must be the last arg, got %q", args[len(args)-1])
	}
}

func TestReplayAudioSeparateTracks(t *testing.T) {
	a := ReplayAudio{Desktop: true, Discord: true, MicDevice: "Mic", SeparateTracks: true, DiscordVolume: 80, BitrateKbps: 256}
	srcs := replayAudioSources(a)
	srcs[0].in = &AudioInput{PipePath: "p0", SampleFmt: "f32le", SampleRate: 48000, Channels: 2}
	// srcs[1] (Discord) stays nil: Discord isn't running, so it's silence.
	inputs, graph, maps := replayAudioArgs(srcs, a)

	if got := joined(inputs); !strings.Contains(got, "-i p0 -re -f lavfi -i anullsrc=r=48000:cl=stereo -f dshow -i audio=Mic") {
		t.Errorf("inputs = %s", got)
	}
	for _, want := range []string{
		"[0:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,asplit=2[m0][s0]",
		"volume=0.80,asplit=2[m1][s1]",
		"[m0][m1][m2]amix=inputs=3:duration=longest:normalize=0[aout]",
	} {
		if !strings.Contains(graph, want) {
			t.Errorf("graph missing %q\n%s", want, graph)
		}
	}
	if got := joined(maps); got != "-map [aout] -map [s0] -map [s1] -map [s2]" {
		t.Errorf("maps = %s", got)
	}
	if got := strings.Join(replayTrackTitles(a), "|"); got != "All audio|Game & desktop|Discord|Microphone" {
		t.Errorf("titles = %s", got)
	}

	cfg := ReplayConfig{FPS: 60, BitrateKbps: 20000, Codec: ReplayCodecH264, Audio: a}
	if got := joined(BuildReplayArgs(oneSource(0), cfg, "h264_nvenc", true, "seg-%08d.ts", srcs)); !strings.Contains(got, "-c:a aac -b:a 256k") {
		t.Errorf("audio bitrate not applied:\n%s", got)
	}
}

func TestReplayMicNoiseSuppression(t *testing.T) {
	a := ReplayAudio{MicDevice: "Mic", MicNoise: MicNoiseStrong, MicVolume: 150}
	_, graph, _ := replayAudioArgs(replayAudioSources(a), a)
	want := "[0:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,highpass=f=100,afftdn=nr=24:nf=-45:tn=1,agate="
	if !strings.Contains(graph, want) || !strings.Contains(graph, ",volume=1.50[aout]") {
		t.Errorf("strong suppression should run before the volume:\n%s", graph)
	}
	a.MicNoise = MicNoiseOff
	if _, graph, _ := replayAudioArgs(replayAudioSources(a), a); strings.Contains(graph, "afftdn") {
		t.Errorf("off means untouched: %s", graph)
	}
	// Only the mic is filtered, never the game or Discord.
	b := ReplayAudio{Desktop: true, MicDevice: "Mic", MicNoise: MicNoiseLight}
	if _, graph, _ := replayAudioArgs(replayAudioSources(b), b); strings.Count(graph, "afftdn") != 1 || !strings.Contains(graph, "[1:a]aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo,highpass=f=80,afftdn") {
		t.Errorf("light suppression on the mic only:\n%s", graph)
	}
}

func TestReplayAudioSingleSource(t *testing.T) {
	a := ReplayAudio{Discord: true, SeparateTracks: true}
	_, graph, maps := replayAudioArgs(replayAudioSources(a), a)
	if strings.Contains(graph, "amix") || strings.Contains(graph, "asplit") {
		t.Errorf("one source needs no mix or split: %s", graph)
	}
	if got := joined(maps); got != "-map [aout]" {
		t.Errorf("maps = %s", got)
	}
	if got := replayTrackTitles(a); len(got) != 1 || got[0] != "Discord" {
		t.Errorf("titles = %v", got)
	}
	if got := replayTrackTitles(ReplayAudio{Desktop: true, Discord: true}); len(got) != 1 || got[0] != "All audio" {
		t.Errorf("mixed-only titles = %v", got)
	}
	if replayTrackTitles(ReplayAudio{}) != nil {
		t.Error("no audio should have no tracks")
	}
}

func TestListReplaySegmentsOrdersAndFilters(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"r0002-00000000.ts", "r0001-00000010.ts", "r0001-00000002.ts", "notes.txt", "r1-1.ts"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	segs := listReplaySegments(dir)
	var names []string
	for _, s := range segs {
		names = append(names, s.name)
	}
	if got := strings.Join(names, ","); got != "r0001-00000002.ts,r0001-00000010.ts,r0002-00000000.ts" {
		t.Errorf("order = %s", got)
	}
	if segs[2].run != 2 {
		t.Errorf("run = %d, want 2", segs[2].run)
	}
	if err := resetReplayDir(dir); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 2 {
		t.Errorf("reset should remove only segments, %d entries left", len(left))
	}
}

func TestReplayPruneKeepsWindow(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("r0001-%08d.ts", i)), nil, 0o644)
	}
	b := NewReplayBuffer()
	b.prune(ReplayConfig{Dir: dir, KeepSeconds: 10}) // 5 segments + spare + in-progress
	segs := listReplaySegments(dir)
	if len(segs) != 7 || segs[0].name != "r0001-00000013.ts" {
		t.Fatalf("kept %d segments starting at %v", len(segs), segs[0].name)
	}
}

// TestStitchReplay builds real MPEG-TS segments from lavfi (two "runs", like a
// supervisor restart) and checks the stitch is a playable MP4 of the right
// length. Needs ffmpeg + ffprobe on PATH; skipped otherwise.
func TestStitchReplay(t *testing.T) {
	bin, err := LocateFFmpeg()
	if err != nil {
		t.Skipf("ffmpeg not available: %v", err)
	}
	if _, err := LocateFFprobe(); err != nil {
		t.Skipf("ffprobe not available: %v", err)
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for run := 1; run <= 2; run++ {
		err := RunFFmpeg(ctx, []string{
			"-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", "testsrc2=s=320x180:r=30",
			"-f", "lavfi", "-i", "sine=f=440:r=48000",
			"-f", "lavfi", "-i", "sine=f=880:r=48000",
			"-map", "0:v", "-map", "1:a", "-map", "2:a",
			"-t", "6", "-c:v", "mpeg2video", "-g", "60",
			"-force_key_frames", "expr:gte(t,n_forced*2)",
			"-c:a", "aac", "-b:a", "96k",
			"-f", "segment", "-segment_time", "2", "-segment_format", "mpegts",
			filepath.Join(dir, fmt.Sprintf("r%04d-%%08d.ts", run)),
		})
		if err != nil {
			t.Fatalf("make segments: %v", err)
		}
	}
	segs := listReplaySegments(dir)
	if len(segs) < 6 {
		t.Fatalf("expected >= 6 segments, got %d", len(segs))
	}

	titles := []string{"All audio", "Microphone"}
	single := filepath.Join(dir, "single.mp4")
	if err := stitchReplay(bin, segs[:3], single, titles); err != nil {
		t.Fatalf("stitch one run: %v", err)
	}
	both := filepath.Join(dir, "both.mp4")
	if err := stitchReplay(bin, segs, both, titles); err != nil {
		t.Fatalf("stitch two runs: %v", err)
	}
	for path, wantMs := range map[string]int{single: 6000, both: 12000} {
		ms, err := ProbeDurationMs(ctx, path)
		if err != nil {
			t.Fatalf("probe %s: %v", path, err)
		}
		if ms < wantMs-300 || ms > wantMs+300 {
			t.Errorf("%s duration = %dms, want ~%dms", filepath.Base(path), ms, wantMs)
		}
		if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
			t.Errorf("temp output left behind for %s", path)
		}
	}

	// Both audio tracks survive the stitch, titled, with the mix as default.
	probe, _ := LocateFFprobe()
	out, err := exec.Command(probe, "-v", "error", "-select_streams", "a",
		"-show_entries", "stream=index:stream_tags=handler_name:stream_disposition=default",
		"-of", "csv=p=0", both).Output()
	if err != nil {
		t.Fatalf("ffprobe: %v", err)
	}
	if got := strings.TrimSpace(strings.ReplaceAll(string(out), "\r", "")); got != "1,1,All audio\n2,0,Microphone" {
		t.Errorf("audio tracks = %q", got)
	}
}

func oneSource(idx int) []replaySource { return []replaySource{{DDAIdx: idx}} }

func TestReplayArgsAllDisplays(t *testing.T) {
	cfg := ReplayConfig{FPS: 60, BitrateKbps: 40000, Codec: ReplayCodecHEVC, IncludeCursor: true}
	srcs := []replaySource{{DDAIdx: 0, X: 1920, Y: 0, W: 3440, H: 1440}, {DDAIdx: 1, X: 0, Y: 0, W: 1920, H: 1080}}
	got := joined(BuildReplayArgs(srcs, cfg, "hevc_nvenc", true, "seg-%08d.ts", nil))
	want := "-filter_complex ddagrab=output_idx=0:framerate=60:draw_mouse=1,hwdownload,format=bgra[s0];" +
		"ddagrab=output_idx=1:framerate=60:draw_mouse=1,hwdownload,format=bgra[s1];" +
		"[s0][s1]xstack=inputs=2:layout=1920_0|0_0:fill=black -c:v hevc_nvenc"
	if !strings.Contains(got, want) {
		t.Errorf("args missing %q\n%s", want, got)
	}
	if c := replayGraphCandidates(cfg, "hevc_nvenc", 2); len(c) != 1 || c[0] {
		t.Errorf("several monitors can't be zero-copy, got %v", c)
	}
}

func TestReplaySourcesAllDisplays(t *testing.T) {
	old := replayDDAIndex
	defer func() { replayDDAIndex = old }()
	replayDDAIndex = func(s ScreenInfo) (int, bool) { return s.ID, true }

	screens := []ScreenInfo{
		{ID: 0, X: 0, Y: 0, W: 3440, H: 1440, IsPrimary: true},
		{ID: 1, X: -1920, Y: 0, W: 1920, H: 1080},
	}
	srcs, w, h, err := replaySources(screens, ReplayAllDisplays)
	if err != nil {
		t.Fatal(err)
	}
	if w != 5360 || h != 1440 || len(srcs) != 2 || srcs[0].X != 1920 || srcs[1].X != 0 {
		t.Errorf("got %dx%d %+v", w, h, srcs)
	}
	srcs, w, _, err = replaySources(screens, -1)
	if err != nil || len(srcs) != 1 || w != 3440 {
		t.Errorf("primary: got %d sources, w=%d, err=%v", len(srcs), w, err)
	}
	if got := fitHeight(5360, 1440, replayMaxWidthH264); got != 1100 {
		t.Errorf("fitHeight = %d, want 1100", got)
	}
}
