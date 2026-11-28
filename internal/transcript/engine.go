package transcript

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// engine.go is the speech-to-text engine: whisper.cpp's whisper-server,
// run on this PC only. Nothing is shipped in the installer: the first time
// transcription is used the app downloads a pinned whisper.cpp build and a
// pinned model (both checked against their SHA-256 before use) into
// %LOCALAPPDATA%\toru\whisper. The server runs as a child process on a
// loopback port at below-normal priority with a few threads, so a call's
// audio and a game keep the CPU first, and it is stopped when transcription
// ends. One segment is transcribed at a time.
//
// On a PC with an NVIDIA GPU (gpu.go) whisper can run on the GPU instead,
// so a call costs the CPU far less while a game runs. That build is big
// (CUDA's libraries), so it is not part of the first download: the call
// page offers it with its size, and it is fetched only when the person asks.
// Once it is on disk it is used; if it won't start, or starts without the
// GPU, the CPU build runs instead for the rest of the session.

// The whisper.cpp build (CPU, x64, runtime-picked SIMD) and the model:
// whisper base, multilingual, 5-bit quantized (~60 MB), from the
// ggerganov/whisper.cpp model repo at a fixed revision. Bump each URL with
// its SHA-256 and size.
const (
	whisperBuild  = "b5907"
	whisperURL    = "https://github.com/ggml-org/whisper.cpp/releases/download/" + whisperBuild + "/whisper-bin-x64.zip"
	whisperSHA256 = "ecca07b277d943e9b9f4712856a144b857271a6b333104e511e1988e80db582b"
	whisperBytes  = 9168428

	modelName   = "ggml-base-q5_1.bin"
	modelURL    = "https://huggingface.co/ggerganov/whisper.cpp/resolve/5359861c739e955e79d9a303bcbc70fb988958b1/" + modelName
	modelSHA256 = "422f1ae452ade6f30a004d7e5c6a43195e4433bc370bf23fac9cc591f01a8898"
	modelBytes  = 59707625

	// Silero's voice detector (v5.1.2, the version whisper b5907 knows), from
	// the ggml-org/whisper-vad repo at a fixed revision. whisper runs it
	// first and transcribes only the parts with speech, so a hum, a chime or
	// a tune that passes the call's own voice check (internal/stream's
	// voice.go) comes back as no text instead of a made-up word.
	vadName   = "ggml-silero-v5.1.2.bin"
	vadURL    = "https://huggingface.co/ggml-org/whisper-vad/resolve/9ffd54a1e1ee413ddf265af9913beaf518d1639b/" + vadName
	vadSHA256 = "29940d98d42b91fbd05ce489f3ecf7c72f0a42f027e4875919a28fb4c04ea2cf"
	vadBytes  = 885098
	// vadPadMS is how much audio around each part the detector keeps: its
	// default (30 ms) clipped the first or last word of real speech.
	vadPadMS = "250"

	// DownloadBytes is everything the first use downloads.
	DownloadBytes = whisperBytes + modelBytes + vadBytes

	// The CUDA 12.4 build of the same release, for NVIDIA GPUs. It ships
	// CUDA's runtime and cuBLAS next to whisper, so nothing else needs
	// installing beyond the NVIDIA driver.
	gpuBuild  = whisperBuild + "-cuda12"
	gpuURL    = "https://github.com/ggml-org/whisper.cpp/releases/download/" + whisperBuild + "/whisper-bin-win-cuda-12.4.0-x64.zip"
	gpuSHA256 = "34f872ce724ca1ff0a4afca481853e5ee74250f8cb22153e499f953562846039"

	// GPUDownloadBytes is the GPU build's download.
	GPUDownloadBytes = 735571214
)

// Devices whisper runs on (EngineStatus.Device).
const (
	DeviceCPU = "cpu"
	DeviceGPU = "gpu"
)

// SampleRate is the rate Transcribe takes: 16 kHz mono, as whisper wants.
const SampleRate = 16000

// Engine phases.
const (
	PhaseMissing     = "missing"     // not downloaded yet
	PhaseDownloading = "downloading" // Progress counts it
	PhaseReady       = "ready"       // downloaded; the server starts on first use
	PhaseError       = "error"       // the last download failed (Error says why)
	PhaseUnsupported = "unsupported" // no build for this OS
)

