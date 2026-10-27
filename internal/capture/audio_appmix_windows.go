//go:build windows

package capture

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// audio_appmix_windows.go builds "desktop audio minus some process trees"
// for more than one tree: an INCLUDE-tree process-loopback client per app
// that has an audio session (any render endpoint), summed into one pipe.
// Live streaming needs this when an audio mixer runs (audio_mixers_windows.go):
// excluding Toru alone keeps the mixer's mic feed, excluding the mixer alone
// keeps Toru's call audio, and the API excludes only one tree.
//
// Trade-offs: a newly started app is picked up on the next scan (up to
// appMixScan late), and the system-sounds session is not captured. When an
// ancestor of a captured app gains a session (a launcher such as Steam after
// its game), the app's client hands over to a fresh one for the ancestor's
// tree, which drops about 100 ms of that app's audio (measured: the only gap
// in a 20 s run). The scan runs on the pipe-writer goroutine and took
// 20-35 ms per pass on a busy desktop without causing an underrun.

const (
	appMixScan = 2 * time.Second
	// appMixLead is how far the mixer runs behind wall clock so each app's
	// buffer holds a few engine packets; appMixMax bounds a buffer (latency)
	// if an app's clock runs fast.
	appMixLead = 60 * time.Millisecond
	appMixMax  = 300 * time.Millisecond
)

// appMixAudio is the mixed capture; it satisfies audioSource.
type appMixAudio struct {
	AudioInput
	ban  []uint32 // tree roots never captured (Toru, mixers)
	pipe *os.File
	stop chan struct{}
	done chan struct{}

	mu   sync.Mutex
	apps map[uint32]*appMixApp
}

// appMixApp is one app's include-tree client and its not-yet-mixed samples.
type appMixApp struct {
	proc    windows.Handle // to notice exit (0 if it couldn't be opened)
	stop    chan struct{}
	done    chan struct{}
	buf     []int16 // interleaved stereo, guarded by appMixAudio.mu
	filling bool    // underran: contribute nothing until appMixLead is buffered
}

// startAppMixAudio captures every app's audio except the trees rooted at ban.
func startAppMixAudio(ban []uint32, pipeName string) (audioSource, error) {
	pipe, err := createAudioPipe(pipeName)
	if err != nil {
		return nil, fmt.Errorf("app audio mix: %w", err)
	}
	m := &appMixAudio{
		AudioInput: AudioInput{PipePath: pipe.Name(), SampleFmt: "s16le", SampleRate: processRate, Channels: processChannels},
		ban:        ban,
		pipe:       pipe,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
		apps:       map[uint32]*appMixApp{},
	}
	m.scan()
	go m.run()
	return m, nil
}

// Input returns the ffmpeg-facing description of the stream.
func (m *appMixAudio) Input() AudioInput { return m.AudioInput }

// Stop ends the mix and every app capture.
func (m *appMixAudio) Stop() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
	// Unblock ConnectNamedPipe if ffmpeg never connected (see audioStream.Stop).
	if h, err := windows.CreateFile(windows.StringToUTF16Ptr(m.PipePath),
		windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0); err == nil {
		_ = windows.CloseHandle(h)
	}
	<-m.done
}

// run serves the pipe: rescans apps periodically and writes the mix on a
// wall clock, padding with silence, exactly like the single-client pumps.
func (m *appMixAudio) run() {
	defer close(m.done)
	defer func() {
		m.mu.Lock()
		apps := m.apps
		m.apps = map[uint32]*appMixApp{}
		m.mu.Unlock()
		for _, a := range apps {
			a.close()
		}
		_ = m.pipe.Close()
	}()

	if err := windows.ConnectNamedPipe(windows.Handle(m.pipe.Fd()), nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return
	}
	select {
	case <-m.stop:
		return
	default:
	}

	// Drop what piled up before ffmpeg connected, or it would be latency.
	m.mu.Lock()
	for _, a := range m.apps {
		a.buf, a.filling = nil, true
	}
	m.mu.Unlock()

	start := time.Now()
	lastScan := start
	var written int64
	for {
		select {
		case <-m.stop:
			return
		case <-time.After(10 * time.Millisecond):
		}
		if time.Since(lastScan) >= appMixScan {
			m.scan()
			lastScan = time.Now()
		}
		expected := int64((time.Since(start) - appMixLead).Seconds() * processRate)
		if n := expected - written; n > 0 {
			if _, err := m.pipe.Write(m.mix(int(n))); err != nil {
				return
			}
			written += n
		}
	}
}

