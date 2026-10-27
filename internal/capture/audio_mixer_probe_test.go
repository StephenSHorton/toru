//go:build windows

package capture

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// The stream-audio probes play quiet (about -36 dBFS), short tones from child
// ffplay processes and measure where they show up. A tone stands in for
// Toru's call audio: like the WebView2 that plays call voices, it is a
// process with its own audio stream on the default device. Opt-in: they need
// a desktop session and an output device (TORU_STREAM_LEAK_PROBE=1).

const probeToneDB = -70.0 // a tone below this is "absent"

// TestStreamAudioLeakProbe logs, for every loopback mode stream audio could
// use, the level of a tone and the total level, first with no tone (the
// baseline: whatever else is playing) and then with it. Its findings on a
// SteelSeries Sonar PC are written up in audio_mixers_windows.go.
func TestStreamAudioLeakProbe(t *testing.T) {
	if os.Getenv("TORU_STREAM_LEAK_PROBE") != "1" {
		t.Skip("opt-in")
	}
	const freq = 1234.0
	mixers := runningAudioMixers()
	t.Logf("running mixers: %+v", mixers)
	for _, pid := range renderSessionPIDs() {
		t.Logf("audio session: %s (pid %d)", processName(pid), pid)
	}

	probes := func(tonePID uint32) []probe {
		ps := []probe{{"whole default-endpoint mix", startLoopbackAudio}}
		if tonePID != 0 {
			ps = append(ps,
				probe{"include tone tree", func(p string) (audioSource, error) { return startProcessLoopbackAudio(tonePID, p) }},
				probe{"exclude tone tree", func(p string) (audioSource, error) { return startExcludeProcessLoopbackAudio(tonePID, p) }},
			)
		}
		for _, ep := range listRenderEndpoints() {
			ep := ep
			ps = append(ps, probe{fmt.Sprintf("endpoint %s (default=%v)", ep.name, ep.isDefault),
				func(p string) (audioSource, error) { return startEndpointLoopbackAudio(ep.id, p) }})
		}
		for _, m := range mixers {
			m := m
			ps = append(ps,
				probe{"include " + m.name, func(p string) (audioSource, error) { return startProcessLoopbackAudio(m.pid, p) }},
				probe{"exclude " + m.name, func(p string) (audioSource, error) { return startExcludeProcessLoopbackAudio(m.pid, p) }},
			)
		}
		return ps
	}

	t.Log("--- baseline, no tone")
	runProbes(t, probes(0), freq)

	tone := startProbeTone(t, 1234)
	t.Logf("--- tone pid %d", tone)
	levels := runProbes(t, probes(tone), freq)
	if l, ok := levels["include tone tree"]; !ok || l[0] < probeToneDB {
		t.Fatalf("include-tone capture didn't hear the tone (%v): no output device, or process loopback is broken", l)
	}
}

// TestStreamAudioAppMixProbe proves the per-app mix that stream audio uses
// while a mixer runs: with one tone player banned (standing in for Toru) and
// a second one not, the mix must carry the second tone and not the first.
func TestStreamAudioAppMixProbe(t *testing.T) {
	if os.Getenv("TORU_STREAM_LEAK_PROBE") != "1" {
		t.Skip("opt-in")
	}
	const banned, kept = 1234.0, 2345.0
	fakeToru := startProbeTone(t, banned)
	app := startProbeTone(t, kept)
	ban := []uint32{fakeToru}
	for _, m := range runningAudioMixers() {
		ban = append(ban, m.pid)
	}
	t.Logf("banned (stand-in Toru + mixers): %v; app tone pid %d", ban, app)

	levels := runProbes(t, []probe{
		{"app mix", func(p string) (audioSource, error) { return startAppMixAudio(ban, p) }},
		{"include app tone", func(p string) (audioSource, error) { return startProcessLoopbackAudio(app, p) }},
	}, banned, kept)
	mix := levels["app mix"]
	if mix == nil {
		t.Fatal("app mix didn't run")
	}
	if mix[1] < probeToneDB {
		t.Errorf("app mix lost the app's tone (%.1f dBFS)", mix[1])
	}
	if mix[0] > probeToneDB {
		t.Errorf("app mix carries the banned tree's tone (%.1f dBFS)", mix[0])
	}
}

