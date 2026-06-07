package overlay

import (
	"errors"
	"strings"
	"testing"
)

// TestFreezeEnabledDefault locks the "absent => freeze ON" default that keeps the
// classic frozen-still behaviour for every overlay.json written before the
// preference existed (and for a brand-new install).
func TestFreezeEnabledDefault(t *testing.T) {
	on := true
	off := false
	cases := []struct {
		name string
		in   *bool
		want bool
	}{
		{"absent defaults on", nil, true},
		{"explicit on", &on, true},
		{"explicit off", &off, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := cropStore{Freeze: c.in}
			if got := st.freezeEnabled(); got != c.want {
				t.Fatalf("freezeEnabled(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// TestRecordingErrorMessage verifies the failed-start message is collapsed to a
// single line (it rides a window URL) and capped in length, while staying
// non-empty for a real error.
func TestRecordingErrorMessage(t *testing.T) {
	multiline := errors.New("start recording: all capture backends failed:\nffmpeg exited during startup\n  Unknown input format: 'ddagrab'")
	got := recordingErrorMessage(multiline)
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("message still has newlines/tabs: %q", got)
	}
	if got == "" {
		t.Fatal("message unexpectedly empty")
	}

	long := errors.New(strings.Repeat("x", 1000))
	got = recordingErrorMessage(long)
	if len([]rune(got)) > 281 { // 280 cap + the single ellipsis rune
		t.Fatalf("message not truncated: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated message should end with ellipsis: %q", got[len(got)-8:])
	}
}