// mix takes n frames from every app (silence where an app has none) and
// returns their clipped sum as s16le.
func (m *appMixAudio) mix(n int) []byte {
	sum := make([]int32, n*processChannels)
	m.mu.Lock()
	for _, a := range m.apps {
		if a.filling {
			if len(a.buf) < int(appMixLead.Seconds()*processRate)*processChannels {
				continue
			}
			a.filling = false
		}
		take := min(len(a.buf), len(sum))
		for i, v := range a.buf[:take] {
			sum[i] += int32(v)
		}
		a.buf = a.buf[take:]
		if take < len(sum) {
			a.filling = true // ran dry: rebuild the lead instead of crackling
		}
	}
	m.mu.Unlock()
	out := make([]byte, len(sum)*2)
	for i, v := range sum {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(int16(max(-32768, min(32767, v)))))
	}
	return out
}

// scan reconciles the clients with the apps that have audio sessions now:
// new apps get a client; apps that exited, or whose tree another picked app
// now covers, lose theirs.
func (m *appMixAudio) scan() {
	parent, exe := processSnapshot()
	sessions := renderSessionPIDs()
	var live []uint32
	for _, pid := range sessions {
		// Sessions can outlive their process. audiodg is the audio engine's
		// own host (effects), not an app.
		if name, ok := exe[pid]; ok && name != "audiodg.exe" {
			live = append(live, pid)
		}
	}
	want := map[uint32]bool{}
	for _, pid := range appMixRoots(live, parent, m.ban) {
		want[pid] = true
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// An empty session list is an enumeration failure (Toru itself has
	// sessions while streaming), not "every app went quiet": keep the clients.
	for pid, a := range m.apps {
		if (len(sessions) > 0 && !want[pid]) || a.exited() {
			delete(m.apps, pid)
			go a.close()
		}
	}
	for pid := range want {
		if _, ok := m.apps[pid]; ok {
			continue
		}
		a := &appMixApp{stop: make(chan struct{}), done: make(chan struct{}), filling: true}
		if h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid); err == nil {
			a.proc = h
		}
		m.apps[pid] = a
		go a.capture(m, pid)
	}
}

// capture runs one include-tree client, appending its frames to a.buf. A
// client that fails to start stays in the map (silent) so it isn't retried
// every scan; it goes when the app exits.
func (a *appMixApp) capture(m *appMixAudio, pid uint32) {
	defer close(a.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(hr) < 0 && uint32(hr) != 0x80010106 {
		return
	}
	c, err := openProcessClient(pid, processLoopbackModeIncludeTree)
	if err != nil {
		return
	}
	defer c.close()
	maxSamples := int(appMixMax.Seconds()*processRate) * processChannels
	sink := func(b []byte) bool {
		m.mu.Lock()
		for i := 0; i+1 < len(b); i += 2 {
			a.buf = append(a.buf, int16(binary.LittleEndian.Uint16(b[i:])))
		}
		if over := len(a.buf) - maxSamples; over > 0 {
			a.buf = a.buf[over:]
		}
		m.mu.Unlock()
		return true
	}
	for {
		select {
		case <-a.stop:
			return
		default:
		}
		c.wait(50)
		c.drain(sink)
	}
}

// exited reports whether the app's root process has ended.
func (a *appMixApp) exited() bool {
	if a.proc == 0 {
		return false
	}
	ev, _ := windows.WaitForSingleObject(a.proc, 0)
	return ev == windows.WAIT_OBJECT_0
}

// close stops the client and waits for it.
func (a *appMixApp) close() {
	close(a.stop)
	<-a.done
	if a.proc != 0 {
		_ = windows.CloseHandle(a.proc)
	}
}
