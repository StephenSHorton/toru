package fx

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// cube.go reads imported .cube 3D LUTs (docs/video-editor-v3.md, User
// presets: "LUT import"). They live in %AppData%\toru\luts; a lut effect
// names one by its file name (LutPattern), so a project never points at an
// arbitrary path. The preview's twin is frontend/src/trim/fx/cube.ts: both
// sample the table with the same trilinear arithmetic, and the result goes
// into the stack's generated colour LUT like any other colour effect.

// MaxCubeSize is the largest LUT_3D_SIZE accepted (65³ is the biggest size
// grading tools commonly write).
const MaxCubeSize = 65

// Cube is a parsed 3D LUT: Size³ RGB triples, red changing fastest, then
// green, then blue (the .cube order), and the input domain.
type Cube struct {
	Title     string
	Size      int
	Data      []float64
	DomainMin [3]float64
	DomainMax [3]float64
}

// ParseCube reads a .cube file's text. It accepts TITLE, DOMAIN_MIN,
// DOMAIN_MAX, comments and blank lines, and refuses 1D LUTs, sizes outside
// 2..MaxCubeSize and a wrong number of entries.
func ParseCube(text string) (*Cube, error) {
	c := &Cube{DomainMax: [3]float64{1, 1, 1}}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	line := 0
	for sc.Scan() {
		line++
		s := strings.TrimSpace(sc.Text())
		if s == "" || s[0] == '#' {
			continue
		}
		f := strings.Fields(s)
		switch strings.ToUpper(f[0]) {
		case "TITLE":
			c.Title = strings.Trim(strings.TrimSpace(s[len(f[0]):]), `"`)
			continue
		case "LUT_1D_SIZE":
			return nil, errors.New("that's a 1D LUT; only 3D LUTs (LUT_3D_SIZE) are supported")
		case "LUT_3D_SIZE":
			if len(f) != 2 {
				return nil, fmt.Errorf("line %d: LUT_3D_SIZE needs one number", line)
			}
			n, err := strconv.Atoi(f[1])
			if err != nil || n < 2 || n > MaxCubeSize {
				return nil, fmt.Errorf("line %d: LUT_3D_SIZE must be 2..%d", line, MaxCubeSize)
			}
			c.Size = n
			c.Data = make([]float64, 0, n*n*n*3)
			continue
		case "DOMAIN_MIN", "DOMAIN_MAX":
			v, err := triple(f[1:])
			if err != nil {
				return nil, fmt.Errorf("line %d: %s: %w", line, f[0], err)
			}
			if strings.EqualFold(f[0], "DOMAIN_MIN") {
				c.DomainMin = v
			} else {
				c.DomainMax = v
			}
			continue
		case "LUT_1D_INPUT_RANGE", "LUT_3D_INPUT_RANGE":
			if len(f) != 3 {
				return nil, fmt.Errorf("line %d: %s needs two numbers", line, f[0])
			}
			lo, err1 := strconv.ParseFloat(f[1], 64)
			hi, err2 := strconv.ParseFloat(f[2], 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("line %d: %s needs two numbers", line, f[0])
			}
			c.DomainMin, c.DomainMax = [3]float64{lo, lo, lo}, [3]float64{hi, hi, hi}
			continue
		}
		if c.Size == 0 {
			// Some writers put other keywords first; skip them, but data
			// before the size is an error.
			if _, err := strconv.ParseFloat(f[0], 64); err != nil {
				continue
			}
			return nil, fmt.Errorf("line %d: colour values before LUT_3D_SIZE", line)
		}
		v, err := triple(f)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if len(c.Data) == cap(c.Data) {
			return nil, fmt.Errorf("line %d: more than %d³ entries", line, c.Size)
		}
		c.Data = append(c.Data, v[0], v[1], v[2])
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if c.Size == 0 {
		return nil, errors.New("not a .cube LUT (no LUT_3D_SIZE line)")
	}
	if want := c.Size * c.Size * c.Size * 3; len(c.Data) != want {
		return nil, fmt.Errorf("expected %d³ = %d entries, found %d", c.Size, want/3, len(c.Data)/3)
	}
	for i := 0; i < 3; i++ {
		if !(c.DomainMax[i] > c.DomainMin[i]) {
			return nil, errors.New("DOMAIN_MAX must be above DOMAIN_MIN")
		}
	}
	return c, nil
}

func triple(f []string) ([3]float64, error) {
	var v [3]float64
	if len(f) != 3 {
		return v, errors.New("expected three numbers")
	}
	for i, s := range f {
		n, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return v, fmt.Errorf("%q isn't a number", s)
		}
		v[i] = n
	}
	return v, nil
}

// Sample is the LUT at colour c (0..1 per channel, clamped to the domain),
// trilinearly interpolated, each channel clamped to 0..1. Keep the
// arithmetic in step with cube.ts sampleCube.
func (c *Cube) Sample(r, g, b float64) [3]float64 {
	n := c.Size
	in := [3]float64{r, g, b}
	var i0 [3]int
	var fr [3]float64
	for k := 0; k < 3; k++ {
		x := (in[k] - c.DomainMin[k]) / (c.DomainMax[k] - c.DomainMin[k])
		if !(x > 0) {
			x = 0
		} else if x > 1 {
			x = 1
		}
		x *= float64(n - 1)
		j := int(math.Floor(x))
		if j > n-2 {
			j = n - 2
		}
		i0[k] = j
		fr[k] = x - float64(j)
	}
	at := func(ri, gi, bi, ch int) float64 { return c.Data[((bi*n+gi)*n+ri)*3+ch] }
	var out [3]float64
	for ch := 0; ch < 3; ch++ {
		c00 := at(i0[0], i0[1], i0[2], ch)*(1-fr[0]) + at(i0[0]+1, i0[1], i0[2], ch)*fr[0]
		c10 := at(i0[0], i0[1]+1, i0[2], ch)*(1-fr[0]) + at(i0[0]+1, i0[1]+1, i0[2], ch)*fr[0]
		c01 := at(i0[0], i0[1], i0[2]+1, ch)*(1-fr[0]) + at(i0[0]+1, i0[1], i0[2]+1, ch)*fr[0]
		c11 := at(i0[0], i0[1]+1, i0[2]+1, ch)*(1-fr[0]) + at(i0[0]+1, i0[1]+1, i0[2]+1, ch)*fr[0]
		c0 := c00*(1-fr[1]) + c10*fr[1]
		c1 := c01*(1-fr[1]) + c11*fr[1]
		v := c0*(1-fr[2]) + c1*fr[2]
		if !(v > 0) {
			v = 0
		} else if v > 1 {
			v = 1
		}
		out[ch] = v
	}
	return out
}

// LutDir is %AppData%\toru\luts, where imported LUTs are kept. A var so
// tests can point it elsewhere.
var LutDir = func() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru", "luts"), nil
}

// LutPattern is an imported LUT's id: its file name in LutDir. No path
// separators, so an id can never name a file outside the folder.
var LutPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _.()+-]{0,99}\.cube$`)

// LutPath is the file of an imported LUT id.
func LutPath(id string) (string, error) {
	if !LutPattern.MatchString(id) || strings.Contains(id, "..") {
		return "", fmt.Errorf("bad LUT id %q", id)
	}
	dir, err := LutDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, id), nil
}

// LoadLut reads and parses an imported LUT by id.
func LoadLut(id string) (*Cube, error) {
	p, err := LutPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("the LUT %q isn't imported on this PC", id)
	}
	if err != nil {
		return nil, err
	}
	return ParseCube(string(b))
}
