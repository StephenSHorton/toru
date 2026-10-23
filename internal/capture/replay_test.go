package capture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReplayArgsZeroCopyNative(t *testing.T) {
	cfg := ReplayConfig{FPS: 60, BitrateKbps: 20000, Codec: ReplayCodecH264, IncludeCursor: true}
	args := BuildReplayArgs(1, cfg, "h264_nvenc", true, `C:\seg\r0001-%08d.ts`)
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
		t.Errorf("output pattern must be the last arg (injectAudioMix relies on it), got %q", args[len(args)-1])
	}
}

func TestReplayArgsDownscaleAndAV1(t *testing.T) {
	cfg := ReplayConfig{FPS: 30, Height: 1080, BitrateKbps: 8000, Codec: ReplayCodecAV1}
	got := joined(BuildReplayArgs(0, cfg, "av1_qsv", false, "seg-%08d.mkv"))
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
	if c := replayGraphCandidates(native, "hevc_nvenc"); len(c) != 2 || !c[0] {
		t.Errorf("native NVENC should try zero-copy first, got %v", c)
	}
	if c := replayGraphCandidates(native, "h264_qsv"); len(c) != 1 || c[0] {
		t.Errorf("QSV can't take D3D11 frames from ddagrab, got %v", c)
	}
	if c := replayGraphCandidates(scaled, "h264_nvenc"); len(c) != 1 || c[0] {
		t.Errorf("downscale needs the CPU graph, got %v", c)
	}
}

func TestReplayAudioMixIsAAC(t *testing.T) {
	cfg := ReplayConfig{FPS: 60, BitrateKbps: 20000, Codec: ReplayCodecH264}
	args := BuildReplayArgs(0, cfg, "h264_nvenc", true, "seg-%08d.ts")
	pipes := []AudioInput{{PipePath: `\\.\pipe\a`, SampleFmt: "f32le", SampleRate: 48000, Channels: 2}}
	got := joined(injectAudioMixCodec(args, pipes, "Mic (USB)", "aac", replayAudioBitrate))
	for _, want := range []string{
		"[v] -f f32le -ar 48000 -ac 2 -i \\\\.\\pipe\\a -f dshow -i audio=Mic (USB)",
		"amix=inputs=2:duration=longest:normalize=0[aout] -map [aout] -c:a aac -b:a 160k seg-%08d.ts",
		"-map [v]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("args missing %q\n%s", want, got)
		}
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

	single := filepath.Join(dir, "single.mp4")
	if err := stitchReplay(bin, segs[:3], single); err != nil {
		t.Fatalf("stitch one run: %v", err)
	}
	both := filepath.Join(dir, "both.mp4")
	if err := stitchReplay(bin, segs, both); err != nil {
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
}
