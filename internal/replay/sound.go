package replay

import (
	"bytes"
	"encoding/binary"
	"math"
)

// tone is one note of a feedback sound.
type tone struct {
	hz, ms float64
}

// Toru's own save sounds, synthesized once so they play even when the Windows
// sound scheme is silent (MessageBeep's "Asterisk" often is). Short and soft:
// they have to be noticeable mid-game without being annoying.
var (
	savedSound  = synthWAV([]tone{{784, 80}, {1175, 160}}, 0.22) // G5 → D6, rising: done
	failedSound = synthWAV([]tone{{392, 120}, {294, 200}}, 0.25) // G4 → D4, falling: failed
)

// synthWAV renders notes as a 44.1 kHz mono 16-bit WAV. Each note is a sine
// with a 5 ms attack and an exponential decay, so there are no clicks.
func synthWAV(notes []tone, volume float64) []byte {
	const rate = 44100
	var pcm []int16
	for _, n := range notes {
		count := int(rate * n.ms / 1000)
		attack := rate * 5 / 1000
		for i := 0; i < count; i++ {
			env := math.Exp(-4 * float64(i) / float64(count))
			if i < attack {
				env *= float64(i) / float64(attack)
			}
			v := volume * env * math.Sin(2*math.Pi*n.hz*float64(i)/rate)
			pcm = append(pcm, int16(v*math.MaxInt16))
		}
	}

	var b bytes.Buffer
	dataLen := uint32(len(pcm) * 2)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, 36+dataLen)
	b.WriteString("WAVEfmt ")
	for _, v := range []any{
		uint32(16), uint16(1), uint16(1), // fmt chunk size, PCM, mono
		uint32(rate), uint32(rate * 2), // sample rate, byte rate
		uint16(2), uint16(16), // block align, bits per sample
	} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, dataLen)
	_ = binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}
