package capture

import (
	"image"
	"image/draw"
	"math"
)

// WindowComposeOpts controls the macOS-style window still treatment: transparent
// padding outside the window, a soft drop shadow, and optional rounded-corner
// alpha so desktop pixels that leaked into the rect (Win11 rounded chrome /
// invisible resize borders) disappear.
//
// Dimensions are PHYSICAL pixels (already DPI-scaled by the caller).
type WindowComposeOpts struct {
	// Pad is transparent margin on every side (also the blur room for the shadow).
	// 0 = no padding / no shadow room.
	Pad int
	// Blur is the box-blur radius applied to the shadow mask (passes ≈ gaussian).
	Blur int
	// OffsetY shifts the shadow down (positive = below the window), matching the
	// typical OS window shadow bias.
	OffsetY int
	// CornerR is the rounded-corner radius applied as an alpha mask on the window
	// content. 0 = square (maximized / legacy).
	CornerR int
	// ShadowAlpha is peak shadow opacity in 0..255 (black RGB).
	ShadowAlpha uint8
	// SkipBeautify disables pad/shadow/round entirely and returns a straight copy
	// of src (used for maximized / near-fullscreen windows).
	SkipBeautify bool
}

// DefaultWindowComposeOpts returns macOS-like still treatment scaled by DPI.
// scale is the monitor scale factor (1.0 = 96 DPI, 1.5 = 144 DPI, …).
func DefaultWindowComposeOpts(scale float64) WindowComposeOpts {
	if scale <= 0 {
		scale = 1
	}
	// Tuned to read close to macOS window screenshots at 100% and 150% DPI.
	return WindowComposeOpts{
		Pad:         scaleI(40, scale),
		Blur:        scaleI(18, scale),
		OffsetY:     scaleI(10, scale),
		CornerR:     scaleI(8, scale), // Win11 system corner radius ≈ 8 DIP
		ShadowAlpha: 110,
	}
}

func scaleI(v int, scale float64) int {
	n := int(math.Round(float64(v) * scale))
	if n < 1 && v > 0 {
		return 1
	}
	return n
}

// ComposeWindowStill places src on a transparent canvas with optional rounded
// corners and a soft drop shadow. The result is always a fresh *image.RGBA with
// Bounds().Min at (0,0).
//
// This is pure image processing — the caller supplies already-cropped window
// pixels (from a frozen desktop still or a live grab of the DWM frame bounds).
func ComposeWindowStill(src image.Image, opt WindowComposeOpts) *image.RGBA {
	if src == nil {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}

	// Straight copy path for maximized / fullscreen-ish windows.
	if opt.SkipBeautify || opt.Pad <= 0 {
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
		if opt.CornerR > 0 && !opt.SkipBeautify {
			applyRoundedAlpha(dst, opt.CornerR)
		}
		return dst
	}

	pad := opt.Pad
	outW := w + 2*pad
	outH := h + 2*pad
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))

	// --- shadow ---
	if opt.ShadowAlpha > 0 && opt.Blur > 0 {
		// Alpha mask of the (rounded) window, then box-blur and offset downward.
		mask := make([]uint8, w*h)
		fillRoundedMask(mask, w, h, opt.CornerR)
		blurred := boxBlurAlpha(mask, w, h, opt.Blur)

		// Shadow is drawn with the window origin at (pad, pad+OffsetY).
		sx0 := pad
		sy0 := pad + opt.OffsetY
		sa := float64(opt.ShadowAlpha) / 255.0
		for y := 0; y < h; y++ {
			oy := sy0 + y
			if oy < 0 || oy >= outH {
				continue
			}
			row := y * w
			for x := 0; x < w; x++ {
				a := blurred[row+x]
				if a == 0 {
					continue
				}
				ox := sx0 + x
				if ox < 0 || ox >= outW {
					continue
				}
				// Premultiply-ish: black RGB with scaled alpha.
				alpha := uint8(float64(a) * sa)
				if alpha == 0 {
					continue
				}
				i := out.PixOffset(ox, oy)
				// Max with existing (usually empty) so overlapping soft edges stack softly.
				if alpha > out.Pix[i+3] {
					out.Pix[i+0] = 0
					out.Pix[i+1] = 0
					out.Pix[i+2] = 0
					out.Pix[i+3] = alpha
				}
			}
		}
	}

	// --- window content on top ---
	// Draw into a temp buffer so we can apply corner alpha without mutating src,
	// then Src-blit over the shadow (Src replaces; we want window opaque where
	// present, so use Over for anti-aliased corners).
	win := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(win, win.Bounds(), src, b.Min, draw.Src)
	if opt.CornerR > 0 {
		applyRoundedAlpha(win, opt.CornerR)
	}
	dstRect := image.Rect(pad, pad, pad+w, pad+h)
	draw.Draw(out, dstRect, win, image.Point{}, draw.Over)

	return out
}

// fillRoundedMask writes 0/255 coverage for a w×h rounded rect into mask.
func fillRoundedMask(mask []uint8, w, h, r int) {
	if r <= 0 {
		for i := range mask {
			mask[i] = 255
		}
		return
	}
	if r*2 > w {
		r = w / 2
	}
	if r*2 > h {
		r = h / 2
	}
	rf := float64(r)
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			mask[row+x] = coverageRounded(float64(x)+0.5, float64(y)+0.5, float64(w), float64(h), rf)
		}
	}
}

