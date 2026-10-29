package capture

import (
	"strings"
	"testing"
)

func TestRecorderHandles(t *testing.T) {
	r := NewRecorder()
	if h := r.Handles(); len(h) != 0 {
		t.Fatalf("idle: %v", h)
	}
	r.recs["rec-10"], r.recs["rec-2"], r.recs["rec-1"] = &recording{}, &recording{}, &recording{}
	if got := strings.Join(r.Handles(), ","); got != "rec-1,rec-2,rec-10" {
		t.Fatalf("handles %s", got)
	}
}
