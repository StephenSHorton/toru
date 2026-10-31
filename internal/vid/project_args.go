package vid

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/StephenSHorton/toru/internal/capture"
	effectfx "github.com/StephenSHorton/toru/internal/fx"
)

// project_args.go turns a v2 ProjectExport into ffmpeg args
// (docs/video-editor-v2.md, Export). No ffmpeg calls; the only files it
// writes are v3 effects' LUTs, into the work folder it's given.
//
// Every clip is its own input ("-ss in -t len -i path"; images loop for the
// clip's length). Video: a black canvas the length of the project, then each
// visible clip overlaid onto it bottom track first, each clip timed with
// setpts and an enable window, so gaps stay black. Audio: a silent bed the
// length of the project, each audible clip delayed to its start and mixed in
// (amix, normalize=0), so gaps stay silent. Then v1's crop, text, preset
// scale and codecs. Only LGPL filters (Toru ships BtbN's LGPL FFmpeg).

// BuildProjectArgs builds the whole export. encoder is the H.264 encoder for
// MP4 output (capture.TrimH264Encoder()); textFilter is TextFilter's chain
// for the project's texts ("" for none); work is a folder for the effects'
// LUT files, which must outlive the ffmpeg run. req must have passed
// Validate.
func BuildProjectArgs(req ProjectExport, encoder string, textFilter string, work string) ([]string, error) {
	p := req.Project
	w, h := evenDim(p.Canvas.Width), evenDim(p.Canvas.Height)
	fps := p.Canvas.FPS
	if fps <= 0 {
		fps = defaultCanvasFPS
	}
	total := ProjectDurationMs(p)
	media := map[string]MediaJSON{}
	for _, m := range p.Media {
		media[m.ID] = m
	}

	args := []string{"-y"}
	inputs := 0
	addInput := func(c ClipJSON, m MediaJSON) int {
		if m.Kind == "image" {
			if strings.EqualFold(filepath.Ext(m.Path), ".gif") {
				args = append(args, "-f", "image2") // first frame as a still, like the preview
			}
			args = append(args, "-loop", "1", "-framerate", formatFPS(fps), "-t", msSeconds(ClipLengthMs(c)), "-i", m.Path)
		} else if c.Hold {
			args = append(args, holdInputArgs(c, m.Path, m.FPS)...)
		} else {
			args = append(args, "-ss", msSeconds(c.In), "-t", msSeconds(c.Out-c.In), "-i", m.Path)
		}
		inputs++
		return inputs - 1
	}

	graph := []string{
		fmt.Sprintf("color=c=black:s=%dx%d:r=%s:d=%s,format=yuv420p[base]", w, h, formatFPS(fps), msSeconds(total)),
		fmt.Sprintf("anullsrc=r=48000:cl=stereo:d=%s,%s[abase]", msSeconds(total), silentFormat),
	}
	videoOut := "[base]"
	var audioIn []string
	layer := 0
	for _, t := range p.Tracks {
		for _, c := range t.Clips {
			m := media[c.Media]
			showVideo := t.Kind == "video" && !t.Hidden && m.Kind != "audio" && clipMaxOpacity(c) > 0
			audible := !t.Muted && (m.Kind == "audio" || m.HasAudio)
			var picks []audioPick
			if audible && !c.Hold { // a freeze frame is silent
				picks = clipAudioPicks(c, m)
			}
			if !showVideo && len(picks) == 0 {
				continue
			}
			in := addInput(c, m)
			if showVideo {
				label := fmt.Sprintf("[v%d]", layer)
				pre, chain, x, y, err := clipVideoChain(in, c, m, w, h, fps, work, layer)
				if err != nil {
					return nil, err
				}
				graph = append(graph, pre...)
				graph = append(graph, chain+label)
				out := fmt.Sprintf("[o%d]", layer)
				graph = append(graph, fmt.Sprintf("%s%soverlay=x=%s:y=%s:eof_action=pass:enable='between(t,%s,%s)'%s",
					videoOut, label, x, y, msSeconds(c.Start), msSeconds(c.Start+ClipLengthMs(c)), out))
				videoOut = out
				layer++
			}
			if len(picks) > 0 {
				label := fmt.Sprintf("[c%d]", len(audioIn))
				graph = append(graph, clipAudioChains(in, c, picks, label)...)
				audioIn = append(audioIn, label)
			}
		}
	}

	var post []string
	frameH := h
	if p.Crop != nil {
		post = append(post, capture.CropFilter(*p.Crop))
		frameH = evenDim(p.Crop.H)
	}
	if textFilter != "" {
		post = append(post, textFilter)
	}
	v1 := EditRequest{Preset: req.Preset, Height: req.Height, Kbps: req.Kbps, OutPath: req.OutPath}
	if target := presetHeight(v1); target > 0 && frameH > target {
		post = append(post, fmt.Sprintf("scale=-2:%d", target))
	}
	if len(post) > 0 {
		graph = append(graph, videoOut+strings.Join(post, ",")+"[vout]")
		videoOut = "[vout]"
	}

	audioOut := "[abase]"
	if len(audioIn) > 0 {
		graph = append(graph, fmt.Sprintf("[abase]%samix=inputs=%d:duration=first:normalize=0[aout]", strings.Join(audioIn, ""), len(audioIn)+1))
		audioOut = "[aout]"
	}

	fc, err := filterComplexArgs(strings.Join(graph, ";"), work)
	if err != nil {
		return nil, err
	}
	args = append(args, fc...)
	args = append(args, "-map", videoOut, "-map", audioOut)
	args = append(args, editCodecArgs(v1, encoder)...)
	args = append(args, capture.ContainerFlags(req.OutPath)...)
	return append(args, req.OutPath), nil
}

