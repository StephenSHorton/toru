package vid

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"golang.org/x/image/font/sfnt"
)

// fonts.go lists the installed fonts a text overlay can use. A text stores a
// font's Name (its full face name, e.g. "Arial Bold"), never a path, so a
// project still opens on another PC; the export looks the name up here and
// falls back to DefaultTextFont when that PC doesn't have it. The preview
// (frontend/src/trim/fonts.ts) loads the same file from Font.URL with
// @font-face and applies the same fallback, so both draw the same face.
//
// Each file contributes its first face only: drawtext opens face 0 of a
// .ttc collection and has no option to pick another. Fonts with no glyph
// for "A" (symbol and icon fonts) are left out.

// Font is one installed font face, with the vertical metrics the preview
// needs to put its lines where drawtext does (font units).
//
// drawtext advances lines by the hhea line height and puts each baseline
// the hhea ascender (rounded up to a pixel) below the line's top. The
// browser on Windows puts it half the leading plus its own ascent below,
// using the OS/2 win ascent/descent, or the typo ones when the font sets
// USE_TYPO_METRICS, each rounded to a pixel. Most fonts agree; some
// (Consolas, Calibri, Palatino, Gabriola) differ by up to a third of an em.
// fonts.ts's baselineShift closes the gap; checked against real exports of
// every installed font: 0.4px mean error at 100px.
type Font struct {
	Name   string `json:"name"`   // full face name; what a text stores
	Family string `json:"family"` // for grouping and search
	URL    string `json:"url"`    // the file, served to the preview
	// LineHeight is the hhea line advance over the em size: the CSS
	// line-height that matches drawtext's line spacing.
	LineHeight float64 `json:"lineHeight"`
	UnitsPerEm int     `json:"unitsPerEm"`
	Ascent     int     `json:"ascent"`     // hhea ascender: drawtext's
	CSSAscent  int     `json:"cssAscent"`  // the browser's ascent
	CSSDescent int     `json:"cssDescent"` // the browser's descent, positive
	path       string
}

// fontURLPrefix is where internal/media serves font files.
const fontURLPrefix = "/__media/font/"

// FontURL is the URL the preview loads the font called name from.
func FontURL(name string) string { return fontURLPrefix + url.PathEscape(name) }

// FontNameFromURLPath is the font name in a /__media/font/<name> path.
func FontNameFromURLPath(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, fontURLPrefix)
	if !ok {
		return "", false
	}
	name, err := url.PathUnescape(rest)
	return name, err == nil && name != ""
}

var (
	fontsMu    sync.Mutex
	fontsCache []Font
)

// InstalledFonts lists the installed fonts sorted by name, read once and
// cached; refresh re-reads them (after the user installs one).
func InstalledFonts(refresh bool) []Font {
	fontsMu.Lock()
	defer fontsMu.Unlock()
	if fontsCache == nil || refresh {
		fontsCache = readFonts(fontFiles())
	}
	return fontsCache
}

// FontFile is the file of the installed font called name ("" when it isn't
// installed).
func FontFile(name string) string {
	if name == "" {
		return ""
	}
	fs := InstalledFonts(false)
	i, ok := slices.BinarySearchFunc(fs, name, func(f Font, n string) int { return strings.Compare(f.Name, n) })
	if !ok {
		return ""
	}
	return fs[i].path
}

// textFontFile is the file to draw a text in: its font when installed, else
// fallback.
func textFontFile(name, fallback string) string {
	if f := FontFile(name); f != "" {
		return f
	}
	return fallback
}

// readFonts reads each file's first face; unreadable files, fonts without
// Latin letters and repeated names (one face installed twice) are left out.
func readFonts(paths []string) []Font {
	out := []Font{}
	seen := map[string]bool{}
	var buf sfnt.Buffer
	for _, p := range paths {
		f, err := readFont(p, &buf)
		if err != nil || seen[f.Name] {
			continue
		}
		seen[f.Name] = true
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b Font) int { return strings.Compare(a.Name, b.Name) })
	return out
}

var errUnusableFont = errors.New("font: no usable name, metrics or Latin glyphs")