type probe struct {
	name  string
	start func(pipe string) (audioSource, error)
}

// startProbeTone plays a quiet sine at freq for a few seconds from a child
// ffplay and returns its pid once it is playing.
func startProbeTone(t *testing.T, freq float64) uint32 {
	t.Helper()
	tone := exec.Command("ffplay", "-hide_banner", "-loglevel", "error", "-nodisp", "-autoexit",
		"-f", "lavfi", "-i", fmt.Sprintf("sine=frequency=%g:duration=5,volume=0.12", freq))
	if err := tone.Start(); err != nil {
		t.Skipf("ffplay unavailable: %v", err)
	}
	t.Cleanup(func() { _ = tone.Process.Kill(); _ = tone.Wait() })
	time.Sleep(800 * time.Millisecond)
	return uint32(tone.Process.Pid)
}

// runProbes runs every probe at once over the same 1.5s, logs each one's
// level at every freq and its total level, and returns the freq levels by
// probe name (probes that failed to start are left out).
func runProbes(t *testing.T, probes []probe, freqs ...float64) map[string][]float64 {
	t.Helper()
	type result struct {
		tones []float64
		rms   float64
		err   error
	}
	results := make([]result, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		src, err := p.start(fmt.Sprintf("toru-leak-probe-%d-%d", time.Now().UnixNano(), i))
		if err != nil {
			results[i].err = err
			continue
		}
		wg.Add(1)
		go func(i int, src audioSource) {
			defer wg.Done()
			defer src.Stop()
			results[i].tones, results[i].rms, results[i].err = readProbeLevels(src.Input(), freqs, 1500*time.Millisecond)
		}(i, src)
	}
	wg.Wait()
	out := map[string][]float64{}
	for i, p := range probes {
		r := results[i]
		if r.err != nil {
			t.Logf("%-70s ERROR %v", p.name, r.err)
			continue
		}
		line := fmt.Sprintf("%-70s", p.name)
		for j, f := range freqs {
			line += fmt.Sprintf("  %gHz %6.1f", f, r.tones[j])
		}
		t.Logf("%s  total %6.1f dBFS", line, r.rms)
		out[p.name] = r.tones
	}
	return out
}

// readProbeLevels reads d of in's pipe (as ffmpeg would) and returns the
// level of each freq component (Goertzel, as a sine amplitude in dBFS) and
// the total RMS level, on channel 0.
func readProbeLevels(in AudioInput, freqs []float64, d time.Duration) ([]float64, float64, error) {
	h, err := windows.CreateFile(windows.StringToUTF16Ptr(in.PipePath),
		windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return nil, 0, err
	}
	pipe := os.NewFile(uintptr(h), "pipe")
	defer func() { _ = pipe.Close() }()

	width := 2
	if in.SampleFmt == "f32le" || in.SampleFmt == "s32le" {
		width = 4
	}
	frames := int(float64(in.SampleRate) * d.Seconds())
	buf := make([]byte, frames*in.Channels*width)
	if _, err := io.ReadFull(pipe, buf); err != nil {
		return nil, 0, err
	}
	samples := make([]float64, frames)
	var sum float64
	for f := 0; f < frames; f++ {
		b := buf[f*in.Channels*width:]
		switch in.SampleFmt {
		case "f32le":
			samples[f] = float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
		case "s32le":
			samples[f] = float64(int32(binary.LittleEndian.Uint32(b))) / 2147483648
		default:
			samples[f] = float64(int16(binary.LittleEndian.Uint16(b))) / 32768
		}
		sum += samples[f] * samples[f]
	}
	db := func(v float64) float64 { return 20 * math.Log10(math.Max(v, 1e-9)) }
	tones := make([]float64, len(freqs))
	for i, freq := range freqs {
		// Goertzel: |X(k)| * 2/N is the amplitude of a sine at freq.
		w := 2 * math.Pi * freq / float64(in.SampleRate)
		c := 2 * math.Cos(w)
		var s1, s2 float64
		for _, x := range samples {
			s0 := x + c*s1 - s2
			s2, s1 = s1, s0
		}
		tones[i] = db(math.Sqrt(s1*s1+s2*s2-c*s1*s2) * 2 / float64(len(samples)))
	}
	return tones, db(math.Sqrt(sum / float64(len(samples)))), nil
}