// EngineStatus is what the call page shows about the engine.
type EngineStatus struct {
	Phase    string `json:"phase"`
	Done     int64  `json:"done"`  // bytes downloaded so far
	Total    int64  `json:"total"` // bytes to download
	Error    string `json:"error"`
	Model    string `json:"model"`
	Threads  int    `json:"threads"`
	Running  bool   `json:"running"` // the server process is up
	Language string `json:"language"`

	// Device is where whisper runs: DeviceGPU or DeviceCPU (while it runs,
	// what the server said it uses; otherwise what it would start on).
	Device string `json:"device"`
	// GPU is this PC's NVIDIA GPU that whisper can run on ("" for none).
	GPU string `json:"gpu"`
	// GPUReady: the GPU build is downloaded.
	GPUReady bool `json:"gpuReady"`
	// GPUFetching: the GPU build is downloading (GPUDone of GPUTotal bytes),
	// or warming up right after.
	GPUFetching bool  `json:"gpuFetching"`
	GPUDone     int64 `json:"gpuDone"`
	GPUTotal    int64 `json:"gpuTotal"` // the GPU build's download size, shown before it starts
	// GPUError says why the GPU build didn't download, or didn't work this
	// session (whisper then runs on the CPU).
	GPUError string `json:"gpuError"`
	// GPUFailed: the GPU build didn't work this session, so the CPU build runs.
	GPUFailed bool `json:"gpuFailed"`
}

// Engine downloads, runs and talks to whisper-server.
type Engine struct {
	root     string // %LOCALAPPDATA%\toru\whisper; "" = unsupported
	onChange func()

	mu       sync.Mutex
	phase    string
	done     int64
	errText  string
	fetching bool
	running  bool // the server process is up (mirrors cmd, readable without srvMu)

	probe     func() GPU
	gpuKnown  chan struct{} // closed once probe has answered
	gpu       GPU
	gpuFetch  bool   // the GPU build is downloading or warming up
	gpuDone   int64  // its bytes so far
	gpuErr    string // why it didn't download or didn't work
	gpuFailed bool   // it didn't work this session: run the CPU build
	device    string // what the running server uses ("" when none runs)

	// fetchVAD downloads the voice detector for a PC that got the engine
	// before it was added (nil in tests: never downloads). vadTried: it
	// was tried this session.
	fetchVAD func(ctx context.Context) error
	vadTried bool

	srvMu sync.Mutex // one transcription (and one server start) at a time
	cmd   *exec.Cmd
	port  int
	exit  chan struct{}
}

// NewEngine returns the engine rooted at %LOCALAPPDATA%\toru\whisper.
// onChange (may be nil) runs whenever Status changes.
func NewEngine(onChange func()) *Engine {
	root := ""
	if runtime.GOOS == "windows" {
		if cache, err := os.UserCacheDir(); err == nil {
			root = filepath.Join(cache, "toru", "whisper")
		}
	}
	e := newEngineAt(root, onChange, probeGPU)
	e.fetchVAD = func(ctx context.Context) error {
		return fetchFile(ctx, vadURL, vadSHA256, vadBytes, e.vadPath(), nil)
	}
	return e
}

// NewEngineAt is NewEngine rooted at root ("" = unsupported: nothing is
// ever downloaded or run), on a PC with no GPU. For tests.
func NewEngineAt(root string, onChange func()) *Engine {
	return newEngineAt(root, onChange, func() GPU { return GPU{} })
}

// newEngineAt asks probe for this PC's GPU in the background (it runs a
// program), and says so through onChange when it answers.
func newEngineAt(root string, onChange func(), probe func() GPU) *Engine {
	e := &Engine{root: root, onChange: onChange, probe: probe, gpuKnown: make(chan struct{})}
	e.phase = e.diskPhase()
	if root == "" {
		close(e.gpuKnown)
		return e
	}
	go func() {
		g := probe()
		e.mu.Lock()
		e.gpu = g
		e.mu.Unlock()
		close(e.gpuKnown)
		if g.Name != "" {
			logf("whisper can run on %s (driver %s)", g.Name, g.Driver)
			e.changed()
		}
	}()
	return e
}

func (e *Engine) binDir() string    { return filepath.Join(e.root, whisperBuild) }
func (e *Engine) gpuDir() string    { return filepath.Join(e.root, gpuBuild) }
func (e *Engine) modelPath() string { return filepath.Join(e.root, "models", modelName) }
func (e *Engine) vadPath() string   { return filepath.Join(e.root, "models", vadName) }
func (e *Engine) serverPath() string {
	return filepath.Join(e.binDir(), "whisper-server.exe")
}
func (e *Engine) gpuServerPath() string {
	return filepath.Join(e.gpuDir(), "whisper-server.exe")
}

