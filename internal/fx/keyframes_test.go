package fx

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"testing"
)

// TestKeyframesGolden checks ValueAt against the table the TS side wrote
// (frontend/scripts/keyframes-golden.ts), so preview and export animate the
// same way.
func TestKeyframesGolden(t *testing.T) {
	b, err := os.ReadFile("testdata/keyframes_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var table struct {
		Cases []struct {
			Name string `json:"name"`
			Keys []struct {
				T    float64 `json:"t"`
				V    any     `json:"v"`
				Ease string  `json:"ease"`
			} `json:"keys"`
			T    float64 `json:"t"`
			Base any     `json:"base"`
			Want any     `json:"want"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &table); err != nil {
		t.Fatal(err)
	}
	if len(table.Cases) < 100 {
		t.Fatalf("only %d cases", len(table.Cases))
	}
	for _, c := range table.Cases {
		keys := make([]Key, len(c.Keys))
		for i, k := range c.Keys {
			keys[i] = Key{T: k.T, V: k.V, Ease: k.Ease}
		}
		got := ValueAt(keys, c.T, c.Base)
		if !sameValue(got, c.Want) {
			t.Errorf("%s: got %v, want %v", c.Name, got, c.Want)
		}
	}
}

func sameValue(got, want any) bool {
	if g, ok := got.(float64); ok {
		w, ok := want.(float64)
		return ok && math.Abs(g-w) < 1e-9
	}
	if g, ok := point(got); ok {
		w, ok := point(want)
		return ok && math.Abs(g[0]-w[0]) < 1e-9 && math.Abs(g[1]-w[1]) < 1e-9
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}

func TestEaseEnds(t *testing.T) {
	for _, e := range []string{"linear", "in", "out", "inOut"} {
		if Ease(e, 0) != 0 || Ease(e, 1) != 1 {
			t.Errorf("%s: ends %v %v", e, Ease(e, 0), Ease(e, 1))
		}
	}
	if Ease("hold", 0.9) != 0 {
		t.Error("hold moves")
	}
}
