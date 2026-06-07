package capture

import (
	"strings"
	"testing"
)

// The out-of-box policy is the §10 licensing decision: VP9 in WebM, no AVC
// obligation, no probing required. This test runs WITHOUT ffmpeg on purpose —
// the default path must never depend on a probe.
func TestDefaultPolicyIsVP9WebM(t *testing.T) {
	t.Setenv("TORU_CODEC", "")
	enc := SelectEncoder(videoReq())
	if enc.Name != "libvpx-vp9" || enc.Ext != ".webm" {
		t.Errorf("default policy must be VP9/WebM, got %s/%s", enc.Name, enc.Ext)
	}
	joinedArgs := strings.Join(enc.Args, " ")
	for _, want := range []string{"-deadline realtime", "-cpu-used 8", "-row-mt 1"} {
		if !strings.Contains(joinedArgs, want) {
			t.Errorf("VP9 realtime args missing %q: %s", want, joinedArgs)
		}
	}
}

func TestTargetBitrateClamps(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		{100, 100, "2000k"},      // tiny crop clamps up to 2 Mbps
		{1920, 1080, "12441k"},   // 1080p ≈ 12.4 Mbps
		{3840, 2160, "30000k"},   // 4K clamps down to 30 Mbps
	}
	for _, c := range cases {
		if got := targetBitrate(c.w, c.h); got != c.want {
			t.Errorf("targetBitrate(%d,%d) = %q, want %q", c.w, c.h, got, c.want)
		}
	}
}

// Container flags are muxer-PRIVATE options: +faststart on WebM is an ffmpeg
// error, so the WebM arg lists must not contain it anywhere.
func TestContainerFlagsGated(t *testing.T) {
	enc := VideoEncoder{Name: "libvpx-vp9", Ext: ".webm"}
	gdi := strings.Join(BuildVideoArgsGDI(videoReq(), enc, "out.webm"), " ")
	if strings.Contains(gdi, "faststart") {
		t.Errorf("WebM output must not carry -movflags:\n%s", gdi)
	}
	mp4 := strings.Join(BuildVideoArgsGDI(videoReq(), VideoEncoder{Name: "h264_nvenc", Ext: ".mp4"}, "out.mp4"), " ")
	if !strings.Contains(mp4, "-movflags +faststart") {
		t.Errorf("MP4 output must carry -movflags +faststart:\n%s", mp4)
	}
}

// Audio injection placement is LOAD-BEARING (ffmpeg options are positional):
// audio input groups must directly follow the VIDEO INPUT — never after the
// video codec options, where ffmpeg would parse "-c:v" as an input option and
// die with EINVAL. Explicit -map disables auto stream selection, so the video
// must be mapped too; multiple sources mix via amix:normalize=0.
func TestInjectAudioMix(t *testing.T) {
	enc := VideoEncoder{Name: "libvpx-vp9", Ext: ".webm"}
	sys := AudioInput{PipePath: `\\.\pipe\toru-sys`, SampleFmt: "f32le", SampleRate: 48000, Channels: 2}
	app := AudioInput{PipePath: `\\.\pipe\toru-app`, SampleFmt: "s16le", SampleRate: 48000, Channels: 2}

	// Single source on gdigrab: input after "-i desktop", direct map (video
	// input is 0, so the one audio input is 1).
	gdi := strings.Join(injectAudioMix(BuildVideoArgsGDI(videoReq(), enc, "out.webm"), []AudioInput{sys}, ""), " ")
	if !strings.Contains(gdi, `-i desktop -f f32le -ar 48000 -ac 2 -i \\.\pipe\toru-sys -c:v`) {
		t.Errorf("gdigrab: audio input must follow the video input, before -c:v:\n%s", gdi)
	}
	if !strings.Contains(gdi, "-map 0:v -map 1:a") {
		t.Errorf("gdigrab: explicit video + audio maps required:\n%s", gdi)
	}
	if !strings.HasSuffix(gdi, "-c:a libopus -b:a 128k out.webm") {
		t.Errorf("gdigrab: audio codec + output must close the command:\n%s", gdi)
	}

	// Three sources on ddagrab (system + app + mic): the filter graph gains a
	// [v] label, audio inputs start at 0 (the graph is not an -i input), and
	// all three mix via amix without normalization.
	dda, err := BuildVideoArgsDDA(videoReq(), twoMonitors(), 0, enc, "out.webm")
	if err != nil {
		t.Fatal(err)
	}
	ddaStr := strings.Join(injectAudioMix(dda, []AudioInput{sys, app}, "Microphone (Realtek)"), " ")
	if !strings.Contains(ddaStr, "format=bgra[v]") {
		t.Errorf("ddagrab: video graph must gain the [v] label:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "-map [v]") {
		t.Errorf("ddagrab: labeled video map required:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "-f dshow -i audio=Microphone (Realtek)") {
		t.Errorf("mic dshow input missing:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "[0:a][1:a][2:a]amix=inputs=3:duration=longest:normalize=0[aout]") {
		t.Errorf("3-source amix missing/mislabeled:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "-map [aout]") {
		t.Errorf("mixed audio map missing:\n%s", ddaStr)
	}
}

// Precise trim must re-encode with a codec that is LEGAL for the output
// container: H.264/AAC inside WebM is invalid and ffmpeg rejects it.
func TestTrimCodecFollowsContainer(t *testing.T) {
	webm := strings.Join(BuildTrimArgs(TrimRequest{
		VideoPath: "in.webm", StartMs: 0, EndMs: 1000, Precise: true, OutPath: "out.webm",
	}), " ")
	if !strings.Contains(webm, "libvpx-vp9") || strings.Contains(webm, "libx264") {
		t.Errorf("precise WebM trim must use VP9, got:\n%s", webm)
	}
	if strings.Contains(webm, "faststart") {
		t.Errorf("WebM trim must not carry -movflags:\n%s", webm)
	}

	mp4 := strings.Join(BuildTrimArgs(TrimRequest{
		VideoPath: "in.mp4", StartMs: 0, EndMs: 1000, Precise: true, OutPath: "out.mp4",
	}), " ")
	if !strings.Contains(mp4, "libx264") {
		t.Errorf("precise MP4 trim keeps libx264, got:\n%s", mp4)
	}
}
