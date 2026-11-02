// Package voice is Toru's one list of voice clean-up presets: the ffmpeg
// filter chains that take noise out of a microphone and, for the strongest
// one, polish it for a video. The replay recorder applies the live ones to
// the mic as it records (capture.ReplayAudio.MicNoise); the editor renders
// any of them onto an audio clip (voicesvc), so a name means the same sound
// in both places.
package voice

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Preset ids. Off, Light and Strong are the recorder's original levels and
// keep their chains, so a mic set to "light" sounds as it always did.
const (
	Off      = "off"
	Light    = "light"
	Strong   = "strong"
	Clean    = "voice"
	CleanMax = "voicemax"
	Polish   = "polish"
)

// Preset describes one entry for the pickers.
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Live: the chain can run on a mic while it records (no model file to
	// find, no second pass over the whole file).
	Live bool `json:"live"`
}

var presets = []Preset{
	{Off, "Off", "The sound as recorded.", true},
	{Light, "Light", "Takes out steady background noise: fans, hum, hiss.", true},
	{Strong, "Strong", "Light, plus a gate that quiets keyboard and room sound between words.", true},
	{Clean, "Clean voice", "A speech model keeps your voice and removes nearly everything else.", false},
	{CleanMax, "Clean voice max", "Clean voice, then noise reduction and a gate for very noisy rooms.", false},
	{Polish, "Voice polish", "Clean voice, then EQ, de-essing and compression, evened out to -16 LUFS for video.", false},
}

// Presets lists every preset in picker order.
func Presets() []Preset { return append([]Preset(nil), presets...) }

// Find returns the preset with id.
func Find(id string) (Preset, bool) {
	for _, p := range presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}

// IsLive says whether id is a preset the recorder can apply live.
func IsLive(id string) bool {
	p, ok := Find(id)
	return ok && p.Live
}

// Loudness targets for Polish (two-pass loudnorm, then a limiter).
const (
	PolishLUFS   = -16.0
	PolishTP     = -1.5
	PolishLRA    = 11.0
	polishLimitA = 0.89 // about -1 dBFS
)

// Chain is the filter chain for a preset, "" for Off. model is the .rnnn
// file the speech-model presets read (ModelPath); live presets ignore it.
// Polish's chain stops before loudness: Loudness adds that once the chain's
// output is measured. Unknown ids are an error.
func Chain(id, model string) (string, error) {
	rnn := func() (string, error) {
		if model == "" {
			return "", fmt.Errorf("voice: %s needs the speech model", id)
		}
		return arnndn(model), nil
	}
	switch id {
	case Off, "":
		return "", nil
	case Light:
		return "highpass=f=80,afftdn=nr=12:nf=-50:tn=1", nil
	case Strong:
		return "highpass=f=100,afftdn=nr=24:nf=-45:tn=1,agate=threshold=0.01:ratio=4:attack=5:release=300:range=0.08", nil
	case Clean:
		f, err := rnn()
		if err != nil {
			return "", err
		}
		return "highpass=f=80," + f + ",agate=threshold=0.006:ratio=2:attack=5:release=300:range=0.25", nil
	case CleanMax:
		f, err := rnn()
		if err != nil {
			return "", err
		}
		return "highpass=f=100," + f + ",afftdn=nr=12:nf=-50:tn=1,agate=threshold=0.01:ratio=4:attack=5:release=300:range=0.06", nil
	case Polish:
		f, err := rnn()
		if err != nil {
			return "", err
		}
		return "highpass=f=80," + f +
			",agate=threshold=0.008:ratio=3:attack=5:release=250:range=0.1" +
			",equalizer=f=200:t=q:w=1:g=-2,equalizer=f=3500:t=q:w=1.2:g=3" +
			",deesser=i=0.4" +
			",acompressor=threshold=0.08:ratio=3:attack=10:release=200:makeup=2", nil
	}
	return "", fmt.Errorf("voice: unknown preset %q", id)
}

// NeedsLoudness says whether a preset ends with Polish's loudness pass.
func NeedsLoudness(id string) bool { return id == Polish }

// Measured is loudnorm's first-pass report (print_format=json).
type Measured struct {
	I, TP, LRA, Thresh, Offset string
}

