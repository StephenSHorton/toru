package capture

import (
	"strings"
	"testing"
)

// twoMonitors: primary 1920x1080 at origin, secondary 2560x1440 to the LEFT
// (negative X), so we exercise negative virtual-desktop coordinates.
func twoMonitors() []ScreenInfo {
	return []ScreenInfo{
		{ID: 0, X: 0, Y: 0, W: 1920, H: 1080, ScaleFactor: 1.0, IsPrimary: true},
		{ID: 1, X: -2560, Y: 0, W: 2560, H: 1440, ScaleFactor: 1.5, IsPrimary: false},
	}
}

func joined(args []string) string { return strings.Join(args, " ") }

// The whole two-developer split depends on this: ddagrab MUST rebase the
// virtual-desktop Rect to monitor-relative, gdigrab MUST NOT.
func TestVideoArgsRebasing(t *testing.T) {
	screens := twoMonitors()
	// A 800x600 region on the SECONDARY monitor, 100px in from its top-left.
	// Secondary origin is (-2560,0), so virtual-desktop Rect.X = -2560+100 = -2460.
	req := CaptureRequest{
		Mode: ModeVideo, Sub: SubRegion, MonitorID: 1,
		Rect: Rect{X: -2460, Y: 100, W: 800, H: 600},
	}

	enc := VideoEncoder{Name: "h264_nvenc", Ext: ".mp4"}
	// ddaIdx 0 while MonitorID is 1: the DXGI output order is NOT the kbinani
	// order (observed inverted in the wild), so output_idx comes from the
	// resolved DXGI index while the offset rebase uses the kbinani screen.
	dda, err := BuildVideoArgsDDA(req, screens, 0, enc, "out.mp4")
	if err != nil {
		t.Fatalf("BuildVideoArgsDDA: %v", err)
	}
	gdi := BuildVideoArgsGDI(req, enc, "out.mp4")

	ddaStr, gdiStr := joined(dda), joined(gdi)

	// ddagrab: rebased to monitor-relative (100,100) via MonitorID's screen…
	if !strings.Contains(ddaStr, "offset_x=100:offset_y=100") {
		t.Errorf("ddagrab not rebased to monitor-relative (want offset_x=100:offset_y=100):\n%s", ddaStr)
	}
	// …while output_idx is the caller-resolved DXGI index, NOT MonitorID.
	if !strings.Contains(ddaStr, "output_idx=0") {
		t.Errorf("ddagrab output_idx must be the resolved DXGI index (0), not MonitorID:\n%s", ddaStr)
	}

	// gdigrab: uses the raw virtual-desktop coordinate (-2460), NOT rebased.
	if !strings.Contains(gdiStr, "-offset_x -2460") {
		t.Errorf("gdigrab should use raw virtual-desktop offset -2460:\n%s", gdiStr)
	}

	// The two paths MUST differ in exactly this way.
	if strings.Contains(gdiStr, "offset_x=100") {
		t.Errorf("gdigrab must NOT be rebased:\n%s", gdiStr)
	}
}

func TestMsToTimecode(t *testing.T) {
	cases := map[int]string{
		0:        "00:00:00.000",
		1500:     "00:00:01.500",
		61_000:   "00:01:01.000",
		3_661_250: "01:01:01.250",
	}
	for ms, want := range cases {
		if got := msToTimecode(ms); got != want {
			t.Errorf("msToTimecode(%d) = %q, want %q", ms, got, want)
		}
	}
}