// useGPU reports whether the next server start uses the GPU build: this PC
// has a GPU, the build is downloaded, and it hasn't failed this session.
func (e *Engine) useGPU() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.useGPULocked()
}

func (e *Engine) useGPULocked() bool {
	return e.gpu.Name != "" && !e.gpuFailed && fileExists(e.gpuServerPath())
}

func (e *Engine) diskPhase() string {
	if e.root == "" {
		return PhaseUnsupported
	}
	if fileExists(e.serverPath()) && fileExists(e.modelPath()) {
		return PhaseReady
	}
	return PhaseMissing
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// threads is how many CPU threads whisper may use: a quarter of the
// machine, between 1 and 4.
func threads() int {
	return min(max(runtime.NumCPU()/4, 1), 4)
}

// Status reports the engine's phase and download progress.
func (e *Engine) Status() EngineStatus {
	e.mu.Lock()
	st := EngineStatus{Phase: e.phase, Done: e.done, Total: DownloadBytes, Error: e.errText,
		Model: "whisper base (multilingual)", Threads: threads(), Language: "auto", Running: e.running,
		GPU: e.gpu.Name, GPUFetching: e.gpuFetch, GPUDone: e.gpuDone, GPUTotal: GPUDownloadBytes,
		GPUError: e.gpuErr, GPUFailed: e.gpuFailed, Device: e.device}
	if e.root != "" && e.gpu.Name != "" {
		st.GPUReady = fileExists(e.gpuServerPath())
	}
	if st.Device == "" {
		st.Device = DeviceCPU
		if e.useGPULocked() {
			st.Device = DeviceGPU
		}
	}
	e.mu.Unlock()
	return st
}

func (e *Engine) changed() {
	if e.onChange != nil {
		e.onChange()
	}
}

// Ready reports whether the engine is downloaded.
func (e *Engine) Ready() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.phase == PhaseReady
}

// Download fetches whatever is missing, in the background. It returns at
// once; Status shows progress. Calling it while a download runs, or once
// ready, does nothing.
func (e *Engine) Download() {
	e.mu.Lock()
	if e.fetching || e.phase == PhaseReady || e.phase == PhaseUnsupported {
		e.mu.Unlock()
		return
	}
	e.fetching = true
	e.phase, e.done, e.errText = PhaseDownloading, 0, ""
	e.mu.Unlock()
	e.changed()
	go func() {
		err := e.fetchAll(context.Background())
		e.mu.Lock()
		e.fetching = false
		if err != nil {
			e.phase, e.errText = PhaseError, err.Error()
		} else {
			e.phase = e.diskPhase()
		}
		e.mu.Unlock()
		if err != nil {
			logf("whisper download: %v", err)
		}
		e.changed()
	}()
}

// DownloadGPU fetches the GPU build in the background (the call page shows
// its size first), then warms it up once if the model is here: the first
// run on a GPU newer than the build compiles whisper's GPU code for it
// (about half a minute on an RTX 50), and the driver keeps the result. It
// does nothing on a PC without a GPU, once downloaded, or while it runs.
func (e *Engine) DownloadGPU() {
	e.mu.Lock()
	if e.root == "" || e.gpu.Name == "" || e.gpuFetch || fileExists(e.gpuServerPath()) {
		e.mu.Unlock()
		return
	}
	e.gpuFetch, e.gpuDone, e.gpuErr, e.gpuFailed = true, 0, "", false
	e.mu.Unlock()
	e.changed()
	go func() {
		last := time.Time{}
		err := fetchZip(context.Background(), gpuURL, gpuSHA256, GPUDownloadBytes, e.gpuDir(), extractWhisper, func(done, _ int64) {
			e.mu.Lock()
			e.gpuDone = done
			e.mu.Unlock()
			if time.Since(last) > 200*time.Millisecond {
				last = time.Now()
				e.changed()
			}
		})
		if err != nil {
			logf("whisper GPU download: %v", err)
			e.mu.Lock()
			e.gpuErr = err.Error()
			e.mu.Unlock()
		} else {
			e.changed()
			e.warmGPU()
		}
		e.mu.Lock()
		e.gpuFetch = false
		e.mu.Unlock()
		e.changed()
	}()
}