// LoudnessMeasure is the filter that measures a chain's output for the
// second pass; its JSON goes to stderr.
func LoudnessMeasure() string {
	return fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:print_format=json", PolishLUFS, PolishTP, PolishLRA)
}

// Loudness is the second pass: linear gain to the target from the measured
// values, back to 48 kHz (loudnorm works at 192 kHz), then a limiter.
func Loudness(m Measured) string {
	return fmt.Sprintf("loudnorm=I=%g:TP=%g:LRA=%g:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true,aresample=48000,alimiter=limit=%g:level=false",
		PolishLUFS, PolishTP, PolishLRA, m.I, m.TP, m.LRA, m.Thresh, m.Offset, polishLimitA)
}

var errSilent = errors.New("voice: silent")

// IsSilent says whether err is ParseMeasured's report of a silent input.
func IsSilent(err error) bool { return errors.Is(err, errSilent) }

// ParseMeasured reads loudnorm's JSON block from ffmpeg's stderr.
func ParseMeasured(stderr string) (Measured, error) {
	i := strings.LastIndex(stderr, "{")
	j := strings.LastIndex(stderr, "}")
	if i < 0 || j < i {
		return Measured{}, errors.New("voice: no loudness report")
	}
	block := stderr[i : j+1]
	get := func(key string) string {
		k := strings.Index(block, `"`+key+`"`)
		if k < 0 {
			return ""
		}
		rest := block[k+len(key)+2:]
		a := strings.Index(rest, `"`)
		if a < 0 {
			return ""
		}
		b := strings.Index(rest[a+1:], `"`)
		if b < 0 {
			return ""
		}
		return rest[a+1 : a+1+b]
	}
	m := Measured{I: get("input_i"), TP: get("input_tp"), LRA: get("input_lra"), Thresh: get("input_thresh"), Offset: get("target_offset")}
	// Silence measures as -inf: there is nothing to even out.
	if strings.Contains(m.I, "inf") {
		return Measured{}, errSilent
	}
	for _, v := range []string{m.I, m.TP, m.LRA, m.Thresh, m.Offset} {
		if v == "" || strings.ContainsAny(v, ":,;[]'\\") {
			return Measured{}, fmt.Errorf("voice: bad loudness report %q", block)
		}
	}
	return m, nil
}

// arnndn is the speech-model filter reading a model file.
func arnndn(model string) string { return "arnndn=m=" + FilterPath(model) }

// FilterPath quotes a file path as a filter option value inside a graph:
// forward slashes, the drive colon escaped, in single quotes (an apostrophe
// closes the quote, is escaped, and reopens it).
func FilterPath(p string) string {
	p = filepath.ToSlash(p)
	p = strings.ReplaceAll(p, `:`, `\:`)
	p = strings.ReplaceAll(p, `'`, `'\''`)
	return "'" + p + "'"
}

//go:embed models/sh.rnnn
var model []byte

var (
	modelMu   sync.Mutex
	modelPath string
)

// modelDir is where the model is written for ffmpeg to read; a var so tests
// can move it.
var modelDir = func() string {
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "toru", "voice")
	}
	return filepath.Join(os.TempDir(), "toru", "voice")
}

// ModelPath writes the embedded speech model to the cache folder (once; the
// name carries its hash) and returns its path.
func ModelPath() (string, error) {
	modelMu.Lock()
	defer modelMu.Unlock()
	if modelPath != "" {
		if _, err := os.Stat(modelPath); err == nil {
			return modelPath, nil
		}
	}
	p := filepath.Join(modelDir(), "sh-"+ModelSHA256()[:12]+".rnnn")
	if b, err := os.ReadFile(p); err != nil || len(b) != len(model) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", err
		}
		tmp := p + ".part"
		if err := os.WriteFile(tmp, model, 0o644); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, p); err != nil {
			_ = os.Remove(tmp)
			return "", err
		}
	}
	modelPath = p
	return p, nil
}

// ModelSHA256 is the embedded model's hash (models/README.md).
func ModelSHA256() string {
	sum := sha256.Sum256(model)
	return hex.EncodeToString(sum[:])
}
