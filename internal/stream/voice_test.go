package stream

import (
	"encoding/base64"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/transcript"
)

// testdata/speech.wav is the first 5 s of whisper.cpp's samples/jfk.wav
// (public domain): "And so my fellow Americans, ask not ...", 16 kHz mono.
func speechSample(t *testing.T) []int16 {
	t.Helper()
	raw, err := os.ReadFile("testdata/speech.wav")
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, (len(raw)-44)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[44+2*i:]))
	}
	return pcm
}

const sr = transcript.SampleRate

func secs(d float64) int { return int(d * sr) }

func clip16(v float64) int16 { return int16(max(-32768, min(32767, math.Round(v)))) }

// noise is white noise at an RMS level.
func noise(r *rand.Rand, n int, level float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = r.NormFloat64() * level
	}
	return out
}

func mix(parts ...[]float64) []int16 {
	n := 0
	for _, p := range parts {
		n = max(n, len(p))
	}
	out := make([]int16, n)
	for i := range out {
		var v float64
		for _, p := range parts {
			if i < len(p) {
				v += p[i]
			}
		}
		out[i] = clip16(v)
	}
	return out
}

func floats(pcm []int16, gain float64) []float64 {
	out := make([]float64, len(pcm))
	for i, v := range pcm {
		out[i] = float64(v) * gain
	}
	return out
}

// typing is key clicks: a sharp, quickly fading burst (noise and a ringing
// 2.4 kHz tone) every 120 to 250 ms, as hard as speech.
func typing(r *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for at := secs(0.1); at < n; at += secs(0.12 + 0.13*r.Float64()) {
		amp := 6000 + 6000*r.Float64()
		for i := 0; i < secs(0.03) && at+i < n; i++ {
			env := math.Exp(-float64(i) / (0.004 * sr))
			out[at+i] += amp * env * (r.NormFloat64() + math.Sin(2*math.Pi*2400*float64(i)/sr))
		}
	}
	return out
}

// breathing is low, rushing noise swelling in and out (in and out breaths
// of about a second), as loud as quiet speech.
func breathing(r *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	var lp float64
	for i := range out {
		lp = 0.9*lp + 0.1*r.NormFloat64()
		phase := math.Mod(float64(i)/sr, 2.2) / 1.1 // 0..2: a breath in, a breath out
		env := math.Sin(math.Pi * math.Mod(phase, 1))
		out[i] = lp * 9000 * env * env
	}
	return out
}

// thumps are a desk being knocked: a short 90 Hz thud every half second.
func thumps(n int) []float64 {
	out := make([]float64, n)
	for at := secs(0.2); at < n; at += secs(0.5) {
		for i := 0; i < secs(0.06) && at+i < n; i++ {
			out[at+i] += 12000 * math.Exp(-float64(i)/(0.015*sr)) * math.Sin(2*math.Pi*90*float64(i)/sr)
		}
	}
	return out
}

// darkBreath is a breath right on the mic: deep, rumbling noise.
func darkBreath(r *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	var lp float64
	for i := range out {
		lp = 0.99*lp + 0.1*r.NormFloat64()
		phase := math.Mod(float64(i)/sr, 2.2) / 1.1
		env := math.Sin(math.Pi * math.Mod(phase, 1))
		out[i] = lp * 9000 * env * env
	}
	return out
}

// rumble is wind or handling noise: very low, slowly swelling.
func rumble(r *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	var lp float64
	for i := range out {
		lp = 0.995*lp + 0.1*r.NormFloat64()
		out[i] = lp * 3000 * (1 + math.Sin(2*math.Pi*0.5*float64(i)/sr))
	}
	return out
}

// bumps are the mic being bumped: 400 ms swells of a 55 Hz boom.
func bumps(r *rand.Rand, n int) []float64 {
	out := make([]float64, n)
	for at := secs(0.3); at+secs(0.4) < n; at += secs(1.2) {
		for i := range secs(0.4) {
			env := math.Sin(math.Pi * float64(i) / float64(secs(0.4)))
			out[at+i] = 10000 * env * (math.Sin(2*math.Pi*55*float64(i)/sr) + 0.1*r.NormFloat64())
		}
	}
	return out
}

// hum is mains hum with harmonics, steady.
func hum(n int, hz float64) []float64 {
	out := make([]float64, n)
	for i := range out {
		x := 2 * math.Pi * hz * float64(i) / sr
		out[i] = 1500*math.Sin(x) + 700*math.Sin(2*x) + 400*math.Sin(3*x)
	}
	return out
}

func TestNoVoiceIsSkipped(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	n := secs(4)
	for name, pcm := range map[string][]int16{
		"silence":            make([]int16, n),
		"quiet room":         mix(noise(r, n, 30)),
		"fan noise":          mix(noise(r, n, 2000)),
		"typing":             mix(noise(r, n, 40), typing(r, n)),
		"typing, noisy room": mix(noise(r, n, 400), typing(r, n)),
		"breathing":          mix(noise(r, n, 40), breathing(r, n)),
		"breath on the mic":  mix(noise(r, n, 40), darkBreath(r, n)),
		"wind rumble":        mix(noise(r, n, 40), rumble(r, n)),
		"mic bumps":          mix(noise(r, n, 40), bumps(r, n)),
		"desk knocks":        mix(noise(r, n, 40), thumps(n)),
		"50 Hz hum":          mix(noise(r, n, 40), hum(n, 50)),
		"60 Hz hum":          mix(noise(r, n, 40), hum(n, 60)),
		"typing over hum":    mix(noise(r, n, 40), hum(n, 60), typing(r, n)),
	} {
		v := measureVoice(pcm)
		if v.voiced >= minVoiced {
			t.Errorf("%s: voiced %v (longest %v, floor %.0f), want under %v", name, v.voiced, v.longest, v.floor, minVoiced)
		}
	}
}

