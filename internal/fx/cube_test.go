package fx

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// identityCube is an n³ identity LUT as .cube text.
func identityCube(n int, header string) string {
	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "LUT_3D_SIZE %d\n", n)
	for bi := 0; bi < n; bi++ {
		for g := 0; g < n; g++ {
			for r := 0; r < n; r++ {
				d := float64(n - 1)
				fmt.Fprintf(&b, "%f %f %f\n", float64(r)/d, float64(g)/d, float64(bi)/d)
			}
		}
	}
	return b.String()
}

func TestParseCubeIdentity(t *testing.T) {
	c, err := ParseCube(identityCube(5, "# comment\nTITLE \"Teal\"\n\nDOMAIN_MIN 0 0 0\nDOMAIN_MAX 1 1 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "Teal" || c.Size != 5 {
		t.Fatalf("title %q size %d", c.Title, c.Size)
	}
	for _, in := range [][3]float64{{0, 0, 0}, {1, 1, 1}, {0.3, 0.7, 0.11}, {0.999, 0.5, 0.01}, {-1, 2, 0.5}} {
		got := c.Sample(in[0], in[1], in[2])
		for k := 0; k < 3; k++ {
			want := math.Max(0, math.Min(1, in[k]))
			if math.Abs(got[k]-want) > 1e-6 {
				t.Errorf("Sample(%v)[%d] = %v, want %v", in, k, got[k], want)
			}
		}
	}
}

func TestParseCubeInvertAndDomain(t *testing.T) {
	// A 2³ LUT that inverts, over a 0..2 domain: input 1 is the middle.
	text := "DOMAIN_MAX 2 2 2\nLUT_3D_SIZE 2\n"
	for bi := 0; bi < 2; bi++ {
		for g := 0; g < 2; g++ {
			for r := 0; r < 2; r++ {
				text += fmt.Sprintf("%d %d %d\n", 1-r, 1-g, 1-bi)
			}
		}
	}
	c, err := ParseCube(text)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Sample(1, 0, 2)
	if math.Abs(got[0]-0.5) > 1e-9 || got[1] != 1 || got[2] != 0 {
		t.Errorf("Sample = %v, want [0.5 1 0]", got)
	}
}

func TestParseCubeRejects(t *testing.T) {
	for name, text := range map[string]string{
		"1D":        "LUT_1D_SIZE 4\n0 0 0\n",
		"no size":   "TITLE x\n",
		"too big":   "LUT_3D_SIZE 200\n",
		"short":     "LUT_3D_SIZE 2\n0 0 0\n",
		"long":      identityCube(2, "") + "0 0 0\n",
		"nan":       "LUT_3D_SIZE 2\n" + strings.Repeat("0 NaN 0\n", 8),
		"two vals":  "LUT_3D_SIZE 2\n" + strings.Repeat("0 0\n", 8),
		"early":     "0 0 0\nLUT_3D_SIZE 2\n",
		"bad range": "DOMAIN_MIN 1 1 1\nDOMAIN_MAX 1 1 1\n" + identityCube(2, ""),
	} {
		if _, err := ParseCube(text); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLutPath(t *testing.T) {
	for _, bad := range []string{"", "../x.cube", `a\b.cube`, "a/b.cube", "x.txt", ".cube", "a..cube"} {
		if _, err := LutPath(bad); err == nil {
			t.Errorf("LutPath(%q) accepted", bad)
		}
	}
	if _, err := LutPath("Teal Orange (2).cube"); err != nil {
		t.Error(err)
	}
}
