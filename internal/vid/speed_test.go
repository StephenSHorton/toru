package vid

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// TestSpeedGolden checks the Go ramp maths against the table speed.ts
// wrote (frontend/scripts/speed-golden.ts), so the export times frames
// exactly as the preview shows them.
func TestSpeedGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/speed_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Cases []struct {
			Name   string    `json:"name"`
			SrcLen float64   `json:"srcLen"`
			Points []speedPt `json:"points"`
			Length float64   `json:"length"`
			At     []struct{ S, T, V float64 }
			Inv    []struct{ T, S float64 }
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	if len(g.Cases) < 50 {
		t.Fatalf("only %d golden cases", len(g.Cases))
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }
	for _, gc := range g.Cases {
		c := &speedCurve{Pts: normSpeedPts(gc.Points, gc.SrcLen), Audio: "follow"}
		if !near(c.outLength(), gc.Length) {
			t.Errorf("%s: length %v, want %v", gc.Name, c.outLength(), gc.Length)
		}
		for _, a := range gc.At {
			if !near(c.outAt(a.S), a.T) || !near(c.speedAt(a.S), a.V) {
				t.Errorf("%s: at s=%v got T=%v v=%v, want T=%v v=%v", gc.Name, a.S, c.outAt(a.S), c.speedAt(a.S), a.T, a.V)
			}
		}
		for _, a := range gc.Inv {
			if !near(c.srcAt(a.T), a.S) {
				t.Errorf("%s: s(%v) = %v, want %v", gc.Name, a.T, c.srcAt(a.T), a.S)
			}
		}
	}
}

func TestSpeedFlatSegment(t *testing.T) {
	// The spec's trap: a k = 0 segment must not divide by zero.
	c := &speedCurve{Pts: normSpeedPts([]speedPt{{0, 2}, {4, 2}}, 4), Audio: "follow"}
	if got := c.outLength(); got != 2 {
		t.Fatalf("4 s at 2x = %v s, want 2", got)
	}
	e := curveSetpts(c)
	if strings.Contains(e, "log(") || !strings.Contains(e, "/2") {
		t.Fatalf("flat segment expression %q", e)
	}
}

func TestCurveSetptsShape(t *testing.T) {
	c := &speedCurve{Pts: normSpeedPts([]speedPt{{0, 1}, {1, 3}, {2, 3}, {3, 1}}, 3), Audio: "follow"}
	e := curveSetpts(c)
	if !strings.HasPrefix(e, "setpts='(") || !strings.HasSuffix(e, ")/TB'") {
		t.Fatalf("expression %q", e)
	}
	if n := strings.Count(e, "if(lt("); n != 3 {
		t.Fatalf("%d branches for 3 segments: %q", n, e)
	}
	if strings.ContainsAny(e, "eE") && strings.Contains(e, "e-") {
		t.Fatalf("exponent in %q", e)
	}
}

func TestRampAudioChunksFillTheClip(t *testing.T) {
	for _, pts := range [][]speedPt{
		{{0, 1}, {2, 0.1}, {3, 0.1}, {5, 1}},
		{{0, 4}, {1.5, 1}, {5, 1}},
		{{0, 1}, {1, 6}, {2, 6}, {3, 1}},
	} {
		end := pts[len(pts)-1].S
		c := &speedCurve{Pts: normSpeedPts(pts, end), Audio: "follow"}
		sum := 0.0
		for _, ch := range rampAudioChunks(c) {
			sum += (ch.S1 - ch.S0) / ch.Tempo
			if ch.Audible && (ch.Tempo < audioMinSpeed || ch.Tempo > audioMaxSpeed) {
				t.Errorf("chunk at %v..%v audible at tempo %v", ch.S0, ch.S1, ch.Tempo)
			}
		}
		if math.Abs(sum-c.outLength()) > 1e-9 {
			t.Errorf("chunks last %v s, clip %v s", sum, c.outLength())
		}
	}
}

func TestClipLengthMsCurveAndHold(t *testing.T) {
	c := ClipJSON{In: 1000, Out: 3000, Speed: 1, SpeedCurve: &SpeedCurveJSON{Points: []SpeedPointJSON{{0, 1}, {2000, 3}}, Audio: "follow"}}
	if got, want := ClipLengthMs(c), int(math.Round(math.Log(3)*1000)); got != want {
		t.Fatalf("curve clip %d ms, want %d", got, want)
	}
	h := ClipJSON{In: 5000, Out: 7000, Speed: 1, Hold: true, SpeedCurve: c.SpeedCurve}
	if got := ClipLengthMs(h); got != 2000 {
		t.Fatalf("hold clip %d ms, want 2000", got)
	}
}
