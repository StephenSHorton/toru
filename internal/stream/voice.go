package stream

import (
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// voice.go keeps whisper's made-up lines out of call transcripts. On a real
// microphone the call window's speech cutter (a loudness gate) also sends
// stretches of typing, breathing, clicks and room noise, and whisper, given
// audio with no words in it, writes lines anyway ("Thank you.", "Thanks for
// watching!"). So each stretch is checked for a human voice before it is
// transcribed, and the text that comes back is checked against the audio.
//
// A voice is voiced sound: a pitch (the vocal folds open and close 70 to
// 400 times a second, so the waveform repeats), well above the stretch's own
// noise floor, held for at least 100 ms (one syllable). Typing and clicks
// are too short and don't repeat, breathing and fan noise don't repeat, and
// a steady hum is not above the floor. Measures are relative to the stretch
// so a mic's gain doesn't matter. Each test leans towards keeping: a little
// clearly voiced sound anywhere passes, since dropping real speech is worse
// than one stray line.

const (
	voiceFrame    = 320 // 20 ms at 16 kHz
	voiceRun      = 5   // frames in a row (100 ms) to count as voice
	voicePeriodic = 0.5 // normalised autocorrelation at the pitch lag
	voiceAboveDB  = 9   // a voiced frame is this much above the floor
	voiceMinLevel = 60  // and at least this loud (int16 RMS, about -55 dBFS)

	// minVoiced is the voiced time a stretch needs to be transcribed at all.
	minVoiced = voiceRun * 20 * time.Millisecond
	// weakVoiced: under this much voice, a line that is only one of whisper's
	// stock phrases ("Thank you.") is taken as made up.
	weakVoiced = 300 * time.Millisecond
)

// voiceInfo is what measureVoice found in a stretch.
type voiceInfo struct {
	voiced  time.Duration // in runs of voiced frames of at least 100 ms
	span    time.Duration // from the first such run's start to the last one's end
	longest time.Duration // the longest run
	floor   float64       // the stretch's noise floor (int16 RMS)
}

// measureVoice finds the voiced sound in 16 kHz mono samples.
func measureVoice(pcm []int16) voiceInfo {
	n := len(pcm) / voiceFrame
	if n == 0 {
		return voiceInfo{}
	}
	rms := make([]float64, n)
	for f := range n {
		var e float64
		for _, v := range pcm[f*voiceFrame : (f+1)*voiceFrame] {
			e += float64(v) * float64(v)
		}
		rms[f] = math.Sqrt(e / voiceFrame)
	}
	// The floor: the quiet end of the stretch (the cutter keeps 300 ms
	// before the speech and some after it, so there is usually some).
	sorted := slices.Clone(rms)
	slices.Sort(sorted)
	floor := sorted[n/10]
	level := max(voiceMinLevel, floor*math.Pow(10, voiceAboveDB/20.0))

	info := voiceInfo{floor: floor}
	first, last, run := -1, -1, 0
	end := func(f int) { // a run ended just before frame f
		if run >= voiceRun {
			d := time.Duration(run) * 20 * time.Millisecond
			info.voiced += d
			info.longest = max(info.longest, d)
			if first < 0 {
				first = f - run
			}
			last = f
		}
		run = 0
	}
	for f := range n {
		if rms[f] >= level && periodic(pcm, f*voiceFrame) {
			run++
			continue
		}
		end(f)
	}
	end(n)
	if first >= 0 {
		info.span = time.Duration(last-first) * 20 * time.Millisecond
	}
	return info
}

// periodic says whether the 40 ms from sample at repeats at a pitch between
// 70 and 400 Hz. It works at 8 kHz (pairs of samples averaged), which keeps
// the pitch and costs a quarter. Low rumble (a breath on the mic, wind, a
// bump) looks alike from one sample to the next without having a pitch, so
// the pitch peak only counts once the correlation has first fallen away.
func periodic(pcm []int16, at int) bool {
	const win = 320                // 40 ms at 8 kHz
	const minLag, maxLag = 20, 114 // 400 Hz .. 70 Hz at 8 kHz
	const fallen = 0.25
	var x [win + maxLag]float64
	have := 0
	var mean float64
	for i := range x {
		j := at + 2*i
		if j+1 >= len(pcm) {
			break
		}
		x[i] = (float64(pcm[j]) + float64(pcm[j+1])) / 2
		mean += x[i]
		have++
	}
	if have < win+minLag {
		return false
	}
	mean /= float64(have)
	for i := range have {
		x[i] -= mean
	}
	dipped, best := false, 0.0
	for lag := 2; lag <= maxLag && win+lag <= have; lag++ {
		var xy, xx, yy float64
		for i := range win {
			a, b := x[i], x[i+lag]
			xy += a * b
			xx += a * a
			yy += b * b
		}
		r := 0.0
		if xx > 0 && yy > 0 {
			r = xy / math.Sqrt(xx*yy)
		}
		if r < fallen {
			dipped = true
		}
		if dipped && lag >= minLag {
			best = max(best, r)
		}
	}
	return best >= voicePeriodic
}

// --- text checks ----------------------------------------------------------------

// neverSaid are lines whisper writes from its training on subtitled video,
// never said in a call: dropped from everyone's lines (cleanText).
var (
	neverSaidLines = map[string]bool{
		"thanks for watching":                          true,
		"thank you for watching":                       true,
		"thanks for watching and see you next time":    true,
		"thank you for watching and see you next time": true,
		"thank you so much for watching":               true,
		"thanks for watching dont forget to subscribe": true,
		"please subscribe":                             true,
		"please like and subscribe":                    true,
		"like and subscribe":                           true,
		"dont forget to like and subscribe":            true,
		"subscribe to my channel":                      true,
		"please subscribe to my channel":               true,
		"see you in the next video":                    true,
		"ill see you in the next video":                true,
	}
	// Subtitle credits: a line starting with one ("Subtitles by the Amara.org
	// community"), or naming amara.org anywhere.
	neverSaidWithin = regexp.MustCompile(`(?i)amara\.org|^(subtitles|subtitled|subtitling|captions|captioned|captioning|transcription|transcribed|translated|translation) by\b`)
)

// stockLines are short lines whisper often invents from noise that people
// also really say: dropped only from our own lines, when the audio had
// little voice in it.
var stockLines = map[string]bool{
	"thank you": true, "thank you very much": true, "thank you so much": true,
	"thanks": true, "bye": true, "bye bye": true, "goodbye": true,
	"you": true, "so": true, "the end": true, "oh": true, "hmm": true,
}

// phraseKey is t lower-cased with only its letters, digits and single
// spaces left: "Thanks for watching!" -> "thanks for watching".
func phraseKey(t string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(t) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		case r == '\'' || r == '’':
		default:
			space = true
		}
	}
	return b.String()
}

func neverSaid(t string) bool {
	return neverSaidLines[phraseKey(t)] || neverSaidWithin.MatchString(t)
}

// Text the audio couldn't hold: fast speech is about 15 to 20 characters a
// second; allow 30, plus some for the unvoiced sounds at either end.
const (
	maxCharsPerSecond = 30
	maxCharsExtra     = 15
)

// madeUp says why our own line (already through cleanText) doesn't fit the
// voice measured in its audio, or "" when it fits.
func madeUp(text string, v voiceInfo) string {
	if v.voiced < weakVoiced && stockLines[phraseKey(text)] {
		return "stock phrase on little voice"
	}
	limit := maxCharsExtra + int(maxCharsPerSecond*(v.span+400*time.Millisecond).Seconds())
	if n := len([]rune(text)); n > limit {
		return "more text than the voice could hold"
	}
	return ""
}
