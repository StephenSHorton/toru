// Package voicesvc renders the voice presets (internal/voice) onto audio for
// the editor. Applying a preset makes a processed copy of the clip's sound
// with ffmpeg, and the clip then plays that file in the preview and in the
// export, so what you hear is what you get. Audition renders a few seconds
// in every preset for an A/B.
package voicesvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/voice"
)

// chainVersion is in every cache key: bump it when a chain or the render
// changes, so old copies aren't reused.
const chainVersion = 1

// Level is one audio stream of the source at a volume, as a clip's
// per-track levels pick it (vid.TrackLevel).
type Level struct {
	Index  int  `json:"index"`
	Volume int  `json:"volume"` // percent
	Muted  bool `json:"muted"`
}

// RenderRequest asks for a source's sound through a preset.
type RenderRequest struct {
	Path   string  `json:"path"`   // the source file
	Stream int     `json:"stream"` // the audio stream to use when Levels is empty
	Levels []Level `json:"levels"` // the clip's per-track levels, if it has any
	Preset string  `json:"preset"`
	// Start and Duration (s) render a section (Audition); Duration 0 = the
	// whole file from 0, so the copy lines up with the source second for
	// second.
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
}

// Service is the Wails-bound voice preset API.
type Service struct {
	mu   sync.Mutex
	busy map[string]*sync.Mutex // cache path -> lock
}

// New returns the service.
func New() *Service { return &Service{busy: map[string]*sync.Mutex{}} }

// Presets lists the presets in picker order.
func (s *Service) Presets() []voice.Preset { return voice.Presets() }

// outDir is where rendered copies live. Applied copies are referenced by
// saved projects, so this is the user's local app data, not the temp
// folder. A var so tests can move it.
var outDir = func() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "toru", "voice", "renders")
	}
	return filepath.Join(os.TempDir(), "toru", "voice", "renders")
}

// Render returns the path of req's source sound through req.Preset as a
// FLAC file, rendering it on the first call (later calls with the same
// source, levels, preset and range reuse it). The source's size and mtime
// are in the key, so a replaced file renders again.
func (s *Service) Render(req RenderRequest) (string, error) {
	if _, ok := voice.Find(req.Preset); !ok {
		return "", fmt.Errorf("voice: unknown preset %q", req.Preset)
	}
	if req.Start < 0 || req.Duration < 0 {
		return "", errors.New("voice: negative range")
	}
	fi, err := os.Stat(req.Path)
	if err != nil {
		return "", err
	}
	out := filepath.Join(outDir(), cacheKey(req, fi)+".flac")
	lk := s.lock(out)
	lk.Lock()
	defer lk.Unlock()
	if _, err := os.Stat(out); err == nil {
		return out, nil
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return "", err
	}
	timeout := 30 * time.Minute
	if req.Duration > 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	tmp := strings.TrimSuffix(out, ".flac") + ".part.flac"
	defer func() { _ = os.Remove(tmp) }()
	if err := render(ctx, req, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, out); err != nil {
		return "", err
	}
	return out, nil
}

// Audition renders a section of the source in every preset at once (a few
// at a time) and returns their paths by preset id. A preset that fails is
// left out; an error only when all of them do.
func (s *Service) Audition(req RenderRequest) (map[string]string, error) {
	if req.Duration <= 0 {
		return nil, errors.New("voice: audition needs a duration")
	}
	ps := voice.Presets()
	paths := make([]string, len(ps))
	errs := make([]error, len(ps))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for i, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			r := req
			r.Preset = p.ID
			paths[i], errs[i] = s.Render(r)
		}()
	}
	wg.Wait()
	out := map[string]string{}
	for i, p := range ps {
		if errs[i] == nil {
			out[p.ID] = paths[i]
		}
	}
	if len(out) == 0 {
		return nil, errors.Join(errs...)
	}
	return out, nil
}

func (s *Service) lock(key string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l, ok := s.busy[key]; ok {
		return l
	}
	l := &sync.Mutex{}
	s.busy[key] = l
	return l
}

func cacheKey(req RenderRequest, fi os.FileInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "v%d|%s|%d|%d|%d|%s|%.3f|%.3f", chainVersion, strings.ToLower(filepath.Clean(req.Path)),
		fi.Size(), fi.ModTime().UnixNano(), req.Stream, req.Preset, req.Start, req.Duration)
	for _, l := range req.Levels {
		fmt.Fprintf(&b, "|%d:%d:%t", l.Index, l.Volume, l.Muted)
	}
	h := sha256.Sum256([]byte(b.String()))
	return req.Preset + "-" + hex.EncodeToString(h[:8])
}