// warmGPU starts the GPU build once and transcribes a second of silence, so
// its one-time GPU code compile happens now and not on someone's first
// sentence. A CPU server already running (mid-call) is stopped first, so
// the call's next segment runs on the GPU.
func (e *Engine) warmGPU() {
	if !e.Ready() {
		return
	}
	e.srvMu.Lock()
	defer e.srvMu.Unlock()
	was := e.cmd != nil
	e.stopLocked()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := e.startLocked(ctx); err != nil {
		return
	}
	// With the voice detector on, silence would skip the model (and so the
	// compile): this one request runs without it.
	if _, err := e.inferLocked(ctx, make([]int16, SampleRate), false); err != nil {
		logf("whisper GPU warm-up: %v", err)
	}
	if !was {
		e.stopLocked()
	}
}

func (e *Engine) fetchAll(ctx context.Context) error {
	var base int64
	last := time.Time{}
	progress := func(done, _ int64) {
		e.mu.Lock()
		e.done = base + done
		e.mu.Unlock()
		if time.Since(last) > 200*time.Millisecond {
			last = time.Now()
			e.changed()
		}
	}
	if !fileExists(e.serverPath()) {
		if err := fetchZip(ctx, whisperURL, whisperSHA256, whisperBytes, e.binDir(), extractWhisper, progress); err != nil {
			return err
		}
		pruneOthers(e.root, whisperBuild, gpuBuild, "models")
	}
	base = whisperBytes
	if !fileExists(e.modelPath()) {
		if err := fetchFile(ctx, modelURL, modelSHA256, modelBytes, e.modelPath(), progress); err != nil {
			return err
		}
	}
	base += modelBytes
	if !fileExists(e.vadPath()) {
		// Not needed to transcribe: without it whisper runs as before,
		// and the next server start tries again.
		if err := fetchFile(ctx, vadURL, vadSHA256, vadBytes, e.vadPath(), progress); err != nil {
			logf("whisper voice detector download: %v", err)
		}
	}
	pruneOthers(filepath.Dir(e.modelPath()), modelName, vadName)
	return nil
}

// download streams url to a temp file in dir's parent, checking its SHA-256,
// and returns the temp file's path (the caller removes it).
func download(ctx context.Context, url, sum string, size int64, parent string, onProgress func(done, total int64)) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(parent, "download-*")
	if err != nil {
		return "", err
	}
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmp.Name())
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("download %s: %w", path.Base(url), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", path.Base(url), resp.Status)
	}
	h := sha256.New()
	pw := &progressWriter{total: size, onProgress: onProgress}
	if _, err := io.Copy(io.MultiWriter(tmp, h, pw), resp.Body); err != nil {
		return "", fmt.Errorf("download %s: %w", path.Base(url), err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return "", fmt.Errorf("download %s: checksum mismatch (got %s)", path.Base(url), got)
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	ok = true
	return tmp.Name(), nil
}

func fetchFile(ctx context.Context, url, sum string, size int64, dst string, onProgress func(done, total int64)) error {
	tmp, err := download(ctx, url, sum, size, filepath.Dir(dst), onProgress)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// fetchZip downloads and checks an archive, unpacks it with extract into a
// staging folder and swaps that in, so a half-unpacked copy is never used.
func fetchZip(ctx context.Context, url, sum string, size int64, dir string, extract func(archive, dst string) error, onProgress func(done, total int64)) error {
	tmp, err := download(ctx, url, sum, size, filepath.Dir(dir), onProgress)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp) }()
	stage := dir + ".partial"
	_ = os.RemoveAll(stage)
	if err := extract(tmp, stage); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	_ = os.RemoveAll(dir)
	if err := os.Rename(stage, dir); err != nil {
		_ = os.RemoveAll(stage)
		return err
	}
	return nil
}

// extractWhisper copies whisper-server.exe and the DLLs it loads (whisper,
// ggml and ggml's per-CPU backends; in the GPU build also ggml's CUDA
// backend and the CUDA runtime and cuBLAS it links) flat into dst; the
// archive's other tools are skipped.
func extractWhisper(archive, dst string) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return fmt.Errorf("open whisper archive: %w", err)
	}
	defer func() { _ = zr.Close() }()
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	found := false
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Base(f.Name)
		lower := strings.ToLower(name)
		keep := lower == "whisper-server.exe" || lower == "whisper.dll" ||
			(strings.HasPrefix(lower, "ggml") && strings.HasSuffix(lower, ".dll")) ||
			cudaDLLs[lower]
		if !keep {
			continue
		}
		if err := extractZipFile(f, filepath.Join(dst, name)); err != nil {
			return err
		}
		found = found || lower == "whisper-server.exe"
	}
	if !found {
		return errors.New("whisper archive has no whisper-server.exe")
	}
	return nil
}