// applyRoundedAlpha multiplies dst's alpha by a rounded-rect mask in place.
func applyRoundedAlpha(dst *image.RGBA, r int) {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	if r <= 0 {
		return
	}
	if r*2 > w {
		r = w / 2
	}
	if r*2 > h {
		r = h / 2
	}
	rf := float64(r)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cov := coverageRounded(float64(x)+0.5, float64(y)+0.5, float64(w), float64(h), rf)
			if cov == 255 {
				continue
			}
			i := dst.PixOffset(b.Min.X+x, b.Min.Y+y)
			// Scale existing alpha (and premultiplied RGB if any — our captures are
			// straight alpha opaque RGB so just scale A).
			dst.Pix[i+3] = uint8((int(dst.Pix[i+3]) * int(cov)) / 255)
			if cov == 0 {
				dst.Pix[i+0] = 0
				dst.Pix[i+1] = 0
				dst.Pix[i+2] = 0
			}
		}
	}
}

// coverageRounded returns 0..255 coverage of pixel center (px,py) against a
// rounded rect [0,w)×[0,h) with corner radius r. Uses a 1px soft edge for AA.
func coverageRounded(px, py, w, h, r float64) uint8 {
	if r <= 0 {
		return 255
	}
	// Outside the outer AABB → transparent.
	if px < 0 || py < 0 || px >= w || py >= h {
		return 0
	}
	// Interior (away from all corners) → opaque.
	if px >= r && px < w-r {
		return 255
	}
	if py >= r && py < h-r {
		return 255
	}
	// Distance to nearest corner circle centre.
	cx := px
	cy := py
	switch {
	case px < r && py < r: // top-left
		cx, cy = r, r
	case px >= w-r && py < r: // top-right
		cx, cy = w-r, r
	case px < r && py >= h-r: // bottom-left
		cx, cy = r, h-r
	case px >= w-r && py >= h-r: // bottom-right
		cx, cy = w-r, h-r
	default:
		return 255
	}
	d := math.Hypot(px-cx, py-cy)
	// Soft edge across ~1px for anti-aliasing.
	if d <= r-0.5 {
		return 255
	}
	if d >= r+0.5 {
		return 0
	}
	// Linear falloff across the edge.
	t := (r + 0.5 - d) // 1 → 0 across the band
	return uint8(math.Max(0, math.Min(255, t*255)))
}

// boxBlurAlpha runs three horizontal+vertical box blurs (≈ gaussian) on a w×h
// alpha plane. radius is the half-width of each box.
func boxBlurAlpha(src []uint8, w, h, radius int) []uint8 {
	if radius <= 0 || w <= 0 || h <= 0 {
		out := make([]uint8, len(src))
		copy(out, src)
		return out
	}
	// Three passes of box blur approximate a gaussian.
	a := make([]uint8, w*h)
	b := make([]uint8, w*h)
	copy(a, src)
	for pass := 0; pass < 3; pass++ {
		boxBlurH(a, b, w, h, radius)
		boxBlurV(b, a, w, h, radius)
	}
	return a
}

func boxBlurH(src, dst []uint8, w, h, r int) {
	span := 2*r + 1
	for y := 0; y < h; y++ {
		row := y * w
		// Seed window sum for x=0.
		sum := 0
		for k := -r; k <= r; k++ {
			x := k
			if x < 0 {
				x = 0
			} else if x >= w {
				x = w - 1
			}
			sum += int(src[row+x])
		}
		dst[row] = uint8(sum / span)
		for x := 1; x < w; x++ {
			// Slide: drop left, add right (edge-clamped).
			left := x - r - 1
			if left < 0 {
				left = 0
			}
			right := x + r
			if right >= w {
				right = w - 1
			}
			// Recompute is safer near edges with clamp; for speed we re-sum on edges only.
			if x-r-1 < 0 || x+r >= w {
				sum = 0
				for k := -r; k <= r; k++ {
					xx := x + k
					if xx < 0 {
						xx = 0
					} else if xx >= w {
						xx = w - 1
					}
					sum += int(src[row+xx])
				}
			} else {
				sum += int(src[row+right]) - int(src[row+left])
			}
			dst[row+x] = uint8(sum / span)
		}
	}
}

func boxBlurV(src, dst []uint8, w, h, r int) {
	span := 2*r + 1
	for x := 0; x < w; x++ {
		sum := 0
		for k := -r; k <= r; k++ {
			y := k
			if y < 0 {
				y = 0
			} else if y >= h {
				y = h - 1
			}
			sum += int(src[y*w+x])
		}
		dst[x] = uint8(sum / span)
		for y := 1; y < h; y++ {
			if y-r-1 < 0 || y+r >= h {
				sum = 0
				for k := -r; k <= r; k++ {
					yy := y + k
					if yy < 0 {
						yy = 0
					} else if yy >= h {
						yy = h - 1
					}
					sum += int(src[yy*w+x])
				}
			} else {
				top := y - r - 1
				bot := y + r
				sum += int(src[bot*w+x]) - int(src[top*w+x])
			}
			dst[y*w+x] = uint8(sum / span)
		}
	}
}
