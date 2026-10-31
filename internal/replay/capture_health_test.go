package replay

import (
	"testing"

	"github.com/StephenSHorton/toru/internal/capture"
)

// Trouble starts once, at captureTroubleAfter failures in a row, stays while
// capture keeps failing, and ends when a run lasts (Failures back to 0).
func TestCaptureHealth(t *testing.T) {
	var h captureHealth
	step := func(st capture.ReplayStatus) (bool, bool) {
		var started, recovered bool
		h, started, recovered = h.next(st)
		return started, recovered
	}
	for i := 1; i < captureTroubleAfter; i++ {
		if started, _ := step(capture.ReplayStatus{Running: true, Failures: i, Error: "e"}); started || h.troubled {
			t.Fatalf("trouble after %d failures", i)
		}
	}
	if started, _ := step(capture.ReplayStatus{Running: true, Failures: captureTroubleAfter, Error: "stopped"}); !started || h.msg != "stopped" {
		t.Fatalf("no trouble at %d failures: %+v", captureTroubleAfter, h)
	}
	if started, _ := step(capture.ReplayStatus{Running: true, Failures: captureTroubleAfter + 1, Error: "again"}); started || h.msg != "again" {
		t.Fatalf("trouble started twice or kept an old message: %+v", h)
	}
	// A new run starts while the streak is still counted: not recovered yet.
	if _, recovered := step(capture.ReplayStatus{Running: true, Recording: true, Failures: captureTroubleAfter + 1}); recovered {
		t.Fatal("recovered before a run lasted")
	}
	if _, recovered := step(capture.ReplayStatus{Running: true, Recording: true}); !recovered || h.troubled {
		t.Fatalf("not recovered: %+v", h)
	}

	h = captureHealth{troubled: true, msg: "x"}
	if started, recovered := step(capture.ReplayStatus{}); started || recovered || h.troubled {
		t.Fatalf("stopping the buffer should clear trouble quietly: %+v", h)
	}
}