func TestSpeechIsKept(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	speech := speechSample(t)
	n := len(speech)
	pad := func(p []int16) []int16 { // the cutter's 300 ms of quiet before
		return mix(noise(r, secs(0.3), 30), append(make([]float64, secs(0.3)), floats(p, 1)...))
	}
	for name, pcm := range map[string][]int16{
		"speech":                speech,
		"first phrase":          speech[:secs(2.2)],      // "And so my fellow Americans"
		"second phrase":         pad(speech[secs(3.2):]), // "ask not ..."
		"one word":              pad(speech[secs(3.2):secs(3.75)]),
		"quiet mic":             mix(floats(speech, 0.05)),
		"noisy room":            mix(floats(speech, 1), noise(r, n, 1200)),
		"quiet mic, noisy room": mix(floats(speech, 0.1), noise(r, n, 150)),
		"typing while talking":  mix(floats(speech, 1), typing(r, n)),
		"breathing, hum":        mix(floats(speech, 0.5), breathing(r, n), hum(n, 50)),
	} {
		v := measureVoice(pcm)
		if v.voiced < minVoiced {
			t.Errorf("%s: voiced %v (longest %v, floor %.0f), want at least %v", name, v.voiced, v.longest, v.floor, minVoiced)
		}
		t.Logf("%s: voiced %v over %v", name, v.voiced, v.span)
	}
}

func TestMadeUpText(t *testing.T) {
	speech := measureVoice(speechSample(t))
	word := voiceInfo{voiced: 200 * time.Millisecond, span: 200 * time.Millisecond}
	blip := voiceInfo{voiced: 100 * time.Millisecond, span: 100 * time.Millisecond}
	for _, c := range []struct {
		text string
		v    voiceInfo
		drop bool
	}{
		{"And so, my fellow Americans, ask not what your country can do for you.", speech, false},
		{"Thank you.", speech, false},
		// Fast speech (a 280 words a minute voice): 86 characters over 2.3 s.
		{"Thank you all for your time tonight, I think we covered everything we needed to cover.", voiceInfo{voiced: 1580 * time.Millisecond, span: 2320 * time.Millisecond}, false},
		{"Okay.", word, false},
		{"Yes.", blip, false},
		{"Thank you.", word, true},
		{"Thanks!", blip, true},
		{"Bye-bye.", word, true},
		{"you", blip, true},
		{"Thank you all for your time tonight.", blip, true}, // seen on a cut segment
		{"Thank you all for your time tonight.", word, true},
		{"I'll see you all next week, take care.", word, true},
	} {
		why := madeUp(c.text, c.v)
		if (why != "") != c.drop {
			t.Errorf("madeUp(%q, voiced %v) = %q, want drop=%v", c.text, c.v.voiced, why, c.drop)
		}
	}
}

func TestCleanTextNeverSaid(t *testing.T) {
	for in, want := range map[string]string{
		"Thanks for watching!":                    "",
		"Thank you for watching.":                 "",
		"Please subscribe to my channel.":         "",
		"Don't forget to like and subscribe!":     "",
		"Subtitles by the Amara.org community":    "",
		"Transcription by CastingWords":           "",
		"Thank you.":                              "Thank you.", // people say it: kept from others
		"Thanks for watching the kids yesterday.": "Thanks for watching the kids yesterday.",
		"The notes were transcribed by Toru.":     "The notes were transcribed by Toru.",
		"It was translated by Maria last week.":   "It was translated by Maria last week.",
		"Go to google.com and search it.":         "Go to google.com and search it.",
	} {
		if got := cleanText(in); got != want {
			t.Errorf("cleanText(%q) = %q, want %q", in, got, want)
		}
	}
}

// A stretch with no voice in it never reaches whisper (or the queue).
func TestNewSegmentSkipsNoVoice(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	enc := func(pcm []int16) string {
		b := make([]byte, 2*len(pcm))
		for i, v := range pcm {
			binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
		}
		return base64.StdEncoding.EncodeToString(b)
	}
	n := secs(3)
	if _, keep, err := newSegment(1, 2, enc(mix(noise(r, n, 40), typing(r, n)))); err != nil || keep {
		t.Fatalf("typing: keep=%v err=%v", keep, err)
	}
	seg, keep, err := newSegment(1, 2, enc(speechSample(t)))
	if err != nil || !keep {
		t.Fatalf("speech: keep=%v err=%v", keep, err)
	}
	if seg.voice.voiced < minVoiced || len(seg.pcm) != len(speechSample(t)) {
		t.Fatalf("speech segment: voiced %v, %d samples", seg.voice.voiced, len(seg.pcm))
	}
}
