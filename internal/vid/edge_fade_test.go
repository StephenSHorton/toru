package vid

import "testing"

// A split leaves two clips whose sound runs on unbroken: no edge fade goes
// between them. Anything else at a cut gets one (Irori #131).
func TestSeamless(t *testing.T) {
	a := ClipJSON{Media: "m", Start: 0, In: 1000, Out: 3000, Speed: 1, Volume: 1}
	b := ClipJSON{Media: "m", Start: 2000, In: 3000, Out: 5000, Speed: 1, Volume: 1}
	if !seamless(a, b) {
		t.Fatal("a split should be seamless")
	}
	for name, c := range map[string]ClipJSON{
		"other media":  {Media: "n", Start: 2000, In: 3000, Out: 5000, Speed: 1, Volume: 1},
		"gap":          {Media: "m", Start: 2500, In: 3000, Out: 5000, Speed: 1, Volume: 1},
		"source jump":  {Media: "m", Start: 2000, In: 4000, Out: 6000, Speed: 1, Volume: 1},
		"other speed":  {Media: "m", Start: 2000, In: 3000, Out: 5000, Speed: 2, Volume: 1},
		"other volume": {Media: "m", Start: 2000, In: 3000, Out: 5000, Speed: 1, Volume: 0.5},
		"own fade in":  {Media: "m", Start: 2000, In: 3000, Out: 5000, Speed: 1, Volume: 1, FadeIn: 200},
		"reversed":     {Media: "m", Start: 2000, In: 3000, Out: 5000, Speed: 1, Volume: 1, Reversed: true},
		"freeze frame": {Media: "m", Start: 2000, In: 3000, Out: 5000, Speed: 1, Volume: 1, Hold: true},
	} {
		if seamless(a, c) {
			t.Errorf("%s: should not be seamless", name)
		}
	}
}