// cudaDLLs are the CUDA libraries ggml-cuda.dll loads (lower case).
var cudaDLLs = map[string]bool{"cudart64_12.dll": true, "cublas64_12.dll": true, "cublaslt64_12.dll": true}

func extractZipFile(f *zip.File, out string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	w, err := os.Create(out)
	if err != nil {
		return err
	}
	if _, err := io.Copy(w, rc); err != nil {
		_ = w.Close()
		return err
	}
	return w.Close()
}

// pruneOthers removes entries of parent other than keep (earlier builds or
// models), leaving downloads in progress alone.
func pruneOthers(parent string, keep ...string) {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, en := range entries {
		name := en.Name()
		if strings.HasSuffix(name, ".partial") || strings.HasPrefix(name, "download-") {
			continue
		}
		skip := false
		for _, k := range keep {
			skip = skip || strings.EqualFold(name, k)
		}
		if !skip {
			_ = os.RemoveAll(filepath.Join(parent, name))
		}
	}
}

type progressWriter struct {
	done, total int64
	onProgress  func(done, total int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if p.onProgress != nil {
		p.onProgress(p.done, max(p.total, p.done))
	}
	return len(b), nil
}

// --- the server ---------------------------------------------------------------

// Transcribe turns 16 kHz mono samples into text. It starts the server on
// first use. Calls run one at a time.
func (e *Engine) Transcribe(ctx context.Context, pcm []int16) (string, error) {
	if !e.Ready() {
		return "", errors.New("the speech model isn't downloaded")
	}
	e.srvMu.Lock()
	defer e.srvMu.Unlock()
	if err := e.startLocked(ctx); err != nil {
		return "", err
	}
	text, err := e.inferLocked(ctx, pcm, true)
	if err != nil && !e.aliveLocked() {
		// It died mid-request: start it again once (on the CPU if it
		// was the GPU build that died).
		if e.deviceNow() == DeviceGPU {
			e.failGPU(fmt.Errorf("it stopped mid-sentence: %w", err))
		}
		e.stopLocked()
		if err2 := e.startLocked(ctx); err2 != nil {
			return "", err2
		}
		text, err = e.inferLocked(ctx, pcm, true)
	}
	return text, err
}

func (e *Engine) aliveLocked() bool {
	if e.cmd == nil {
		return false
	}
	select {
	case <-e.exit:
		return false
	default:
		return true
	}
}

// startLocked starts the server if it isn't running: the GPU build when it
// should be used, else (or if that fails) the CPU build.
func (e *Engine) startLocked(ctx context.Context) error {
	if e.aliveLocked() {
		return nil
	}
	e.stopLocked()
	e.ensureVADLocked(ctx)
	if e.useGPU() {
		err := e.launchLocked(ctx, true)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err // given up on, not a GPU failure
		}
		e.failGPU(err)
	}
	return e.launchLocked(ctx, false)
}

// ensureVADLocked fetches the voice detector once a session when it is
// missing (an engine downloaded before it was added). If that fails whisper
// runs without it: the call's own voice check still applies.
func (e *Engine) ensureVADLocked(ctx context.Context) {
	if e.vadTried || e.fetchVAD == nil || fileExists(e.vadPath()) {
		return
	}
	e.vadTried = true
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := e.fetchVAD(ctx); err != nil {
		logf("whisper voice detector download: %v", err)
	}
}

// failGPU gives up on the GPU build for this session: whisper runs on the
// CPU build until Toru restarts (or the GPU build is downloaded again).
func (e *Engine) failGPU(err error) {
	logf("whisper GPU build failed, using the CPU: %v", err)
	e.mu.Lock()
	e.gpuFailed = true
	e.gpuErr = err.Error()
	e.mu.Unlock()
	e.changed()
}

func (e *Engine) deviceNow() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.device
}

