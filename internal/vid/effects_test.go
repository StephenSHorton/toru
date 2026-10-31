package vid

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/StephenSHorton/toru/internal/fx/fxtest"
)

func fx(mod func(*ClipJSON)) ClipJSON {
	c := ClipJSON{Speed: 1, Volume: 1, Filters: FiltersJSON{Contrast: 1, Saturation: 1, Look: "none"}}
	if mod != nil {
		mod(&c)
	}
	return c
}

func TestClipVideoFilter(t *testing.T) {
	const sepia = "colorchannelmixer=rr=0.393:rg=0.769:rb=0.189:gr=0.349:gg=0.686:gb=0.168:br=0.272:bg=0.534:bb=0.131"
	cases := []struct {
		name string
		clip ClipJSON
		srcH int
		want string
	}{
		{"no effects", fx(nil), 1080, ""},
		{"zero filters mean none", ClipJSON{Speed: 1}, 1080, ""},
		{"green screen", fx(func(c *ClipJSON) { c.Chroma = &ChromaJSON{Color: "#00ff00", Similarity: 0.3, Blend: 0.1} }), 1080,
			"chromakey=color=0x00FF00:similarity=0.3:blend=0.1"},
		{"chroma clamps and reads short hex", fx(func(c *ClipJSON) { c.Chroma = &ChromaJSON{Color: "#0f0", Similarity: 0, Blend: 3} }), 1080,
			"chromakey=color=0x00FF00:similarity=0.01:blend=1"},
		{"bad colour falls back to green", fx(func(c *ClipJSON) { c.Chroma = &ChromaJSON{Color: "blue", Similarity: 0.2} }), 1080,
			"chromakey=color=0x00FF00:similarity=0.2:blend=0"},
		{"sepia look", fx(func(c *ClipJSON) { c.Filters.Look = "sepia" }), 1080, "format=rgba," + sepia},
		{"black and white = zero saturation", fx(func(c *ClipJSON) { c.Filters.Saturation = 0 }), 1080,
			"format=rgba,colorchannelmixer=rr=0.299:rg=0.587:rb=0.114:gr=0.299:gg=0.587:gb=0.114:br=0.299:bg=0.587:bb=0.114"},
		{"brightness and contrast", fx(func(c *ClipJSON) { c.Filters.Brightness = 0.2; c.Filters.Contrast = 1.5 }), 1080,
			"format=rgba,lutrgb=r='clip((val-127.5)*1.5+127.5+51,0,255)':g='clip((val-127.5)*1.5+127.5+51,0,255)':b='clip((val-127.5)*1.5+127.5+51,0,255)'"},
		{"blur scales with height", fx(func(c *ClipJSON) { c.Filters.Blur = 10 }), 1440, "format=gbrap,avgblur=sizeX=13:sizeY=13"},
		{"blur rounds on a small frame", fx(func(c *ClipJSON) { c.Filters.Blur = 20 }), 60, "format=gbrap,avgblur=sizeX=1:sizeY=1"},
		{"blur too small to show", fx(func(c *ClipJSON) { c.Filters.Blur = 1 }), 360, ""},
		{"everything, in preview order", fx(func(c *ClipJSON) {
			c.Chroma = &ChromaJSON{Color: "#00FF00", Similarity: 0.25, Blend: 0.05}
			c.Filters = FiltersJSON{Brightness: -0.1, Contrast: 1.2, Saturation: 1, Look: "sepia", Blur: 2}
		}), 1080,
			"chromakey=color=0x00FF00:similarity=0.25:blend=0.05,format=rgba," + sepia +
				",lutrgb=r='clip((val-127.5)*1.2+127.5+-25.5,0,255)':g='clip((val-127.5)*1.2+127.5+-25.5,0,255)':b='clip((val-127.5)*1.2+127.5+-25.5,0,255)'" +
				",format=gbrap,avgblur=sizeX=2:sizeY=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClipVideoFilter(tc.clip, tc.srcH); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestColorMatrix(t *testing.T) {
	near := func(a, b [9]float64) bool {
		for i := range a {
			if math.Abs(a[i]-b[i]) > 1e-9 {
				return false
			}
		}
		return true
	}
	if m := ColorMatrix(FiltersJSON{Saturation: 1, Look: "none"}); !near(m, identity3) {
		t.Errorf("default isn't identity: %v", m)
	}
	if m := ColorMatrix(FiltersJSON{Saturation: 1, Look: "warm"}); !near(m, LookMatrices["warm"]) {
		t.Errorf("look alone isn't its matrix: %v", m)
	}
	// Saturation 0 makes every row the luma weights; a look after it mixes
	// those rows, so each output row is (row sum) x luma weights.
	m := ColorMatrix(FiltersJSON{Saturation: 0, Look: "cool"})
	cool := LookMatrices["cool"]
	for r := 0; r < 3; r++ {
		sum := cool[r*3] + cool[r*3+1] + cool[r*3+2]
		want := [3]float64{sum * lumaR, sum * lumaG, sum * lumaB}
		for c := 0; c < 3; c++ {
			if math.Abs(m[r*3+c]-want[c]) > 1e-9 {
				t.Errorf("look·sat row %d = %v, want %v", r, m[r*3:r*3+3], want)
			}
		}
	}
	// Saturation keeps grey grey: every row sums to 1.
	m = ColorMatrix(FiltersJSON{Saturation: 2.5, Look: "none"})
	for r := 0; r < 3; r++ {
		if s := m[r*3] + m[r*3+1] + m[r*3+2]; math.Abs(s-1) > 1e-9 {
			t.Errorf("row %d sums to %v", r, s)
		}
	}
}

func TestClipAudioFilter(t *testing.T) {
	cases := []struct {
		name   string
		clip   ClipJSON
		segLen float64
		want   string
	}{
		{"nothing", fx(nil), 10, ""},
		{"2x", fx(func(c *ClipJSON) { c.Speed = 2 }), 5, "atempo=2"},
		{"4x chains", fx(func(c *ClipJSON) { c.Speed = 4 }), 5, "atempo=2,atempo=2"},
		{"3x", fx(func(c *ClipJSON) { c.Speed = 3 }), 5, "atempo=2,atempo=1.5"},
		{"0.5x", fx(func(c *ClipJSON) { c.Speed = 0.5 }), 5, "atempo=0.5"},
		{"0.25x chains", fx(func(c *ClipJSON) { c.Speed = 0.25 }), 5, "atempo=0.5,atempo=0.5"},
		{"0.3x", fx(func(c *ClipJSON) { c.Speed = 0.3 }), 5, "atempo=0.5,atempo=0.6"},
		{"speed clamps to 4", fx(func(c *ClipJSON) { c.Speed = 10 }), 5, "atempo=2,atempo=2"},
		{"volume", fx(func(c *ClipJSON) { c.Volume = 1.5 }), 5, "volume=1.5"},
		{"muted wins over volume", fx(func(c *ClipJSON) { c.Volume = 1.5; c.Muted = true }), 5, "volume=0"},
		{"fades", fx(func(c *ClipJSON) { c.FadeIn = 1500; c.FadeOut = 2000 }), 10, "afade=t=in:st=0:d=1.5,afade=t=out:st=8:d=2"},
		{"fades fit the clip", fx(func(c *ClipJSON) { c.FadeIn = 3000; c.FadeOut = 3000 }), 4, "afade=t=in:st=0:d=3,afade=t=out:st=3:d=1"},
		{"all, in order", fx(func(c *ClipJSON) { c.Speed = 0.5; c.Volume = 0.8; c.FadeIn = 500; c.FadeOut = 500 }), 6,
			"atempo=0.5,volume=0.8,afade=t=in:st=0:d=0.5,afade=t=out:st=5.5:d=0.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClipAudioFilter(tc.clip, tc.segLen); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

func TestAtempoFactorsMultiplyBack(t *testing.T) {
	for _, s := range []float64{0.25, 0.3, 0.5, 0.75, 1, 1.25, 2, 2.5, 3.9, 4} {
		p := 1.0
		for _, f := range atempoFactors(s) {
			if f < 0.5-1e-9 || f > 2+1e-9 {
				t.Errorf("%v: step %v outside 0.5..2", s, f)
			}
			p *= f
		}
		if math.Abs(p-s) > 1e-9 {
			t.Errorf("%v: steps multiply to %v", s, p)
		}
	}
}

// TestEffectsRunInFFmpeg runs every chain through a real ffmpeg, so a typo
// or a filter the build lacks fails here and not in an export. It runs the
// pinned LGPL build with TORU_FFMPEG_PINNED=1 or $TORU_FFMPEG (see
// internal/fx/fxtest), else ffmpeg on PATH; skipped without any.
func TestEffectsRunInFFmpeg(t *testing.T) {
	ff := os.Getenv("TORU_FFMPEG")
	if ff != "" || os.Getenv("TORU_FFMPEG_PINNED") == "1" {
		ff = fxtest.PinnedFFmpeg(t)
	} else {
		var err error
		if ff, err = exec.LookPath("ffmpeg"); err != nil {
			t.Skip("no ffmpeg on PATH")
		}
	}
	all := fx(func(c *ClipJSON) {
		c.Chroma = &ChromaJSON{Color: "#00FF00", Similarity: 0.3, Blend: 0.1}
		c.Filters = FiltersJSON{Brightness: 0.1, Contrast: 1.3, Saturation: 0.5, Look: "vintage", Blur: 4}
	})
	for _, look := range []string{"none", "bw", "sepia", "vintage", "cool", "warm"} {
		c := all
		c.Filters.Look = look
		vf := ClipVideoFilter(c, 180) + ",format=rgba"
		out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc2=s=320x180:r=10:d=0.3", "-vf", vf, "-f", "null", "-").CombinedOutput()
		if err != nil {
			t.Errorf("%s: %v\n%s\nfilter: %s", look, err, out, vf)
		}
	}
	af := ClipAudioFilter(fx(func(c *ClipJSON) { c.Speed = 0.25; c.Volume = 1.2; c.FadeIn = 200; c.FadeOut = 300 }), 2)
	out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "sine=f=440:d=0.5", "-af", af, "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Errorf("audio: %v\n%s\nfilter: %s", err, out, af)
	}
	if strings.Contains(ClipVideoFilter(all, 180), "boxblur") || strings.Contains(ClipVideoFilter(all, 180), "eq=") {
		t.Error("GPL-only filter in the chain")
	}
}

// TestDefaultChromaKeepsNeutrals runs the default green screen through a real
// ffmpeg: white, grey and black stay opaque (chromakey compares colour only,
// so all three sit 0.377 from pure green), while pure green and lit green
// screen colours go fully transparent. Box centres only: the 3x3 average
// softens the pixel ring where a box meets green. Skipped without ffmpeg.
func TestDefaultChromaKeepsNeutrals(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg on PATH")
	}
	boxes := []struct {
		name   string
		rgb    [3]byte
		opaque bool
	}{
		{"white", [3]byte{255, 255, 255}, true},
		{"grey", [3]byte{128, 128, 128}, true},
		{"black", [3]byte{0, 0, 0}, true},
		{"pure green", [3]byte{0, 255, 0}, false},
		{"near green", [3]byte{0x3C, 0xC8, 0x50}, false},
		{"lit green screen", [3]byte{60, 180, 75}, false},
	}
	const box = 16
	w, h := box*len(boxes), box
	frame := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		for _, b := range boxes {
			for x := 0; x < box; x++ {
				frame = append(frame, b.rgb[:]...)
			}
		}
	}
	dc := DefaultChroma
	vf := ClipVideoFilter(ClipJSON{Chroma: &dc}, h) + ",format=rgba"
	cmd := exec.Command(ff, "-v", "error", "-f", "rawvideo", "-pix_fmt", "rgb24", "-s", fmt.Sprintf("%dx%d", w, h), "-i", "-",
		"-vf", vf, "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-")
	cmd.Stdin = bytes.NewReader(frame)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil || len(out) != w*h*4 {
		t.Fatalf("ffmpeg: %v (%d bytes)\n%s\nfilter: %s", err, len(out), stderr.String(), vf)
	}
	for i, b := range boxes {
		a := out[((h/2)*w+i*box+box/2)*4+3]
		if b.opaque && a != 255 {
			t.Errorf("%s: alpha %d, want 255 (opaque)", b.name, a)
		}
		if !b.opaque && a != 0 {
			t.Errorf("%s: alpha %d, want 0 (keyed)", b.name, a)
		}
	}
}