// preroll is how much sound before an audition section the chain hears
// first, so the adaptive filters (afftdn, the gate, the speech model) have
// settled as they would have in the whole file; it is cut off again.
const preroll = 2.0

// sourceGraph is the filter graph from the input's audio to [src]: the
// clip's picked streams at their levels, summed, as the export mixes them
// (vid clipAudioPicks), padded at the front so sample 0 is the file's time
// 0 (a stream that starts a little after the video still lines up).
func sourceGraph(req RenderRequest) string {
	type pick struct{ index, volume int }
	var picks []pick
	if len(req.Levels) > 0 {
		for _, l := range req.Levels {
			if !l.Muted && l.Volume > 0 {
				picks = append(picks, pick{l.Index, l.Volume})
			}
		}
	} else {
		picks = []pick{{req.Stream, 100}}
	}
	const format = "aformat=sample_fmts=fltp:sample_rates=48000:channel_layouts=stereo"
	if len(picks) == 0 {
		return "anullsrc=r=48000:cl=stereo,atrim=0:0.1[src]"
	}
	var chains []string
	var mix strings.Builder
	for j, p := range picks {
		c := fmt.Sprintf("[0:a:%d]aresample=async=1:first_pts=0,%s", p.index, format)
		if p.volume != 100 {
			c += fmt.Sprintf(",volume=%.2f", float64(p.volume)/100)
		}
		if len(picks) == 1 {
			return c + "[src]"
		}
		l := fmt.Sprintf("[p%d]", j)
		chains = append(chains, c+l)
		mix.WriteString(l)
	}
	chains = append(chains, fmt.Sprintf("%samix=inputs=%d:duration=longest:normalize=0[src]", mix.String(), len(picks)))
	return strings.Join(chains, ";")
}

// render runs the preset over the request's range into out (FLAC, 48 kHz
// stereo, 24-bit so quiet passages keep their detail).
func render(ctx context.Context, req RenderRequest, out string) error {
	model := ""
	if !voice.IsLive(req.Preset) {
		p, err := voice.ModelPath()
		if err != nil {
			return fmt.Errorf("voice: speech model: %w", err)
		}
		model = p
	}
	chain, err := voice.Chain(req.Preset, model)
	if err != nil {
		return err
	}
	// A section starts preroll early and is trimmed after the chain.
	in := []string{"-nostdin", "-y"}
	start := 0.0
	if req.Duration > 0 {
		start = max(0, req.Start-preroll)
		in = append(in, "-ss", ff(start), "-t", ff(req.Start-start+req.Duration))
	}
	in = slices.Clip(append(in, "-i", req.Path)) // appended to twice below
	pre := sourceGraph(req)
	body := "[src]" + orNull(chain)
	if req.Duration > 0 {
		body += fmt.Sprintf(",atrim=start=%s,asetpts=PTS-STARTPTS", ff(req.Start-start))
	}
	if voice.NeedsLoudness(req.Preset) {
		m, err := measure(ctx, append(in, "-filter_complex", pre+";"+body+","+voice.LoudnessMeasure()+"[out]", "-map", "[out]", "-f", "null", "-"))
		switch {
		case voice.IsSilent(err):
			// Nothing to even out: the chain alone.
		case err != nil:
			return err
		default:
			body += "," + voice.Loudness(m)
		}
	}
	args := append(in, "-filter_complex", pre+";"+body+"[out]", "-map", "[out]", "-vn", "-sn", "-dn",
		"-ar", "48000", "-c:a", "flac", "-sample_fmt", "s32", out)
	return capture.RunFFmpeg(ctx, args)
}

func orNull(chain string) string {
	if chain == "" {
		return "anull"
	}
	return chain
}

// measure runs a loudnorm first pass and parses its report.
func measure(ctx context.Context, args []string) (voice.Measured, error) {
	bin, err := capture.LocateFFmpeg()
	if err != nil {
		return voice.Measured{}, err
	}
	cmd := exec.CommandContext(ctx, bin, append([]string{"-hide_banner"}, args...)...)
	capture.PrepareFFmpegCmd(cmd)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return voice.Measured{}, fmt.Errorf("voice: loudness pass: %w\n%s", err, tail(string(b), 2000))
	}
	return voice.ParseMeasured(string(b))
}

func ff(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
