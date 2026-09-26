package capture

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShareArgsRebase(t *testing.T) {
	screens := twoMonitors()
	req := CaptureRequest{
		Mode: "share", Sub: SubRegion, MonitorID: 1,
		Rect:          Rect{X: -2460, Y: 100, W: 800, H: 600},
		IncludeCursor: true,
	}
	enc := shareH264("h264_nvenc", req.Rect.W, req.Rect.H)
	dda, err := BuildShareArgsDDA(req, screens, 0, enc, "live/index.m3u8", "live/seg_%05d.ts")
	if err != nil {
		t.Fatal(err)
	}
	gdi := BuildShareArgsGDI(req, enc, "live/index.m3u8", "live/seg_%05d.ts")
	ddaStr, gdiStr := strings.Join(dda, " "), strings.Join(gdi, " ")

	if !strings.Contains(ddaStr, "offset_x=100:offset_y=100") {
		t.Errorf("ddagrab share was not rebased:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "output_idx=0") {
		t.Errorf("ddagrab share output_idx should be the DXGI index:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "framerate=30") {
		t.Errorf("share grab should be 30fps:\n%s", ddaStr)
	}
	if !strings.Contains(ddaStr, "-f hls") || !strings.Contains(ddaStr, "index.m3u8") {
		t.Errorf("share should mux HLS:\n%s", ddaStr)
	}
	if !strings.Contains(gdiStr, "-offset_x -2460") {
		t.Errorf("gdigrab share should keep the virtual-desktop offset:\n%s", gdiStr)
	}
	if strings.Contains(gdiStr, "offset_x=100") {
		t.Errorf("gdigrab share must not be rebased:\n%s", gdiStr)
	}
}

func TestShareScaleWideGrab(t *testing.T) {
	req := CaptureRequest{
		Mode: "share", MonitorID: 0,
		Rect: Rect{X: 0, Y: 0, W: 2560, H: 1440},
	}
	enc := shareH264("h264_nvenc", req.Rect.W, req.Rect.H)
	args := BuildShareArgsGDI(req, enc, "a.m3u8", "s.ts")
	if !strings.Contains(strings.Join(args, " "), "scale=1920:-2") {
		t.Fatalf("wide share should scale down: %s", strings.Join(args, " "))
	}
	narrow := BuildShareArgsGDI(CaptureRequest{Rect: Rect{W: 1280, H: 720}}, enc, "a.m3u8", "s.ts")
	if strings.Contains(strings.Join(narrow, " "), "scale=") {
		t.Fatalf("1280-wide share should not scale: %s", strings.Join(narrow, " "))
	}
}

func TestShareBitrateClamp(t *testing.T) {
	if shareBitrate(100, 100) != "2500k" {
		t.Fatalf("tiny crop bitrate = %s", shareBitrate(100, 100))
	}
	if shareBitrate(3840, 2160) != "12000k" {
		t.Fatalf("4K bitrate = %s", shareBitrate(3840, 2160))
	}
}

func TestReadMJPEG(t *testing.T) {
	// Two synthetic frames. Not valid JPEG scans; the splitter only cares
	// about the SOI/EOI markers.
	var raw bytes.Buffer
	raw.Write([]byte{0x00, 0xFF, 0xD8, 0x01, 0x02, 0xFF, 0xD9, 0xFF, 0xD8, 0xFF, 0xD9})
	var got [][]byte
	if err := readMJPEG(&raw, func(frame []byte) {
		cp := make([]byte, len(frame))
		copy(cp, frame)
		got = append(got, cp)
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames", len(got))
	}
	if !bytes.Equal(got[0], []byte{0xFF, 0xD8, 0x01, 0x02, 0xFF, 0xD9}) {
		t.Fatalf("frame 0 = %v", got[0])
	}
	if !bytes.Equal(got[1], []byte{0xFF, 0xD8, 0xFF, 0xD9}) {
		t.Fatalf("frame 1 = %v", got[1])
	}
}

func TestShareHLSLifecycle(t *testing.T) {
	if _, err := LocateFFmpeg(); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	playlist := filepath.Join(dir, "index.m3u8")
	seg := filepath.ToSlash(filepath.Join(dir, "seg_%05d.ts"))
	args := []string{
		"-y", "-f", "lavfi", "-i", "testsrc=size=320x180:rate=30",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p", "-g", "30",
	}
	args = append(args, hlsMux(playlist, seg)...)
	sess, err := runShareAttempts(mustFFmpeg(t), [][]string{args}, ShareHLS, dir, nil, 400*time.Millisecond, 15*time.Second)
	if err != nil {
		t.Skipf("libx264 HLS not available here: %v", err)
	}
	defer sess.Stop()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if fi, err := os.Stat(playlist); err == nil && fi.Size() > 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("playlist never appeared\n%s", sess.stderr)
}

func TestShareMJPEGLifecycle(t *testing.T) {
	if _, err := LocateFFmpeg(); err != nil {
		t.Skip(err)
	}
	args := []string{
		"-y", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=15",
		"-q:v", "8", "-f", "image2pipe", "-vcodec", "mjpeg", "pipe:1",
	}
	sess, err := runShareAttempts(mustFFmpeg(t), [][]string{args}, ShareMJPEG, "", nil, 400*time.Millisecond, 15*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Stop()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		gen, jpeg := sess.Frame()
		if gen > 0 && len(jpeg) > 4 && jpeg[0] == 0xFF && jpeg[1] == 0xD8 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no JPEG frame arrived")
}

func mustFFmpeg(t *testing.T) string {
	t.Helper()
	bin, err := LocateFFmpeg()
	if err != nil {
		t.Skip(err)
	}
	return bin
}
