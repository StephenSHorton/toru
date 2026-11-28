package transcript

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestParseNvidiaSMI(t *testing.T) {
	for _, c := range []struct{ out, want string }{
		{"NVIDIA GeForce RTX 5080, 621.40, 12.0\r\n", "NVIDIA GeForce RTX 5080"},
		{"NVIDIA GeForce GTX 1060 6GB, 560.94, 6.1\nNVIDIA GeForce RTX 3090, 560.94, 8.6\n", "NVIDIA GeForce GTX 1060 6GB"},
		{"NVIDIA GeForce RTX 3060, 551.61, 8.6", "NVIDIA GeForce RTX 3060"}, // the oldest driver for CUDA 12.4
		{"NVIDIA GeForce RTX 3060, 551.52, 8.6", ""},                        // too old for CUDA 12.4
		{"NVIDIA GeForce RTX 2070, 472.12, 7.5", ""},
		{"NVIDIA GeForce GTX 780, 560.94, 3.5", ""}, // older than the build's kernels
		{"NVIDIA GeForce RTX 4090, 566.03, [N/A]", ""},
		{"", ""},
		{"No devices were found", ""},
	} {
		if got := parseNvidiaSMI(c.out).Name; got != c.want {
			t.Errorf("parseNvidiaSMI(%q) = %q, want %q", c.out, got, c.want)
		}
	}
}

// fakeServer builds testdata/fakewhisper once, a stand-in whisper-server.
var fakeServer = sync.OnceValues(func() (string, error) {
	out := filepath.Join(os.TempDir(), "toru-fakewhisper-"+strings.ReplaceAll(runtime.Version(), " ", "")+".exe")
	cmd := exec.Command("go", "build", "-o", out, "./testdata/fakewhisper")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", &exec.Error{Name: string(b), Err: err}
	}
	return out, nil
})

// installFake puts the stand-in server in dir, behaving as mode.
func installFake(t *testing.T, dir, mode string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("the engine runs whisper-server.exe")
	}
	exe, err := fakeServer()
	if err != nil {
		t.Skipf("can't build the fake whisper-server: %v", err)
	}
	raw, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whisper-server.exe"), raw, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(mode), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeEngine is an engine whose CPU build answers "cpu" and whose GPU build
// (when gpuMode isn't "") behaves as gpuMode, on a PC with gpu.
func fakeEngine(t *testing.T, gpu GPU, gpuMode string) *Engine {
	t.Helper()
	root := t.TempDir()
	e := newEngineAt(root, nil, func() GPU { return gpu })
	<-e.gpuKnown
	installFake(t, e.binDir(), "cpu")
	if gpuMode != "" {
		installFake(t, e.gpuDir(), gpuMode)
	}
	_ = os.MkdirAll(filepath.Dir(e.modelPath()), 0o755)
	_ = os.WriteFile(e.modelPath(), []byte("x"), 0o644)
	e.phase = e.diskPhase()
	t.Cleanup(e.Stop)
	return e
}

var rtx = GPU{Name: "NVIDIA GeForce RTX 5080", Driver: "621.40"}

func transcribeWith(t *testing.T, e *Engine) string {
	t.Helper()
	text, err := e.Transcribe(context.Background(), make([]int16, SampleRate))
	if err != nil {
		t.Fatal(err)
	}
	return text
}

func TestNoGPUUsesTheCPUBuild(t *testing.T) {
	// Even with a GPU build on disk, a PC without a usable GPU runs the CPU build.
	e := fakeEngine(t, GPU{}, "gpu")
	if st := e.Status(); st.GPU != "" || st.GPUReady || st.Device != DeviceCPU {
		t.Fatalf("before: %+v", st)
	}
	if got := transcribeWith(t, e); got != "cpu" {
		t.Errorf("transcribed by %q, want the CPU build", got)
	}
	if st := e.Status(); st.Device != DeviceCPU || !st.Running {
		t.Errorf("after: %+v", st)
	}
}

func TestGPUBuildOffered(t *testing.T) {
	// A GPU and no GPU build yet: the CPU build runs, and the GPU build's
	// size is there to show before it downloads.
	e := fakeEngine(t, rtx, "")
	st := e.Status()
	if st.GPU != rtx.Name || st.GPUReady || st.GPUTotal != GPUDownloadBytes || st.Device != DeviceCPU {
		t.Fatalf("status %+v", st)
	}
	if got := transcribeWith(t, e); got != "cpu" {
		t.Errorf("transcribed by %q, want the CPU build", got)
	}
}

func TestGPUBuildUsed(t *testing.T) {
	e := fakeEngine(t, rtx, "gpu")
	if st := e.Status(); !st.GPUReady || st.Device != DeviceGPU {
		t.Fatalf("before: %+v", st)
	}
	if got := transcribeWith(t, e); got != "gpu" {
		t.Errorf("transcribed by %q, want the GPU build", got)
	}
	if st := e.Status(); st.Device != DeviceGPU || st.GPUFailed {
		t.Errorf("after: %+v", st)
	}
}

func TestGPUBuildFallsBackToCPU(t *testing.T) {
	for _, mode := range []string{"exit", "nogpu"} {
		t.Run(mode, func(t *testing.T) {
			e := fakeEngine(t, rtx, mode)
			if got := transcribeWith(t, e); got != "cpu" {
				t.Errorf("transcribed by %q, want the CPU build", got)
			}
			st := e.Status()
			if !st.GPUFailed || st.GPUError == "" || st.Device != DeviceCPU {
				t.Errorf("status %+v", st)
			}
			// It stays on the CPU for the session, even after a restart.
			e.Stop()
			if got := transcribeWith(t, e); got != "cpu" {
				t.Errorf("after a restart transcribed by %q", got)
			}
		})
	}
}

func TestVoiceDetectorWhenDownloaded(t *testing.T) {
	// Without the voice detector whisper starts as before; with it on disk
	// it starts with --vad.
	e := fakeEngine(t, GPU{}, "")
	if got := transcribeWith(t, e); got != "cpu" {
		t.Fatalf("without the detector: %q", got)
	}
	e.Stop()
	_ = os.WriteFile(e.vadPath(), []byte("x"), 0o644)
	if got := transcribeWith(t, e); got != "cpu+vad" {
		t.Fatalf("with the detector: %q", got)
	}
}

func TestVoiceDetectorFetchedOnceOnStart(t *testing.T) {
	// An engine downloaded before the detector was added fetches it on the
	// next start, once a session; a failed fetch still transcribes.
	e := fakeEngine(t, GPU{}, "")
	calls := 0
	e.fetchVAD = func(context.Context) error {
		calls++
		return os.ErrNotExist
	}
	if got := transcribeWith(t, e); got != "cpu" {
		t.Fatalf("after a failed fetch: %q", got)
	}
	e.Stop()
	_ = transcribeWith(t, e)
	if calls != 1 {
		t.Fatalf("fetched %d times, want 1", calls)
	}
	e.Stop()
	e.vadTried = false
	e.fetchVAD = func(context.Context) error { return os.WriteFile(e.vadPath(), []byte("x"), 0o644) }
	if got := transcribeWith(t, e); got != "cpu+vad" {
		t.Fatalf("after fetching: %q", got)
	}
}