// A spatial crop cannot be stream-copied: it must force the re-encode path
// even with Precise=false, emit even-aligned yuv420p-safe dimensions, keep the
// container-driven codec choice, and stay absent when no crop is requested.
func TestTrimCropArgs(t *testing.T) {
	cases := []struct {
		name    string
		req     TrimRequest
		want    []string // substrings that must appear in the joined args
		wantNot []string // substrings that must NOT appear
	}{
		{
			name: "crop forces re-encode even when Precise=false",
			req: TrimRequest{VideoPath: "in.mp4", StartMs: 0, EndMs: 1000, Precise: false,
				Crop: &Rect{X: 100, Y: 50, W: 300, H: 200}, OutPath: "out.mp4"},
			want:    []string{"-vf crop=300:200:100:50", "-c:v libx264"},
			wantNot: []string{"-c copy"},
		},
		{
			name: "odd sizes AND odd offsets round down to even (yuv420p chroma grid)",
			req: TrimRequest{VideoPath: "in.mp4", StartMs: 0, EndMs: 1000,
				Crop: &Rect{X: 101, Y: 51, W: 301, H: 201}, OutPath: "out.mp4"},
			want:    []string{"crop=300:200:100:50"},
			wantNot: []string{"crop=300:200:101:51"},
		},
		{
			name: "even values pass through untouched",
			req: TrimRequest{VideoPath: "in.mp4", StartMs: 0, EndMs: 1000,
				Crop: &Rect{X: 102, Y: 52, W: 302, H: 202}, OutPath: "out.mp4"},
			want: []string{"crop=302:202:102:52"},
		},
		{
			name: "tiny or non-positive sizes floor at 2, negative offsets clamp to 0",
			req: TrimRequest{VideoPath: "in.mp4", StartMs: 0, EndMs: 1000,
				Crop: &Rect{X: -5, Y: -1, W: 1, H: 0}, OutPath: "out.mp4"},
			want: []string{"crop=2:2:0:0"},
		},
		{
			name: "webm crop keeps the VP9/Opus codec choice and no faststart",
			req: TrimRequest{VideoPath: "in.webm", StartMs: 0, EndMs: 1000,
				Crop: &Rect{X: 0, Y: 0, W: 640, H: 360}, OutPath: "out.webm"},
			want:    []string{"-vf crop=640:360:0:0", "-c:v libvpx-vp9", "-c:a libopus"},
			wantNot: []string{"libx264", "faststart", "-c copy"},
		},
		{
			name: "mp4 crop keeps the H.264/AAC codec choice and faststart",
			req: TrimRequest{VideoPath: "in.mp4", StartMs: 0, EndMs: 1000, Precise: true,
				Crop: &Rect{X: 0, Y: 0, W: 640, H: 360}, OutPath: "out.mp4"},
			want:    []string{"-vf crop=640:360:0:0", "-c:v libx264", "-c:a aac", "-movflags +faststart"},
			wantNot: []string{"libvpx-vp9", "-c copy"},
		},
		{
			name:    "precise without crop has no -vf",
			req:     TrimRequest{VideoPath: "in.mp4", StartMs: 0, EndMs: 1000, Precise: true, OutPath: "out.mp4"},
			want:    []string{"-c:v libx264"},
			wantNot: []string{"-vf", "crop="},
		},
		{
			name:    "stream-copy without crop is unchanged",
			req:     TrimRequest{VideoPath: "in.mp4", StartMs: 500, EndMs: 1000, OutPath: "out.mp4"},
			want:    []string{"-ss 00:00:00.500 -to 00:00:01.000 -i in.mp4 -c copy"},
			wantNot: []string{"-vf", "crop=", "libx264"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := BuildTrimArgs(tc.req)
			got := joined(args)
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
			for _, w := range tc.wantNot {
				if strings.Contains(got, w) {
					t.Errorf("unexpected %q in:\n%s", w, got)
				}
			}
			// Positional sanity: -vf is an OUTPUT option, so it must follow the
			// input, and the output path must be last.
			if tc.req.Crop != nil {
				vf, in := indexOf(args, "-vf"), indexOf(args, "-i")
				if vf < 0 || in < 0 || vf < in {
					t.Errorf("-vf must come after -i (vf=%d, i=%d):\n%s", vf, in, got)
				}
			}
			if args[len(args)-1] != tc.req.OutPath {
				t.Errorf("output path must be last, got %q", args[len(args)-1])
			}
		})
	}
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

func TestUnknownModeRejected(t *testing.T) {
	s := &StubCapturer{OutDir: t.TempDir()}
	if _, err := s.Capture(CaptureRequest{Mode: "bogus"}); err == nil {
		t.Error("expected error for unknown mode, got nil")
	}
}
