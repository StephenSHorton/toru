//go:build windows

package sys

import (
	"testing"
	"time"
)

// The scan codes are set 1 make codes, the same on every layout for these keys.
func TestKeyScan(t *testing.T) {
	cases := []struct {
		name     string
		scan     uint16
		extended bool
		letter   bool
	}{
		{"Up", 0x48, true, false},
		{"A", 0x1E, false, true},
		{"a", 0x1E, false, true},
		{"F5", 0x3F, false, false},
		{"Num5", 0x4C, false, false},
		{"Space", 0x39, false, false},
		{"Delete", 0x53, true, false},
		{"Num/", 0x35, true, false},
		{"LCtrl", 0x1D, false, false},
		{"RCtrl", 0x1D, true, false},
		{"1", 0x02, false, false},
	}
	for _, c := range cases {
		scan, ext, letter, err := KeyScan(c.name)
		if err != nil {
			t.Errorf("KeyScan(%q): %v", c.name, err)
			continue
		}
		if scan != c.scan || ext != c.extended || letter != c.letter {
			t.Errorf("KeyScan(%q) = %#x ext=%v letter=%v, want %#x ext=%v letter=%v",
				c.name, scan, ext, letter, c.scan, c.extended, c.letter)
		}
	}
	for _, bad := range []string{"", "MouseMiddle", "Mouse4", "F25", "F0", "Bogus"} {
		if _, _, _, err := KeyScan(bad); err == nil {
			t.Errorf("KeyScan(%q) succeeded, want an error", bad)
		}
	}
}

func TestKeyRepeatFrom(t *testing.T) {
	r := keyRepeatFrom(0, 0)
	if r.DelayNs != int64(250*time.Millisecond) || r.PeriodNs != int64(400*time.Millisecond) {
		t.Errorf("slowest = %+v", r)
	}
	r = keyRepeatFrom(3, 31)
	if r.DelayNs != int64(time.Second) || r.PeriodNs != int64(time.Second)/30 {
		t.Errorf("fastest = %+v", r)
	}
	if live := KeyRepeat(); live.DelayNs < int64(250*time.Millisecond) || live.PeriodNs <= 0 {
		t.Errorf("KeyRepeat() = %+v", live)
	}
}

func TestSystemQueries(t *testing.T) {
	if d := DoubleClickTime(); d <= 0 {
		t.Errorf("DoubleClickTime = %v", d)
	}
	if _, _, w, h := VirtualScreen(); w <= 0 || h <= 0 {
		t.Errorf("VirtualScreen size = %dx%d", w, h)
	}
	_ = CapsLock()
}
