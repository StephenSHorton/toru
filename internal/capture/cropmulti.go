package capture

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

// blackOpaque fills dead zones in a stitched straddle crop (region pixels covered
// by no monitor) so they read as solid desktop void rather than transparent.
var blackOpaque = color.RGBA{R: 0, G: 0, B: 0, A: 255}

// CropImageMulti stitches a STRADDLE crop — a region spanning two or more
// monitors — out of the per-monitor frozen images into ONE lossless PNG, and
// returns its path. It is the multi-monitor counterpart of CropImage: where
// CropImage crops a region fully inside one monitor's image, this assembles a
// region that crosses monitor seams.
//
// Coordinate space: vr is in VIRTUAL-DESKTOP PHYSICAL px (origin = primary
// top-left; monitors left/above carry NEGATIVE x/y) — the same space as
// CropToPhysical's `emit` Rect and ScreenInfo.X/Y. Each monitor occupies the
// rect [sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H] in that space, and frozens[sc.ID]
// holds that monitor's W×H physical pixels. Because the whole virtual desktop is
// ONE physical-pixel grid (Per-Monitor-V2 awareness — see internal/dpi), the
// stitch is a straight per-monitor blit with NO rescaling, even across monitors
// with different DPI scales: 1 virtual unit == 1 physical pixel everywhere.
//
// frozens may be the full per-monitor map (freeze-ON) or only the monitors the
// crop touches (freeze-OFF live grab); a screen whose image is absent is skipped.
// screens supplies each monitor's virtual origin (the frozen RGBA's own
// Bounds().Min is (0,0) — kbinani CaptureRect zeroes it — so the origin MUST come
// from ScreenInfo, not the image bounds).
//
// Dead zones — output pixels that fall in a gap between non-tiling monitors (e.g.
// monitors of different heights, or a non-contiguous arrangement) — are left
// OPAQUE BLACK, matching how the desktop void reads in a screenshot.
func CropImageMulti(frozens map[int]*image.RGBA, screens []ScreenInfo, vr Rect) (string, error) {
	if vr.W <= 0 || vr.H <= 0 {
		return "", fmt.Errorf("crop multi: invalid rect %dx%d (W/H must be > 0)", vr.W, vr.H)
	}

	out := image.NewRGBA(image.Rect(0, 0, vr.W, vr.H))
	// Fill opaque black so any dead zone (region pixels covered by no monitor) is
	// solid rather than transparent.
	draw.Draw(out, out.Bounds(), image.NewUniform(blackOpaque), image.Point{}, draw.Src)

	want := image.Rect(vr.X, vr.Y, vr.X+vr.W, vr.Y+vr.H)
	covered := false
	for _, sc := range screens {
		img := frozens[sc.ID]
		if img == nil {
			continue // freeze-OFF passes only the hit monitors' images
		}
		monRect := image.Rect(sc.X, sc.Y, sc.X+sc.W, sc.Y+sc.H)
		isect := want.Intersect(monRect)
		if isect.Empty() {
			continue
		}
		// Source point in the frozen image's own coordinate space. The image covers
		// [sc.X..] in virtual space but its Bounds().Min is (0,0), so a virtual point
		// (vx,vy) maps to image point Bounds().Min + (vx-sc.X, vy-sc.Y). Adding
		// Bounds().Min keeps this correct even if a future capture path returns a
		// non-zero-origin image.
		sp := img.Bounds().Min.Add(image.Pt(isect.Min.X-sc.X, isect.Min.Y-sc.Y))
		// Destination rect in the output's own coordinate space (origin at vr.Min).
		dst := image.Rect(
			isect.Min.X-vr.X,
			isect.Min.Y-vr.Y,
			isect.Max.X-vr.X,
			isect.Max.Y-vr.Y,
		)
		draw.Draw(out, dst, img, sp, draw.Src)
		covered = true
	}
	if !covered {
		return "", fmt.Errorf("crop multi: rect %+v intersects no monitor image", vr)
	}

	return writeTempPNG(out)
}

// writeTempPNG encodes img to a lossless temp PNG under %TEMP%/toru and returns
// its path. Shared by the multi-monitor stitch (single-monitor crops use the
// inline copy in CropImage/CropStill, kept separate to avoid churning that file).
func writeTempPNG(img image.Image) (string, error) {
	dir := filepath.Join(os.TempDir(), "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("crop multi: mkdir temp: %w", err)
	}
	out, err := os.CreateTemp(dir, "toru-shot-*.png")
	if err != nil {
		return "", fmt.Errorf("crop multi: create temp: %w", err)
	}
	if err := png.Encode(out, img); err != nil {
		_ = out.Close()
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("crop multi: encode png: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(out.Name())
		return "", fmt.Errorf("crop multi: close png: %w", err)
	}
	return out.Name(), nil
}
