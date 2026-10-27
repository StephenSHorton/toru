//go:build windows

package capture

import (
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// audio_process_windows.go captures the audio of ONE process tree (e.g. just
// Discord) via Windows 10 2004+'s process-loopback activation:
// ActivateAudioInterfaceAsync on the VAD\Process_Loopback virtual device with
// AUDIOCLIENT_PROCESS_LOOPBACK_MODE_INCLUDE_TARGET_PROCESS_TREE. This is the
// mechanism behind per-app clipping tools (Medal, SteelSeries Moments).
//
// Differences from the whole-mix loopback (audio_windows.go) that shape this
// file:
//   - Activation is ASYNC: we implement IActivateAudioInterfaceCompletionHandler
//     (a COM object built in Go via syscall.NewCallback vtables, answering
//     IAgileObject so the callback may arrive on any MTA thread).
//   - GetMixFormat is NOT supported on the process-loopback client: WE choose
//     the format (48kHz s16 stereo) and the engine converts.
//   - The capture client only pumps in EVENTCALLBACK mode; the pump waits on
//     an event instead of polling.

var (
	modMmdevapi                     = windows.NewLazySystemDLL("Mmdevapi.dll")
	procActivateAudioInterfaceAsync = modMmdevapi.NewProc("ActivateAudioInterfaceAsync")
	modKernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procCreateEventW                = modKernel32.NewProc("CreateEventW")
	iidIActivateCompletionHandler   = comGUID{0x41d949ab, 0x9862, 0x444a, [8]byte{0x80, 0xf6, 0xc2, 0x61, 0x33, 0x4d, 0xa5, 0xeb}}
	iidIAgileObject                 = comGUID{0x94ea2b94, 0xe9cc, 0x49e0, [8]byte{0xc0, 0xff, 0xee, 0x64, 0xca, 0x8f, 0x5b, 0x90}}
	iidIUnknown                     = comGUID{0x00000000, 0x0000, 0x0000, [8]byte{0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}}
	processLoopbackVAD              = windows.StringToUTF16Ptr(`VAD\Process_Loopback`)
	audclntStreamFlagsEventCallback = uint32(0x00040000)
	processLoopbackModeIncludeTree  = uint32(0)
	processLoopbackModeExcludeTree  = uint32(1)
	activationTypeProcessLoopback   = uint32(1)
)

// audioclientActivationParams mirrors AUDIOCLIENT_ACTIVATION_PARAMS for the
// process-loopback case (type + {TargetProcessId, ProcessLoopbackMode}).
type audioclientActivationParams struct {
	ActivationType      uint32
	TargetProcessID     uint32
	ProcessLoopbackMode uint32
}

// propVariantBlob mirrors PROPVARIANT carrying VT_BLOB on x64:
// vt at +0, blob.cbSize at +8, blob.pBlobData at +16.
type propVariantBlob struct {
	Vt       uint16
	_        [6]byte
	CbSize   uint32
	_        [4]byte
	BlobData unsafe.Pointer
}

const vtBlob = 65

// activationHandler is our Go-implemented COM object. Layout REQUIREMENT:
// the vtable pointer must be the first word, exactly like a C COM object.
type activationHandler struct {
	vtbl *activationHandlerVtbl
	done chan struct{}
	op   *comObject // the async operation delivered to ActivateCompleted
}

type activationHandlerVtbl struct {
	QueryInterface    uintptr
	AddRef            uintptr
	Release           uintptr
	ActivateCompleted uintptr
}

// handlerVtblSingleton is shared by all handler instances; the callbacks are
// created once (syscall.NewCallback registrations are process-permanent).
var handlerVtblSingleton = &activationHandlerVtbl{
	QueryInterface: windows.NewCallback(func(this *activationHandler, riid *comGUID, out *unsafe.Pointer) uintptr {
		// We are IUnknown, the completion handler, and agile (callable from
		// any MTA thread without marshaling — required or the callback hangs).
		if *riid == iidIUnknown || *riid == iidIActivateCompletionHandler || *riid == iidIAgileObject {
			*out = unsafe.Pointer(this)
			return 0 // S_OK; refcounting is a no-op (Go GC owns the object)
		}
		*out = nil
		return 0x80004002 // E_NOINTERFACE
	}),
	AddRef:  windows.NewCallback(func(_ *activationHandler) uintptr { return 1 }),
	Release: windows.NewCallback(func(_ *activationHandler) uintptr { return 1 }),
	ActivateCompleted: windows.NewCallback(func(this *activationHandler, op *comObject) uintptr {
		this.op = op
		close(this.done)
		return 0
	}),
}

// activateProcessLoopbackClient performs the async activation dance and
// returns an IAudioClient scoped to pid's process tree. mode is
// processLoopbackModeIncludeTree (only that tree) or
// processLoopbackModeExcludeTree (the whole system mix minus that tree).
func activateProcessLoopbackClient(pid, mode uint32) (*comObject, error) {
	params := audioclientActivationParams{
		ActivationType:      activationTypeProcessLoopback,
		TargetProcessID:     pid,
		ProcessLoopbackMode: mode,
	}
	pv := propVariantBlob{
		Vt:       vtBlob,
		CbSize:   uint32(unsafe.Sizeof(params)),
		BlobData: unsafe.Pointer(&params),
	}
	handler := &activationHandler{vtbl: handlerVtblSingleton, done: make(chan struct{})}

	var asyncOp *comObject
	hr, _, _ := procActivateAudioInterfaceAsync.Call(
		uintptr(unsafe.Pointer(processLoopbackVAD)),
		uintptr(unsafe.Pointer(&iidIAudioClient)),
		uintptr(unsafe.Pointer(&pv)),
		uintptr(unsafe.Pointer(handler)),
		uintptr(unsafe.Pointer(&asyncOp)),
	)
	if int32(hr) < 0 {
		return nil, fmt.Errorf("process loopback: ActivateAudioInterfaceAsync: 0x%x", hr)
	}
	defer comRelease(asyncOp)

	select {
	case <-handler.done:
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("process loopback: activation timed out")
	}
	runtime.KeepAlive(handler)
	runtime.KeepAlive(&params)

	// IActivateAudioInterfaceAsyncOperation::GetActivateResult = vtbl 3.
	var actHR int32
	var client *comObject
	if hr := int32(comCall(handler.op, 3, uintptr(unsafe.Pointer(&actHR)), uintptr(unsafe.Pointer(&client)))); hr < 0 {
		return nil, fmt.Errorf("process loopback: GetActivateResult: 0x%x", hr)
	}
	if actHR < 0 || client == nil {
		return nil, fmt.Errorf("process loopback: activation failed: 0x%x", uint32(actHR))
	}
	return client, nil
}

// startProcessLoopbackAudio begins capturing pid's process-tree audio into a
// named pipe, exactly like startLoopbackAudio does for the whole system mix.
// Fixed format: 48kHz s16 stereo (GetMixFormat is unsupported here; the audio
// engine converts to whatever we ask for).
func startProcessLoopbackAudio(pid uint32, pipeName string) (audioSource, error) {
	ready := make(chan audioInitResult, 1)
	s := &audioStream{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go s.processPump(pid, processLoopbackModeIncludeTree, pipeName, ready)
	res := <-ready
	if res.err != nil {
		return nil, res.err
	}
	s.AudioInput = res.in
	return s, nil
}

// startExcludeProcessLoopbackAudio captures the whole system mix EXCEPT pid's
// process tree (the replay buffer's "desktop audio without Discord").
func startExcludeProcessLoopbackAudio(pid uint32, pipeName string) (audioSource, error) {
	ready := make(chan audioInitResult, 1)
	s := &audioStream{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go s.processPump(pid, processLoopbackModeExcludeTree, pipeName, ready)
	res := <-ready
	if res.err != nil {
		return nil, res.err
	}
	s.AudioInput = res.in
	return s, nil
}

// Process-loopback clients always deliver this format (we choose it; the
// engine converts): 48kHz s16 stereo.
const (
	processRate       = 48000
	processChannels   = 2
	processBlockAlign = processChannels * 2
)

// processClient is one started process-loopback capture client. Open, use
// and close it on ONE COM-initialized (MTA), OS-locked goroutine.
type processClient struct {
	client, capt *comObject
	event        windows.Handle
}

// openProcessClient activates, initializes and starts a process-loopback
// client for pid's tree in mode (include or exclude).
func openProcessClient(pid, mode uint32) (*processClient, error) {
	client, err := activateProcessLoopbackClient(pid, mode)
	if err != nil {
		return nil, err
	}
	c := &processClient{client: client}

	// Our chosen format — plain WAVEFORMATEX, 16-bit PCM 48k stereo.
	wfx := waveFormatEx{
		FormatTag:      waveFormatPCM,
		Channels:       processChannels,
		SamplesPerSec:  processRate,
		AvgBytesPerSec: processRate * processBlockAlign,
		BlockAlign:     processBlockAlign,
		BitsPerSample:  16,
	}

	// IAudioClient::Initialize = vtbl 3 — process loopback REQUIRES event-
	// callback mode and a zero device period.
	if hr := int32(comCall(client, 3, audclntShareModeShared,
		uintptr(audclntStreamFlagsLoopback|audclntStreamFlagsEventCallback),
		loopbackBufferDuration100ns, 0, uintptr(unsafe.Pointer(&wfx)), 0)); hr < 0 {
		c.close()
		return nil, fmt.Errorf("process loopback: Initialize: 0x%x", hr)
	}

	hEvent, _, _ := procCreateEventW.Call(0, 0, 0, 0)
	if hEvent == 0 {
		c.close()
		return nil, fmt.Errorf("process loopback: CreateEvent failed")
	}
	c.event = windows.Handle(hEvent)
	if hr := int32(comCall(client, 13, hEvent)); hr < 0 { // SetEventHandle = vtbl 13
		c.close()
		return nil, fmt.Errorf("process loopback: SetEventHandle: 0x%x", hr)
	}

	// GetService = vtbl 14
	if hr := int32(comCall(client, 14, uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(&c.capt)))); hr < 0 || c.capt == nil {
		c.capt = nil
		c.close()
		return nil, fmt.Errorf("process loopback: GetService: 0x%x", hr)
	}

	if hr := int32(comCall(client, 10)); hr < 0 { // Start = vtbl 10
		c.close()
		return nil, fmt.Errorf("process loopback: Start: 0x%x", hr)
	}
	return c, nil
}

// wait blocks until the engine signals new packets or ms elapses.
func (c *processClient) wait(ms uint32) { _, _ = windows.WaitForSingleObject(c.event, ms) }

// drain hands every queued packet to sink (silent packets as zeros) and
// returns the frames delivered, or false once sink fails.
func (c *processClient) drain(sink func([]byte) bool) (int64, bool) {
	var frames int64
	for {
		var packetFrames uint32
		if hr := int32(comCall(c.capt, 5, uintptr(unsafe.Pointer(&packetFrames)))); hr < 0 || packetFrames == 0 {
			return frames, true
		}
		var (
			data  *byte
			n     uint32
			flags uint32
		)
		if hr := int32(comCall(c.capt, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&n)), uintptr(unsafe.Pointer(&flags)), 0, 0)); hr < 0 {
			return frames, true
		}
		buf := make([]byte, int(n)*processBlockAlign)
		if flags&audclntBufferFlagsSilent == 0 && data != nil {
			copy(buf, unsafe.Slice(data, len(buf)))
		}
		comCall(c.capt, 4, uintptr(n))
		if !sink(buf) {
			return frames, false
		}
		frames += int64(n)
	}
}