// maxInlineGraph is the longest filtergraph passed on the command line;
// Windows caps a whole command line at 32,767 characters, and effect-heavy
// projects (each glitch is ~3.5 KB) can pass that.
const maxInlineGraph = 16000

// filterComplexArgs passes graph as -filter_complex, or, when it's longer
// than maxInlineGraph, writes it to work (named by its content) and passes
// the file with -/filter_complex (ffmpeg 7+; Toru ships 9).
func filterComplexArgs(graph, work string) ([]string, error) {
	if len(graph) <= maxInlineGraph || work == "" {
		return []string{"-filter_complex", graph}, nil
	}
	sum := sha1.Sum([]byte(graph))
	path := filepath.Join(work, "graph-"+hex.EncodeToString(sum[:8])+".txt")
	if err := os.WriteFile(path, []byte(graph), 0o644); err != nil {
		return nil, fmt.Errorf("export: write filtergraph: %w", err)
	}
	return []string{"-/filter_complex", path}, nil
}

// clipVideoChain is a clip's picture from input in, ready to overlay: timed
// (speed, then shifted to the clip's start), at the canvas rate, with its
// effects, scaled to its transform, with alpha and opacity. It returns any
// statements the effects need first (a v3 stack is a small graph of its
// own; layer keeps its labels unique), the chain (without its output label)
// and where its top-left goes on the canvas (overlay x/y arguments).
func clipVideoChain(in int, c ClipJSON, m MediaJSON, w, h int, fps float64, work string, layer int) ([]string, string, string, string, error) {
	head := fmt.Sprintf("[%d:v]", in)
	var pre []string
	if m.Untagged709 && m.Kind == "video" {
		// Read an untagged HD source as BT.709 (markUntagged709): the tag
		// rides on the frames into every conversion and the output.
		cs := fmt.Sprintf("[cs%d]", layer)
		pre = append(pre, head+untagged709Filter+cs)
		head = cs
	}
	parts := []string{}
	switch {
	case m.Kind == "image":
		parts = append(parts, "setpts=PTS-STARTPTS")
	case wantsSmooth(c):
		// Smooth slow motion is a small graph of its own (time_args.go).
		sm := fmt.Sprintf("[sm%d]", in)
		pre = append(pre, clipSmoothGraph(head, sm, c, fps, fmt.Sprintf("sm%d", in))...)
		head = sm
	default:
		parts = append(parts, clipTiming(c)...) // speed, curve or freeze (time_args.go)
	}
	parts = append(parts, "fps="+formatFPS(fps))
	if c.Effects != nil {
		// v3: the effect stack (internal/fx), as its own graph between the
		// timing and the scale.
		fxIn, fxOut := fmt.Sprintf("[fx%din]", layer), fmt.Sprintf("[fx%dout]", layer)
		stmts, err := effectfx.StackGraph(stackOf(c), effectfx.Ctx{W: m.Width, H: m.Height, FPS: fps, Frames: effectfx.Frames(float64(ClipLengthMs(c)), fps)}, work, fmt.Sprintf("fx%d", layer), fxIn, fxOut)
		if err != nil {
			return nil, "", "", "", err
		}
		if stmts != nil {
			pre = append(pre, head+strings.Join(parts, ",")+fxIn)
			pre = append(pre, stmts...)
			head, parts = fxOut, nil
		}
	} else if f := ClipVideoFilter(c, m.Height); f != "" {
		parts = append(parts, f)
	}
	place, x, y, err := clipPlacement(c, m.Width, m.Height, w, h, fps, work, layer)
	if err != nil {
		return nil, "", "", "", err
	}
	parts = append(parts, place...)
	parts = append(parts, fmt.Sprintf("setpts=PTS+%s/TB", msSeconds(c.Start)))
	return pre, head + strings.Join(parts, ","), x, y, nil
}