func readFont(path string, buf *sfnt.Buffer) (Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Font{}, err
	}
	c, err := sfnt.ParseCollection(data)
	if err != nil {
		return Font{}, err
	}
	f, err := c.Font(0)
	if err != nil {
		return Font{}, err
	}
	if g, err := f.GlyphIndex(buf, 'A'); err != nil || g == 0 {
		return Font{}, errUnusableFont
	}
	name, err := f.Name(buf, sfnt.NameIDFull)
	name = strings.TrimSpace(name)
	if err != nil || name == "" {
		return Font{}, errUnusableFont
	}
	family, err := f.Name(buf, sfnt.NameIDTypographicFamily)
	if err != nil || family == "" {
		family, _ = f.Name(buf, sfnt.NameIDFamily)
	}
	m, ok := readMetrics(data)
	if !ok {
		return Font{}, errUnusableFont
	}
	cssAsc, cssDesc := m.winAsc, m.winDesc
	if m.useTypo {
		cssAsc, cssDesc = m.typoAsc, -m.typoDesc
	}
	return Font{
		Name:       name,
		Family:     strings.TrimSpace(family),
		URL:        FontURL(name),
		LineHeight: float64(m.hheaAsc-m.hheaDesc+m.hheaGap) / float64(m.upem),
		UnitsPerEm: m.upem,
		Ascent:     m.hheaAsc,
		CSSAscent:  cssAsc,
		CSSDescent: cssDesc,
		path:       path,
	}, nil
}

// fontMetrics are the vertical metrics of a font's first face, in font units
// as stored (hhea and typo descenders negative, win descent positive).
type fontMetrics struct {
	upem                       int
	hheaAsc, hheaDesc, hheaGap int
	typoAsc, typoDesc          int
	winAsc, winDesc            int
	useTypo                    bool
}

// readMetrics reads head, hhea and OS/2 of the first face in data (a font or
// a collection); x/image/font/sfnt doesn't expose OS/2.
func readMetrics(data []byte) (fontMetrics, bool) {
	be := binary.BigEndian
	off := 0
	if len(data) >= 16 && string(data[:4]) == "ttcf" {
		off = int(be.Uint32(data[12:]))
	}
	if off < 0 || off+12 > len(data) {
		return fontMetrics{}, false
	}
	n := int(be.Uint16(data[off+4:]))
	tables := map[string][]byte{}
	for i := range n {
		r := off + 12 + 16*i
		if r+16 > len(data) {
			return fontMetrics{}, false
		}
		o, l := int(be.Uint32(data[r+8:])), int(be.Uint32(data[r+12:]))
		if o < 0 || l < 0 || o+l > len(data) {
			continue
		}
		tables[string(data[r:r+4])] = data[o : o+l]
	}
	head, hhea, os2 := tables["head"], tables["hhea"], tables["OS/2"]
	if len(head) < 20 || len(hhea) < 10 || len(os2) < 78 {
		return fontMetrics{}, false
	}
	i16 := func(b []byte, o int) int { return int(int16(be.Uint16(b[o:]))) }
	u16 := func(b []byte, o int) int { return int(be.Uint16(b[o:])) }
	m := fontMetrics{
		upem:    u16(head, 18),
		hheaAsc: i16(hhea, 4), hheaDesc: i16(hhea, 6), hheaGap: i16(hhea, 8),
		typoAsc: i16(os2, 68), typoDesc: i16(os2, 70),
		winAsc: u16(os2, 74), winDesc: u16(os2, 76),
		useTypo: u16(os2, 62)&0x80 != 0,
	}
	return m, m.upem > 0 && m.hheaAsc-m.hheaDesc+m.hheaGap > 0
}

// isFontFile reports the extensions drawtext and the preview can both load.
func isFontFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".ttf", ".otf", ".ttc":
		return true
	}
	return false
}

var fontChecks sync.Map // name -> error (nil = draws)

// CheckFont reports whether the export can draw text in the installed font
// called name, by drawing one line with it in ffmpeg (cached per name). A
// few fonts (Mongolian Baiti) load but fail in drawtext; the picker refuses
// those rather than letting the export fail later.
func CheckFont(ctx context.Context, name string) error {
	if v, ok := fontChecks.Load(name); ok {
		err, _ := v.(error)
		return err
	}
	file := FontFile(name)
	if file == "" {
		return fmt.Errorf("font %q isn't installed", name)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := capture.RunFFmpeg(ctx, []string{"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:d=0.1",
		"-vf", "drawtext=fontfile=" + filterValue(filepath.ToSlash(file)) + ":text=Ag:fontsize=24:borderw=1:text_align=C:y_align=font",
		"-frames:v", "1", "-f", "null", "-"})
	if ctx.Err() != nil {
		return ctx.Err() // a timeout says nothing about the font: not cached
	}
	if err != nil {
		err = fmt.Errorf("the export can't draw text in %q", name)
	}
	fontChecks.Store(name, err)
	return err
}

// ListFonts is the installed fonts for the text font picker, sorted by name;
// refresh re-reads them (after the user installs a font). JS:
// VideoService.ListFonts.
func (s *VideoService) ListFonts(refresh bool) []Font { return InstalledFonts(refresh) }

// CheckFont is "" when the export can draw text in the installed font
// called name, else why not. JS: VideoService.CheckFont.
func (s *VideoService) CheckFont(name string) string {
	if err := CheckFont(context.Background(), name); err != nil {
		return err.Error()
	}
	return ""
}
