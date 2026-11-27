//go:build windows

package transcript

import (
	"context"
	"encoding/binary"
	"flag"
	"os"
	"runtime"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestWhisperCPUUse measures how much CPU whisper-server uses on this PC,
// on the CPU build and on the GPU build, through the engine itself: the
// real downloads (pinned, checked), the real GPU check, the real servers.
// Each build transcribes the same spoken clip once every few seconds, like
// someone talking in a call, and the server process's CPU time is read from
// Windows before and after.
//
//	go test ./internal/transcript -run WhisperCPUUse -v -timeout 30m -args -measure <16 kHz mono wav> [-root <folder>]
//
// (-root keeps the downloads between runs; relative paths are from this
// package's folder.)
var (
	measureWav  = flag.String("measure", "", "16 kHz mono 16-bit wav for TestWhisperCPUUse")
	measureRoot = flag.String("root", "", "folder TestWhisperCPUUse downloads whisper into (default: a temp folder)")
)

func TestWhisperCPUUse(t *testing.T) {
	path := *measureWav
	if path == "" {
		t.Skip("pass -args -measure <16 kHz mono 16-bit wav> to run")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pcm := make([]int16, (len(raw)-44)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[44+2*i:]))
	}
	root := *measureRoot
	if root == "" {
		root = t.TempDir()
	}
	changed := make(chan struct{}, 1)
	e := newEngineAt(root, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	}, probeGPU)
	<-e.gpuKnown
	st := e.Status()
	t.Logf("GPU: %q (driver %s), %d whisper threads, %d logical CPUs", st.GPU, e.gpu.Driver, st.Threads, runtime.NumCPU())
	if st.GPU == "" {
		t.Skip("no NVIDIA GPU whisper can use on this PC")
	}
	e.Download()
	for !e.Ready() {
		<-changed
		if st := e.Status(); st.Phase == PhaseError {
			t.Fatal(st.Error)
		}
	}
	e.DownloadGPU()
	for {
		st := e.Status()
		if st.GPUError != "" && !st.GPUReady {
			t.Fatal(st.GPUError)
		}
		if st.GPUReady && !st.GPUFetching {
			break
		}
		select {
		case <-changed:
		case <-time.After(time.Second):
		}
	}
	defer e.Stop()

	const rounds = 10
	every := 6 * time.Second
	clip := time.Duration(len(pcm)) * time.Second / SampleRate
	t.Logf("clip: %.1f s of speech, sent every %s, %d times", clip.Seconds(), every, rounds)
	for _, onGPU := range []bool{false, true} {
		e.Stop()
		e.mu.Lock()
		e.gpuFailed = !onGPU // false: the CPU build, as on a PC without a GPU
		e.mu.Unlock()
		if _, err := e.Transcribe(context.Background(), pcm); err != nil { // start it, load the model
			t.Fatal(err)
		}
		device := e.Status().Device
		if (device == DeviceGPU) != onGPU {
			t.Fatalf("wanted the GPU build: %v, runs on %s", onGPU, device)
		}
		e.srvMu.Lock()
		pid := e.cmd.Process.Pid
		e.srvMu.Unlock()
		before := cpuTime(t, pid)
		start := time.Now()
		var slowest time.Duration
		var text string
		for i := 0; i < rounds; i++ {
			at := time.Now()
			text, err = e.Transcribe(context.Background(), pcm)
			if err != nil {
				t.Fatal(err)
			}
			slowest = max(slowest, time.Since(at))
			if wait := every - time.Since(at); wait > 0 && i < rounds-1 {
				time.Sleep(wait)
			}
		}
		wall := time.Since(start)
		used := cpuTime(t, pid) - before
		t.Logf("%s build: %.2f CPU-seconds over %.0f s (%.1f%% of one core, %.2f%% of all %d), %.2f CPU-seconds per clip, slowest clip %d ms; text %q",
			device, used.Seconds(), wall.Seconds(), 100*used.Seconds()/wall.Seconds(),
			100*used.Seconds()/wall.Seconds()/float64(runtime.NumCPU()), runtime.NumCPU(),
			used.Seconds()/rounds, slowest.Milliseconds(), text)
	}
}

// cpuTime is a process's CPU time so far (user and kernel).
func cpuTime(t *testing.T, pid int) time.Duration {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var c, x, k, u windows.Filetime
	if err := windows.GetProcessTimes(h, &c, &x, &k, &u); err != nil {
		t.Fatal(err)
	}
	ticks := func(f windows.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
	return time.Duration((ticks(k) + ticks(u)) * 100)
}
