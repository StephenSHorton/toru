package vid

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	effectfx "github.com/StephenSHorton/toru/internal/fx"
)

// project_text.go composites the editor's texts in a project export. The
// editor draws each text (frontend/src/trim/textRender.ts: font, fill,
// outline, shadow, box, spacing, alignment) into a PNG at the output frame's
// pixel size, the same bitmap the player shows, and sends it with where it
// goes (TextImageJSON). Here each PNG is read with movie= and overlaid while
// the text is on screen, after the crop and before the export's scale, as
// drawtext was. The whole-text opacity is applied on top: static as
// colorchannelmixer aa, keyframed per frame by sendcmd (as clip opacity,
// anim.go). Only LGPL filters: movie, loop, setpts, format,
// colorchannelmixer, sendcmd, trim, overlay.
//
// A text without an image is an error, not drawn some other way: every
// export comes from the editor window, which always renders them.

// TextStyleJSON is the part of the editor's text style the export applies
// itself; the rest is already in the image.
type TextStyleJSON struct {
	Opacity     *float64       `json:"opacity,omitempty"`     // 0..1; nil = 1
	OpacityKeys []KeyframeJSON `json:"opacityKeys,omitempty"` // T in ms from the text's start
}

// TextImageJSON is a text drawn by the editor: a PNG (a data URL) and its
// top-left on the output frame (after crop), output px.
type TextImageJSON struct {
	PNG string `json:"png"`
	X   int    `json:"x"`
	Y   int    `json:"y"`
	W   int    `json:"w"`
	H   int    `json:"h"`
}

const pngDataPrefix = "data:image/png;base64,"

// maxTextPNG caps one text image (a full 4K frame of RGBA PNG is far less).
const maxTextPNG = 64 << 20

// ProjectTextFilter is the chain that draws p's texts, to be appended to the
// post chain (after crop): it ends its input with a label and continues
// with the last overlay, so filters after it keep chaining. The PNGs are
// written into work. "" when there are no texts.
func ProjectTextFilter(p ProjectJSON, work string) (string, error) {
	if len(p.Texts) == 0 {
		return "", nil
	}
	fps := p.Canvas.FPS
	if fps <= 0 {
		fps = defaultCanvasFPS
	}
	var b strings.Builder
	n := 0
	for i, t := range p.Texts {
		if t.End <= t.Start {
			continue
		}
		if t.Image == nil || t.Image.PNG == "" {
			return "", fmt.Errorf("text %d (%q) wasn't rendered by the editor; export from the editor window", i+1, firstLine(t.Text))
		}
		path, err := writeTextPNG(t.Image.PNG, work, i)
		if err != nil {
			return "", fmt.Errorf("text %d: %w", i+1, err)
		}
		src := "movie=filename=" + filterValue(filepath.ToSlash(path))
		lenMs := t.End - t.Start
		eof := "repeat"
		var keys []effectfx.Key
		if t.Style != nil {
			for _, k := range t.Style.OpacityKeys {
				keys = append(keys, effectfx.Key{T: float64(k.T), V: k.V, Ease: k.Ease})
			}
		}
		op := 1.0
		if t.Style != nil && t.Style.Opacity != nil {
			op = clampF(*t.Style.Opacity, 0, 1)
		}
		switch {
		case len(keys) > 0:
			// A stream of the image at the canvas rate with local
			// timestamps, so sendcmd can set the alpha per frame, then moved
			// to the text's start; overlay drops it after its end.
			frames := effectfx.Frames(float64(lenMs), fps)
			target := fmt.Sprintf("colorchannelmixer@txo%d", n)
			cmds := effectfx.NumberCommands(target, "aa", frames, func(f int) float64 {
				v, _ := effectfx.ValueAt(keys, effectfx.FrameMs(f, fps), op).(float64)
				return clampF(v, 0, 1)
			})
			send, err := effectfx.SendcmdFilter(cmds, fps, work)
			if err != nil {
				return "", fmt.Errorf("text %d: %w", i+1, err)
			}
			src += fmt.Sprintf(",loop=loop=-1:size=1:start=0,setpts=N/(%s*TB),format=rgba,%s,%s=aa=%s,trim=duration=%s,setpts=PTS+%s/TB",
				formatFPS(fps), send, target, cmds[0].Value, msSeconds(lenMs), msSeconds(t.Start))
			eof = "pass"
		case op < 1:
			src += ",format=rgba,colorchannelmixer=aa=" + num(op)
		}
		in := fmt.Sprintf("[txb%d]", n)
		img := fmt.Sprintf("[txi%d]", n)
		if n == 0 {
			b.WriteString("null")
		}
		b.WriteString(in + ";" + src + img + ";" + in + img)
		fmt.Fprintf(&b, "overlay=x=%d:y=%d:eof_action=%s:enable=%s", t.Image.X, t.Image.Y, eof,
			// While start <= t < end, as the player shows it.
			filterValue(fmt.Sprintf("gte(t,%s)*lt(t,%s)", msSeconds(t.Start), msSeconds(t.End))))
		n++
	}
	return b.String(), nil
}

// writeTextPNG decodes a PNG data URL into work.
func writeTextPNG(dataURL, work string, i int) (string, error) {
	if !strings.HasPrefix(dataURL, pngDataPrefix) {
		return "", errors.New("image isn't a PNG data URL")
	}
	raw, err := base64.StdEncoding.DecodeString(dataURL[len(pngDataPrefix):])
	if err != nil {
		return "", fmt.Errorf("image: %w", err)
	}
	if len(raw) > maxTextPNG || len(raw) < 8 || string(raw[1:4]) != "PNG" {
		return "", errors.New("image isn't a usable PNG")
	}
	path := filepath.Join(work, fmt.Sprintf("text-%d.png", i))
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", fmt.Errorf("image: %w", err)
	}
	return path, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > 30 {
		return string(r[:30]) + "…"
	}
	return s
}
