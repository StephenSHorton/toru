package toast

import (
	_ "embed"
	"encoding/binary"
	"math"
	"sync"
)

// The s&box editor notification sounds (Facepunch, MIT; sounds/NOTICE.md),
// the same ones Suzuri uses. All three are 16-bit PCM at 44.1 kHz; success is
// stereo, and fail/success carry a LIST chunk before their data.
var (
	//go:embed sounds/success.wav
	successWAV []byte
	//go:embed sounds/fail.wav
	failWAV []byte
	//go:embed sounds/published.wav
	publishedWAV []byte
)

type sound int

const (
	soundSuccess sound = iota
	soundFail
	soundPublished
)

func (s sound) wav() []byte {
	switch s {
	case soundFail:
		return failWAV
	case soundPublished:
		return publishedWAV
	default:
		return successWAV
	}
}

// soundFor maps a card to its sound: an actionable card (it has buttons) or a
// warning gets "published", an error "fail", anything else "success".
func soundFor(k Kind, actionable bool) sound {
	switch {
	case actionable || k == Warn:
		return soundPublished
	case k == Error:
		return soundFail
	default:
		return soundSuccess
	}
}

// PlaySound plays kind's sound on its own, for feedback without a card: replay
// plays its save sound this way so a game in exclusive fullscreen (which no
// window can draw over) still hears it. It honours Volume but not Enabled or
// Sound: the caller has its own setting for it (replay's "Play a sound when
// saved"), and turning off the cards' sounds mustn't silently override that.
func PlaySound(kind Kind) {
	play(soundFor(kind, false), CurrentOptions().Volume)
}

// play plays s at vol (0..1) without waiting.
func play(s sound, vol float64) {
	if b := soundBytes(s, vol); b != nil {
		playWAV(b)
	}
}

var (
	soundMu    sync.Mutex
	soundVol   = -1.0
	soundCache [3][]byte
	// soundHold keeps the last few buffers handed to the OS alive: async
	// playback reads the memory while it plays, after a volume change has
	// already dropped it from soundCache.
	soundHold [4][]byte
	soundSlot int
)

// soundBytes returns s scaled to vol (a cached copy; the embedded bytes at
// full volume), or nil for silence.
func soundBytes(s sound, vol float64) []byte {
	vol = math.Round(vol*100) / 100 // a dragged slider rebuilds per percent, not per pixel
	if vol <= 0 {
		return nil
	}
	soundMu.Lock()
	defer soundMu.Unlock()
	if vol != soundVol {
		soundCache, soundVol = [3][]byte{}, vol
	}
	b := soundCache[s]
	if b == nil {
		b = scaleWAV(s.wav(), vol)
		soundCache[s] = b
	}
	soundHold[soundSlot] = b
	soundSlot = (soundSlot + 1) % len(soundHold)
	return b
}

// pcmData finds the sample bytes (wav[off:off+n]) of a 16-bit PCM WAV by
// walking its RIFF chunks. ok is false for anything else.
func pcmData(wav []byte) (off, n int, ok bool) {
	if len(wav) < 12 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return 0, 0, false
	}
	pcm16 := false
	for p := 12; p+8 <= len(wav); {
		id, size := string(wav[p:p+4]), int(binary.LittleEndian.Uint32(wav[p+4:p+8]))
		body := p + 8
		if size < 0 || body+size > len(wav) {
			return 0, 0, false
		}
		switch id {
		case "fmt ":
			if size >= 16 {
				format := binary.LittleEndian.Uint16(wav[body:])
				bits := binary.LittleEndian.Uint16(wav[body+14:])
				pcm16 = format == 1 && bits == 16
			}
		case "data":
			return body, size, pcm16
		}
		p = body + size + size&1 // chunks are word-aligned
	}
	return 0, 0, false
}

// scaleWAV returns a copy of wav with its samples scaled by vol. At full
// volume, or for a file it can't parse, it returns wav itself.
func scaleWAV(wav []byte, vol float64) []byte {
	off, n, ok := pcmData(wav)
	if !ok || vol >= 1 {
		return wav
	}
	out := append([]byte(nil), wav...)
	for i := off; i+1 < off+n; i += 2 {
		v := float64(int16(binary.LittleEndian.Uint16(out[i:]))) * vol
		binary.LittleEndian.PutUint16(out[i:], uint16(int16(math.Round(v))))
	}
	return out
}
