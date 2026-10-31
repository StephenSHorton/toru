package fx

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"testing"
)

// TestKeysExprMatchesValueAt has ffmpeg evaluate KeysExpr (as an aevalsrc
// signal, one sample per millisecond) and compares every sample with
// ValueAt. Skipped without ffmpeg ($TORU_FFMPEG or PATH).
func TestKeysExprMatchesValueAt(t *testing.T) {
	ff := os.Getenv("TORU_FFMPEG")
	if ff == "" {
		var err error
		if ff, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg")
		}
	}
	sets := map[string][]Key{
		"one":   {{T: 500, V: 2.0}},
		"eases": {{T: 100, V: 0.0, Ease: "in"}, {T: 400, V: 1.0, Ease: "out"}, {T: 700, V: -1.0, Ease: "inOut"}, {T: 1000, V: 0.5, Ease: "linear"}, {T: 1300, V: 3.0}},
		"hold":  {{T: 0, V: 1.0, Ease: "hold"}, {T: 600, V: 4.0, Ease: "hold"}, {T: 900, V: 2.0}},
		"same":  {{T: 300, V: 1.0}, {T: 300, V: 2.0}, {T: 800, V: 0.0}},
	}
	for name, keys := range sets {
		e := KeysExpr(keys, 7, "t")
		out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "aevalsrc=exprs='"+e+"':s=1000:d=1.5", "-f", "f64le", "-").Output()
		if err != nil {
			t.Fatalf("%s: %v (%s)", name, err, e)
		}
		for i := 0; i+8 <= len(out); i += 8 {
			ms := float64(i / 8)
			got := math.Float64frombits(binary.LittleEndian.Uint64(out[i:]))
			want := ValueAt(keys, ms, 7.0).(float64)
			if math.Abs(got-want) > 1e-9 {
				t.Fatalf("%s at %v ms: expression %v, ValueAt %v\n%s", name, ms, got, want, e)
			}
		}
	}
	if got := KeysExpr(nil, 0.25, "t"); got != "0.25" {
		t.Errorf("no keys = %q", got)
	}
}
