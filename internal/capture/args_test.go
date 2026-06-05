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

	dda, err := BuildVideoArgsDDA(req, screens, "out.mp4")
	if err != nil {
		t.Fatalf("BuildVideoArgsDDA: %v", err)
	}
	gdi := BuildVideoArgsGDI(req, "out.mp4")

	ddaStr, gdiStr := joined(dda), joined(gdi)

	// ddagrab: rebased to monitor-relative (100,100) + output_idx=1.
	if !strings.Contains(ddaStr, "offset_x=100:offset_y=100") {
		t.Errorf("ddagrab not rebased to monitor-relative (want offset_x=100:offset_y=100):\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "output_idx=1") {
		t.Errorf("ddagrab missing output_idx=1:\n%s", ddaStr)
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

func TestUnknownModeRejected(t *testing.T) {
	s := &StubCapturer{OutDir: t.TempDir()}
	if _, err := s.Capture(CaptureRequest{Mode: "bogus"}); err == nil {
		t.Error("expected error for unknown mode, got nil")
	}
}