// stackOf is a v3 clip's effects as the export reads them.
func stackOf(c ClipJSON) []effectfx.Effect {
	out := make([]effectfx.Effect, 0, len(c.Effects))
	for _, e := range c.Effects {
		amount := 1.0
		if e.Amount != nil {
			amount = *e.Amount
		}
		var keys map[string][]effectfx.Key
		for id, ks := range e.Keys {
			if keys == nil {
				keys = map[string][]effectfx.Key{}
			}
			for _, k := range ks {
				keys[id] = append(keys[id], effectfx.Key{T: float64(k.T), V: k.V, Ease: k.Ease})
			}
		}
		out = append(out, effectfx.Effect{Type: e.Type, Enabled: e.Enabled, Params: e.Params, Group: e.Group, Amount: amount, Keys: keys})
	}
	return out
}

// ClipSize is a clip's size on the canvas: scale 1 fits the whole picture
// inside the canvas (keeping its aspect), other scales multiply that. Even,
// at least 2x2.
func ClipSize(srcW, srcH, canvasW, canvasH int, scale float64) (int, int) {
	if srcW <= 0 || srcH <= 0 {
		return evenDim(canvasW), evenDim(canvasH)
	}
	fit := math.Min(float64(canvasW)/float64(srcW), float64(canvasH)/float64(srcH)) * scale
	return evenDim(int(math.Round(float64(srcW) * fit))), evenDim(int(math.Round(float64(srcH) * fit)))
}

// clipTransform is the clip's transform, with a zero one (a clip built
// without a transform) meaning centred, fit, opaque.
func clipTransform(c ClipJSON) TransformJSON {
	t := c.Transform
	if t == (TransformJSON{}) {
		return TransformJSON{X: 0.5, Y: 0.5, Scale: 1, Opacity: 1}
	}
	if t.Scale <= 0 || math.IsNaN(t.Scale) {
		t.Scale = 1
	}
	return t
}

func clipOpacity(c ClipJSON) float64 {
	return clampF(clipTransform(c).Opacity, 0, 1)
}

// audioPick is one audio stream of a clip's source at a level (percent).
type audioPick struct{ index, volume int }

// clipAudioPicks is which of the source's audio streams a clip plays: its v1
// per-source-track levels when it has any (muted or 0% ones left out), else
// the mix track at 100%.
func clipAudioPicks(c ClipJSON, m MediaJSON) []audioPick {
	if len(c.Levels) > 0 {
		var out []audioPick
		for _, l := range c.Levels {
			if !l.Muted && l.Volume > 0 {
				out = append(out, audioPick{l.Index, l.Volume})
			}
		}
		return out
	}
	if len(m.Tracks) == 0 {
		return []audioPick{{0, 100}}
	}
	return []audioPick{{mixTrack(MediaInfo{Tracks: m.Tracks}), 100}}
}

// clipAudioChains leaves a clip's audio on label: its picked streams at
// their levels (summed when several), then speed, volume and fades
// (ClipAudioFilter), delayed to the clip's start.
func clipAudioChains(in int, c ClipJSON, picks []audioPick, label string) []string {
	src := fmt.Sprintf("[ca%d]", in)
	var chains []string
	var mixIn strings.Builder
	for j, pk := range picks {
		chain := fmt.Sprintf("[%d:a:%d]asetpts=PTS-STARTPTS,%s", in, pk.index, silentFormat)
		if pk.volume != 100 {
			chain += fmt.Sprintf(",volume=%.2f", float64(pk.volume)/100)
		}
		if len(picks) == 1 {
			chains = append(chains, chain+src)
			continue
		}
		l := fmt.Sprintf("[ca%d_%d]", in, j)
		chains = append(chains, chain+l)
		mixIn.WriteString(l)
	}
	if len(picks) > 1 {
		chains = append(chains, fmt.Sprintf("%samix=inputs=%d:duration=longest:normalize=0%s", mixIn.String(), len(picks), src))
	}
	parts := []string{}
	if cv := clipCurve(c); cv != nil {
		// A ramp's sound is timed piece by piece (time_args.go); the rest of
		// the chain then runs at 1x.
		timed := fmt.Sprintf("[cr%d]", in)
		chains = append(chains, rampAudioGraph(src, timed, cv, fmt.Sprintf("cr%d", in))...)
		src = timed
		c.Speed = 1
	}
	if f := ClipAudioFilter(c, float64(ClipLengthMs(c))/1000); f != "" {
		parts = append(parts, f)
	}
	if c.Start > 0 {
		parts = append(parts, fmt.Sprintf("adelay=delays=%d:all=1", c.Start))
	}
	if len(parts) == 0 {
		parts = append(parts, "anull")
	}
	return append(chains, src+strings.Join(parts, ",")+label)
}
