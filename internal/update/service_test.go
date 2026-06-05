package update

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, current string
		want         bool
	}{
		{"v1.2.3", "1.2.2", true},
		{"v1.2.3", "1.2.3", false},
		{"v1.2.3", "1.3.0", false},
		{"v2.0.0", "1.9.9", true},
		{"v1.0.0", "1.0.0-rc.1", true}, // release beats its own prerelease
		{"v1.2.3", "dev", false},        // dev builds never nag
		{"v1.2.3", "", false},
		{"1.2.3", "1.2.0", true}, // tolerates missing leading v
	}
	for _, c := range cases {
		if got := isNewer(c.tag, c.current); got != c.want {
			t.Errorf("isNewer(%q, %q) = %v, want %v", c.tag, c.current, got, c.want)
		}
	}
}

func TestCmpSemver(t *testing.T) {
	if cmpSemver("1.10.0", "1.9.0") <= 0 {
		t.Error("1.10.0 should sort above 1.9.0 (numeric, not lexical)")
	}
	if cmpSemver("1.2.3", "1.2.3") != 0 {
		t.Error("equal versions should compare 0")
	}
}
