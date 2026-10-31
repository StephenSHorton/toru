package capture

import (
	"strings"
	"testing"
	"time"
)

// The 0.15 loop: ddagrab lost the output to a fullscreen game seconds after
// every start, and each restart came 500 ms later with no backoff. Quick
// failures must now back off, up to the cap.
func TestReplayRetryBacksOffQuickFailures(t *testing.T) {
	r := newReplayRetry(false)
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i, w := range want {
		got, switched := r.failed(3 * time.Second)
		if switched {
			t.Fatalf("failure %d switched source with no backup available", i+1)
		}
		if got != w*time.Second {
			t.Fatalf("failure %d: wait %v, want %v", i+1, got, w*time.Second)
		}
	}
	if r.failures != len(want) {
		t.Fatalf("failures = %d, want %d", r.failures, len(want))
	}
	r.stable()
	if got, _ := r.failed(time.Second); got != replayFirstBackoff || r.failures != 1 {
		t.Fatalf("after recovering: wait %v failures %d, want %v and 1", got, r.failures, replayFirstBackoff)
	}
}

// A run that worked for a while and then stopped (display mode change) is
// restarted at once and doesn't build on an earlier streak.
func TestReplayRetryRestartsALongRunAtOnce(t *testing.T) {
	r := newReplayRetry(true)
	r.failed(0)
	got, switched := r.failed(replayStableRun + time.Second)
	if got != replayRestartDelay || switched || r.failures != 1 || r.source != ReplaySourceDDA {
		t.Fatalf("wait %v switched %v failures %d source %s", got, switched, r.failures, r.source)
	}
}

// Desktop duplication failing twice in a row tries the backup source, and
// keeps it while it works. If the backup fails as well, the two alternate
// with the backoff still growing.
func TestReplayRetrySwitchesSource(t *testing.T) {
	r := newReplayRetry(true)
	if _, switched := r.failed(2 * time.Second); switched || r.source != ReplaySourceDDA {
		t.Fatal("switched after one failure")
	}
	wait, switched := r.failed(2 * time.Second)
	if !switched || r.source != ReplaySourceWGC || wait != replayRestartDelay {
		t.Fatalf("second failure: switched %v source %s wait %v", switched, r.source, wait)
	}
	if r.failures != 2 {
		t.Fatalf("failures = %d, want 2 (the streak shows until a run lasts)", r.failures)
	}
	if wait, _ := r.failed(time.Second); wait != 2*time.Second {
		t.Fatalf("backup's first failure waits %v, want 2s", wait)
	}
	if _, switched := r.failed(time.Second); !switched || r.source != ReplaySourceDDA {
		t.Fatalf("backup failing twice should go back to duplication, source %s", r.source)
	}

	r = newReplayRetry(true)
	r.failed(0)
	r.failed(0)
	r.stable()
	if r.source != ReplaySourceWGC || r.failures != 0 {
		t.Fatalf("after the backup worked: source %s failures %d", r.source, r.failures)
	}
}

func TestReplayArgsBackupSource(t *testing.T) {
	cfg := ReplayConfig{FPS: 144, BitrateKbps: 65000, KeepSeconds: 60, IncludeCursor: true}
	got := strings.Join(BuildReplayArgs(replayVideo{Source: ReplaySourceWGC, HMonitor: 65537}, cfg, "h264_nvenc", true, "seg-%08d.ts", nil), " ")
	if !strings.Contains(got, "-filter_complex gfxcapture=hmonitor=65537:max_framerate=144:capture_cursor=1,fps=144 ") {
		t.Fatalf("backup graph: %s", got)
	}
	if strings.Contains(got, "ddagrab") {
		t.Fatalf("backup graph still uses ddagrab: %s", got)
	}
	got = strings.Join(BuildReplayArgs(replayVideo{Source: ReplaySourceWGC, HMonitor: 7}, cfg, "h264_qsv", false, "seg-%08d.ts", nil), " ")
	if !strings.Contains(got, "fps=144,hwdownload,format=bgra,format=nv12") {
		t.Fatalf("backup CPU graph: %s", got)
	}
}

func TestHasFilter(t *testing.T) {
	list := " .. ddagrab           |->V       Grab Windows Desktop images\n" +
		" .. gfxcapture        |->V       Capture graphics/screen content\n"
	if !hasFilter(list, "gfxcapture") || !hasFilter(list, "ddagrab") {
		t.Fatal("missed a listed filter")
	}
	if hasFilter(" .. gfxcapturex |->V x\n", "gfxcapture") || hasFilter("", "gfxcapture") {
		t.Fatal("matched an unlisted filter")
	}
}

func TestCaptureStopReason(t *testing.T) {
	tail := "[Parsed_ddagrab_0 @ 000001f0c2a1b2c0] DDA ReleaseFrame failed!\n" +
		"[fc#0 @ 000002454d2c5c80] Error while filtering: Generic error in an external library\n" +
		"[fc#0 @ 000002454d2c5c80] Terminating thread with return code -542398533 (Generic error in an external library)\n"
	if got := captureStopReason(tail); got != "DDA ReleaseFrame failed" {
		t.Fatalf("got %q", got)
	}
	// What ffmpeg 9.0.2 logs when a display mode change takes the output.
	lost := "[null @ 1] Application provided invalid, non monotonically increasing dts\n" +
		"[Parsed_ddagrab_0 @ 0000014325c63fc0] AcquireNextFrame failed: 887a0026\n" +
		"[Parsed_ddagrab_0 @ 0000014325c63fc0] EOF timestamp not reliable\n" +
		"[fc#0 @ 0000014325b90f40] Terminating thread with return code -542398533 (Generic error in an external library)\n"
	if got := captureStopReason(lost); got != "AcquireNextFrame failed: 887a0026, the screen was taken over by a fullscreen app or a display change" {
		t.Fatalf("access lost: got %q", got)
	}
	if got := captureStopReason("[x @ 1] something else\n"); got != "[x @ 1] something else" {
		t.Fatalf("no capture line: got %q", got)
	}
}
