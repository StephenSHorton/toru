package vid

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// edit_text.go renders the editor's text overlays as drawtext filters. Pure
// apart from writing one UTF-8 text file per overlay: drawtext reads the text
// from textfile= so the user's text never goes through filtergraph escaping.
//
// The preview (frontend/src/trim/TextOverlays.tsx) draws the same box: the
// text's font (fonts.go; DefaultTextFont when it has none or it isn't
// installed), size = FontPx output pixels or a fraction of the output height
// for the older s/m/l sizes, lines centred, line height = the font's
// Font.LineHeight (1.33 for Segoe UI: the line advance drawtext uses), the block
// centred on (X, Y). y_align=font makes text_h the line boxes, not the ink,
// so "gg" and "HH" sit at the same height, as they do in CSS. Keep the two in
// step.

// DefaultTextFont is the overlay font when a text names none: Segoe UI
// Semibold, on every Windows.
const DefaultTextFont = `C:\Windows\Fonts\seguisb.ttf`

// textSizes is each size's font size as a fraction of the output height.
var textSizes = map[string]float64{"s": 0.04, "m": 0.06, "l": 0.09}

// textBorderRatio is the outline width as a fraction of the font size.
const textBorderRatio = 0.06

// TextFilter returns a filter chain drawing texts on a w×h frame (the output
// after crop, before the preset's scale) and the text files it wrote into
// tmpDir; the caller deletes them after the export. Texts left empty once
// unsupported characters are dropped are skipped; no texts gives "".
func TextFilter(texts []TextOverlay, w, h int, fontFile, tmpDir string) (filter string, files []string, err error) {
	if len(texts) == 0 {
		return "", nil, nil
	}
	if w < 2 || h < 2 {
		return "", nil, fmt.Errorf("text: bad frame %dx%d", w, h)
	}
	if _, err := os.Stat(fontFile); err != nil {
		return "", nil, fmt.Errorf("text: font: %w", err)
	}
	var chain []string
	cleanup := func() {
		for _, f := range files {
			_ = os.Remove(f)
		}
	}
	for i, t := range texts {
		if _, ok := textSizes[t.Size]; !ok && t.FontPx <= 0 {
			cleanup()
			return "", nil, fmt.Errorf("text %d: unknown size %q", i, t.Size)
		}
		if t.StartMs < 0 || t.EndMs <= t.StartMs {
			cleanup()
			return "", nil, fmt.Errorf("text %d: bad time %d..%d ms", i, t.StartMs, t.EndMs)
		}
		body := CleanOverlayText(t.Text)
		if strings.TrimSpace(body) == "" {
			continue
		}
		f, err := os.CreateTemp(tmpDir, "toru-text-*.txt")
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("text: %w", err)
		}
		files = append(files, f.Name())
		_, werr := f.WriteString(body)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			cleanup()
			return "", nil, fmt.Errorf("text: %w", werr)
		}

		size := textPx(t, h)
		border := max(1, int(math.Round(float64(size)*textBorderRatio)))
		cx := int(math.Round(clamp01(t.X) * float64(w)))
		cy := int(math.Round(clamp01(t.Y) * float64(h)))
		chain = append(chain, "drawtext="+strings.Join([]string{
			"fontfile=" + filterValue(filepath.ToSlash(textFontFile(t.Font, fontFile))),
			"textfile=" + filterValue(filepath.ToSlash(f.Name())),
			"expansion=none",
			fmt.Sprintf("fontsize=%d", size),
			"fontcolor=white",
			fmt.Sprintf("borderw=%d", border),
			"bordercolor=black@0.8",
			"text_align=C",
			"y_align=font",
			fmt.Sprintf("x=%d-text_w/2", cx),
			fmt.Sprintf("y=%d-text_h/2", cy),
			"enable=" + filterValue(fmt.Sprintf("between(t,%s,%s)", msSeconds(t.StartMs), msSeconds(t.EndMs))),
		}, ":"))
	}
	if len(chain) == 0 {
		return "", nil, nil
	}
	return strings.Join(chain, ","), files, nil
}

// MaxTextFontPx caps a text's font size (TextOverlays.tsx: MAX_FONT_PX).
const MaxTextFontPx = 1000

// textPx is t's font size in output pixels on an h-tall frame: FontPx when
// set (capped), else its preset Size.
func textPx(t TextOverlay, h int) int {
	if t.FontPx > 0 {
		return min(t.FontPx, MaxTextFontPx)
	}
	return TextFontSize(t.Size, h)
}

// TextFontSize is the font size in output pixels for size on an h-tall frame.
func TextFontSize(size string, h int) int {
	return max(1, int(math.Round(textSizes[size]*float64(h))))
}

// CleanOverlayText is the text as the export draws it: CRLF to LF, tabs to
// spaces, emoji (plus their joiners and variation selectors) dropped since
// Segoe UI has no glyphs for them and drawtext would draw empty boxes, then
// trailing spaces and blank first/last lines trimmed (drawtext counts them,
// CSS white-space: pre doesn't). TextOverlays.tsx's cleanText applies the
// same rules to the preview.
func CleanOverlayText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.Map(func(r rune) rune {
		if isEmojiRune(r) {
			return -1
		}
		return r
	}, s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// isEmojiRune reports runes in the emoji and pictograph blocks, and the
// invisible ones that build emoji sequences.
func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF, // emoji, flags, pictographs
		r >= 0x2600 && r <= 0x27BF,   // misc symbols, dingbats
		r >= 0x2B00 && r <= 0x2BFF,   // arrows and stars (⭐)
		r >= 0xFE00 && r <= 0xFE0F,   // variation selectors
		r >= 0xE0020 && r <= 0xE007F, // tag sequences (subdivision flags)
		r == 0x200D,                  // zero-width joiner
		r == 0x20E3:                  // keycap
		return true
	}
	return false
}

// filterValue escapes s for use as one option value inside a filtergraph:
// first for the option parser (\ : '), then for the graph parser
// (\ ' [ ] , ;). Paths like C:/Users/O'Brien/... and between(t,a,b) then
// survive both.
func filterValue(s string) string {
	return escapeChars(escapeChars(s, `\:'`), `\'[],;`)
}

func escapeChars(s, special string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(special, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func clamp01(f float64) float64 {
	if math.IsNaN(f) {
		return 0.5
	}
	return min(max(f, 0), 1)
}
