package capture

import (
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// replay_retry.go is how the replay supervisor recovers when capture keeps
// stopping.
//
// ddagrab never re-creates its desktop duplication: once Windows takes it
// away (DXGI_ERROR_ACCESS_LOST, e.g. a fullscreen game taking over the
// output, a display mode change, the UAC desktop), the next AcquireNextFrame
// or ReleaseFrame fails and ffmpeg exits ("DDA ReleaseFrame failed!"). The
// supervisor is what starts a new one. It used to wait a fixed 500 ms after
// any run that got past the startup grace, so a game that kept taking the
// output made a restart every ~3 s that recorded nothing and cleared its
// error each time. Now a run that stops before replayStableRun counts as a
// failure: retries back off, the error stays up, and after
// replaySwitchAfter failures in a row on one source the other source is
// tried (gfxcapture, Windows Graphics Capture, when ffmpeg has it).

// Replay capture sources (ReplayStatus.Source).
const (
	ReplaySourceDDA = "dda" // Desktop Duplication (ddagrab)
	ReplaySourceWGC = "wgc" // Windows Graphics Capture (gfxcapture)
)

const (
	// replayStableRun is how long a run must last to count as working.
	replayStableRun = 20 * time.Second
	// replayRestartDelay is the pause before restarting a run that worked,
	// or that was stopped on purpose (Discord restarted, disk space back).
	replayRestartDelay = 500 * time.Millisecond
	// replayFirstBackoff and replayMaxBackoff bound the wait after a run
	// that failed soon after starting; it doubles each time in between.
	replayFirstBackoff = time.Second
	replayMaxBackoff   = 30 * time.Second
	// replaySwitchAfter is how many failures in a row on one source make the
	// supervisor try the other one.
	replaySwitchAfter = 2
)

// replayVideo is the capture source for one ffmpeg run.
type replayVideo struct {
	Source   string  // ReplaySourceDDA ("" is the same) or ReplaySourceWGC
	DDAIdx   int     // ddagrab output_idx
	HMonitor uintptr // gfxcapture monitor
}

// replayVideoSource returns the source filter that starts the video graph.
// Both sources give D3D11 frames, so the rest of the graph is the same.
// gfxcapture only sends a frame when the screen changes; fps= makes the rate
// constant like ddagrab's, which keeps segment and keyframe timing exact.
func replayVideoSource(v replayVideo, cfg ReplayConfig) string {
	if v.Source == ReplaySourceWGC {
		return fmt.Sprintf("gfxcapture=hmonitor=%d:max_framerate=%d:capture_cursor=%s,fps=%d",
			v.HMonitor, cfg.FPS, boolToInt(cfg.IncludeCursor), cfg.FPS)
	}
	return fmt.Sprintf("ddagrab=output_idx=%d:framerate=%d:draw_mouse=%s",
		v.DDAIdx, cfg.FPS, boolToInt(cfg.IncludeCursor))
}

// replayRetry decides, after each run, how long to wait and which source the
// next run uses. Only the supervisor goroutine touches it.
type replayRetry struct {
	source    string
	canSwitch bool          // ffmpeg has the backup source
	failures  int           // failed runs in a row, any source
	onSource  int           // failed runs in a row on the current source
	backoff   time.Duration // wait after the next quick failure
}

func newReplayRetry(canSwitch bool) *replayRetry {
	return &replayRetry{source: ReplaySourceDDA, canSwitch: canSwitch, backoff: replayFirstBackoff}
}

// failed records a run that ended by itself after lived (0 = it never
// started). It returns the wait before the next run, and whether the next
// run uses the other source.
func (r *replayRetry) failed(lived time.Duration) (wait time.Duration, switched bool) {
	if lived >= replayStableRun {
		// It worked for a while: one hiccup, so restart straight away.
		r.failures, r.onSource, r.backoff = 1, 1, replayFirstBackoff
		return replayRestartDelay, false
	}
	r.failures++
	r.onSource++
	if r.canSwitch && r.onSource >= replaySwitchAfter {
		if r.source == ReplaySourceWGC {
			r.source = ReplaySourceDDA
		} else {
			r.source = ReplaySourceWGC
		}
		r.onSource = 0
		return replayRestartDelay, true
	}
	wait = r.backoff
	r.backoff = min(r.backoff*2, replayMaxBackoff)
	return wait, false
}

// stable records that the current run has lasted replayStableRun. The
// source that worked is kept.
func (r *replayRetry) stable() {
	r.failures, r.onSource, r.backoff = 0, 0, replayFirstBackoff
}

var (
	gfxcaptureMu    sync.Mutex
	gfxcaptureProbe = map[string]bool{}
)

// gfxcaptureAvailable reports whether the ffmpeg at bin has the gfxcapture
// source (FFmpeg 8.0+ Windows builds). Cached per binary. A var for tests.
var gfxcaptureAvailable = func(bin string) bool {
	gfxcaptureMu.Lock()
	defer gfxcaptureMu.Unlock()
	if ok, done := gfxcaptureProbe[bin]; done {
		return ok
	}
	cmd := exec.Command(bin, "-hide_banner", "-filters")
	configureSysProcAttr(cmd)
	out, err := cmd.Output()
	ok := err == nil && hasFilter(string(out), "gfxcapture")
	gfxcaptureProbe[bin] = ok
	return ok
}

// hasFilter reports whether ffmpeg -filters output lists name.
func hasFilter(list, name string) bool {
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[1] == name {
			return true
		}
	}
	return false
}