// close stops the client and releases everything it holds.
func (c *processClient) close() {
	if c.capt != nil {
		comCall(c.client, 11) // Stop
		comRelease(c.capt)
	}
	if c.event != 0 {
		_ = windows.CloseHandle(c.event)
	}
	comRelease(c.client)
}

// processPump mirrors audioStream.pump for the process-loopback client:
// event-driven capture, our own fixed format, same pipe + silence handling.
func (s *audioStream) processPump(pid, mode uint32, pipeName string, ready chan<- audioInitResult) {
	defer close(s.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fail := func(err error) { ready <- audioInitResult{AudioInput{}, err} }

	if hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(hr) < 0 && uint32(hr) != 0x80010106 {
		fail(fmt.Errorf("process loopback: CoInitializeEx: 0x%x", hr))
		return
	}

	c, err := openProcessClient(pid, mode)
	if err != nil {
		fail(err)
		return
	}
	defer c.close()

	pipe, err := createAudioPipe(pipeName)
	if err != nil {
		fail(fmt.Errorf("process loopback: %w", err))
		return
	}
	defer func() { _ = pipe.Close() }()

	ready <- audioInitResult{AudioInput{PipePath: pipe.Name(), SampleFmt: "s16le", SampleRate: processRate, Channels: processChannels}, nil}

	if err := windows.ConnectNamedPipe(windows.Handle(pipe.Fd()), nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return
	}
	select {
	case <-s.stop:
		return
	default:
	}

	start := time.Now()
	var framesWritten int64
	silence := make([]byte, processBlockAlign*processRate/10) // 100ms
	write := func(b []byte) bool { _, err := pipe.Write(b); return err == nil }

	for {
		select {
		case <-s.stop:
			return
		default:
		}
		// Wake on engine event OR every 50ms (so stop/gap-fill stay timely).
		c.wait(50)
		n, ok := c.drain(write)
		if !ok {
			return
		}
		framesWritten += n

		expected := int64(time.Since(start).Seconds() * processRate)
		for expected-framesWritten > processRate/10 {
			if !write(silence) {
				return
			}
			framesWritten += processRate / 10
		}
	}
}

// createAudioPipe makes the outbound named pipe ffmpeg reads PCM from. It
// must exist BEFORE ffmpeg spawns (ffmpeg opens the path like a file).
func createAudioPipe(pipeName string) (*os.File, error) {
	pipePath := `\\.\pipe\` + pipeName
	hPipe, err := windows.CreateNamedPipe(
		windows.StringToUTF16Ptr(pipePath),
		windows.PIPE_ACCESS_OUTBOUND,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		1, 1<<20, 0, 0, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("create pipe: %w", err)
	}
	return os.NewFile(uintptr(hPipe), pipePath), nil
}