// launchLocked starts one build's server and waits until it answers. The
// GPU build must also say it put the model on the GPU (it carries ggml's
// CPU backends too, so with an unusable GPU it would quietly run on the
// CPU with more DLLs than the CPU build needs).
func (e *Engine) launchLocked(ctx context.Context, gpu bool) error {
	port, err := freePort()
	if err != nil {
		return err
	}
	dir, exe := e.binDir(), e.serverPath()
	if gpu {
		dir, exe = e.gpuDir(), e.gpuServerPath()
	}
	args := []string{
		"-m", e.modelPath(),
		"--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-t", strconv.Itoa(threads()),
		"-l", "auto",
		"-nt",            // no timestamps in the text
		"--suppress-nst", // no [music] / (coughs) tokens
		"-nth", "0.6",    // skip segments whisper thinks are not speech
		"--public", dir, // no web page to serve
	}
	if fileExists(e.vadPath()) {
		// Transcribe only what Silero's voice detector hears as speech.
		args = append(args, "--vad", "-vm", e.vadPath(), "-vp", vadPadMS)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = dir
	slog := &serverLog{}
	cmd.Stderr = slog
	configureProc(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start whisper: %w", err)
	}
	afterStart(cmd)
	exit := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exit)
	}()
	e.cmd, e.port, e.exit = cmd, port, exit
	// Wait until it answers (the model loads in about a second).
	deadline := time.Now().Add(30 * time.Second)
	for {
		if !e.aliveLocked() {
			e.stopLocked()
			return errors.New("whisper server exited while starting")
		}
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 300*time.Millisecond)
		if err == nil {
			_ = c.Close()
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			e.stopLocked()
			return errors.New("whisper server didn't start")
		}
		time.Sleep(150 * time.Millisecond)
	}
	device := DeviceCPU
	if gpu {
		// The line is written before the server listens; give the copy
		// from its stderr a moment to catch up.
		for i := 0; i < 20 && !slog.onGPU(); i++ {
			time.Sleep(50 * time.Millisecond)
		}
		if !slog.onGPU() {
			e.stopLocked()
			return errors.New("the GPU build started without the GPU")
		}
		device = DeviceGPU
	}
	e.setRunning(true, device)
	e.changed()
	return nil
}

// serverLog reads whisper-server's stderr as it is written, watching for
// the line saying the model went on the GPU ("whisper_backend_init_gpu:
// using CUDA0 backend").
type serverLog struct {
	mu   sync.Mutex
	tail []byte // the unfinished last line
	gpu  bool
}

func (l *serverLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tail = append(l.tail, b...)
	for {
		i := bytes.IndexByte(l.tail, '\n')
		if i < 0 {
			break
		}
		if bytes.Contains(l.tail[:i], []byte("whisper_backend_init_gpu: using CUDA")) {
			l.gpu = true
		}
		l.tail = l.tail[i+1:]
	}
	if len(l.tail) > 4096 {
		l.tail = l.tail[len(l.tail)-4096:]
	}
	return len(b), nil
}

func (l *serverLog) onGPU() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gpu
}

// Stop ends the server process (the download stays). It waits for a
// transcription in progress to finish first.
func (e *Engine) Stop() {
	e.srvMu.Lock()
	was := e.cmd != nil
	e.stopLocked()
	e.srvMu.Unlock()
	if was {
		e.changed()
	}
}

func (e *Engine) stopLocked() {
	if e.cmd == nil {
		return
	}
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	select {
	case <-e.exit:
	case <-time.After(3 * time.Second):
	}
	e.cmd, e.port, e.exit = nil, 0, nil
	e.setRunning(false, "")
}

func (e *Engine) setRunning(on bool, device string) {
	e.mu.Lock()
	e.running, e.device = on, device
	e.mu.Unlock()
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

var inferClient = &http.Client{Timeout: 90 * time.Second}

// inferLocked transcribes pcm; vad false turns the voice detector off for
// this request only (whisper-server copies its settings per request).
func (e *Engine) inferLocked(ctx context.Context, pcm []int16, vad bool) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "speech.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(wav(pcm)); err != nil {
		return "", err
	}
	_ = mw.WriteField("response_format", "json")
	_ = mw.WriteField("temperature", "0")
	if !vad {
		_ = mw.WriteField("vad", "false")
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(e.port)+"/inference", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := inferClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whisper: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf("whisper: %w", err)
	}
	if out.Error != "" {
		return "", errors.New("whisper: " + out.Error)
	}
	return out.Text, nil
}

// wav wraps 16 kHz mono 16-bit samples in a WAV header.
func wav(pcm []int16) []byte {
	n := len(pcm) * 2
	b := make([]byte, 44+n)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1) // PCM
	binary.LittleEndian.PutUint16(b[22:], 1) // mono
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n))
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(s))
	}
	return b
}
