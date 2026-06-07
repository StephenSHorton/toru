//go:build windows

package capture

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// audio_windows.go captures SYSTEM audio ("what you hear") via WASAPI
// loopback and streams raw PCM into the recording ffmpeg through a named
// pipe. FFmpeg's Windows builds have no native loopback input device (dshow
// needs driver-dependent "Stereo Mix" or third-party virtual devices), so we
// own the capture: IAudioClient on the default render endpoint with
// AUDCLNT_STREAMFLAGS_LOOPBACK, the same approach OBS uses.
//
// Loopback quirk that shapes this file: WASAPI delivers NO packets while
// nothing is playing. A naive pump would produce an audio track shorter than
// the video and drift out of sync — so the pump tracks wall-clock elapsed
// frames and synthesizes silence to keep the stream continuous.

// COM identifiers (audio core APIs).
var (
	clsidMMDeviceEnumerator = comGUID{0xbcde0395, 0xe52f, 0x467c, [8]byte{0x8e, 0x3d, 0xc4, 0x57, 0x92, 0x91, 0x69, 0x2e}}
	iidIMMDeviceEnumerator  = comGUID{0xa95664d2, 0x9614, 0x4f35, [8]byte{0xa7, 0x46, 0xde, 0x8d, 0xb6, 0x36, 0x17, 0xe6}}
	iidIAudioClient         = comGUID{0x1cb9ad4c, 0xdbfa, 0x4c32, [8]byte{0xb1, 0x78, 0xc2, 0xf5, 0x68, 0xa7, 0x03, 0xb2}}
	iidIAudioCaptureClient  = comGUID{0xc8adbd64, 0xe71e, 0x48a0, [8]byte{0xa4, 0xde, 0x18, 0x5c, 0x39, 0x5c, 0xd3, 0x17}}
)

type comGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

var (
	modOle32           = syscall_NewLazyDLL("ole32.dll")
	procCoInitializeEx = modOle32.NewProc("CoInitializeEx")
	procCoCreateInst   = modOle32.NewProc("CoCreateInstance")
	procCoTaskMemFree  = modOle32.NewProc("CoTaskMemFree")
)

// waveFormatEx mirrors WAVEFORMATEX; the EXTENSIBLE form appends a SubFormat
// GUID whose first uint32 distinguishes PCM (1) from IEEE float (3).
type waveFormatEx struct {
	FormatTag      uint16
	Channels       uint16
	SamplesPerSec  uint32
	AvgBytesPerSec uint32
	BlockAlign     uint16
	BitsPerSample  uint16
	CbSize         uint16
}

const (
	waveFormatPCM        = 1
	waveFormatIEEEFloat  = 3
	waveFormatExtensible = 0xFFFE

	audclntShareModeShared      = 0
	audclntStreamFlagsLoopback  = 0x00020000
	audclntBufferFlagsSilent    = 0x2
	clsctxAll                   = 0x17
	coinitMultithreaded         = 0x0
	loopbackBufferDuration100ns = 10_000_000 // 1s
)

// audioStream is one live loopback capture session. It satisfies audioSource
// (recording.go), which is what the cross-platform recorder holds.
type audioStream struct {
	AudioInput
	stop chan struct{}
	done chan struct{}
}

// Input returns the ffmpeg-facing description of the stream.
func (s *audioStream) Input() AudioInput { return s.AudioInput }

// audioInitResult carries the pump's setup outcome back to the caller.
type audioInitResult struct {
	in  AudioInput
	err error
}

// startLoopbackAudio begins capturing system audio into the named pipe.
// The pipe exists when this returns; the pump waits for ffmpeg to connect.
// Errors mean "no audio available" — callers fall back to video-only.
func startLoopbackAudio(pipeName string) (audioSource, error) {
	ready := make(chan audioInitResult, 1)
	s := &audioStream{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go s.pump(pipeName, ready)
	res := <-ready
	if res.err != nil {
		return nil, res.err
	}
	s.AudioInput = res.in
	return s, nil
}

// Stop ends the capture and waits for the pump to exit. Safe to call after
// ffmpeg has already gone away (write errors just end the pump sooner).
func (s *audioStream) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	// If ffmpeg never connected, ConnectNamedPipe is still blocking — connect
	// to our own pipe to unblock it so the pump can observe stop and exit.
	if h, err := windows.CreateFile(windows.StringToUTF16Ptr(s.PipePath),
		windows.GENERIC_READ, 0, nil, windows.OPEN_EXISTING, 0, 0); err == nil {
		_ = windows.CloseHandle(h)
	}
	<-s.done
}

// pump owns the whole capture lifecycle on one locked OS thread: COM init,
// WASAPI setup (reported via ready), pipe serve, capture loop, teardown.
func (s *audioStream) pump(pipeName string, ready chan<- audioInitResult) {
	defer close(s.done)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	fail := func(err error) {
		ready <- audioInitResult{AudioInput{}, err}
	}

	if hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(hr) < 0 && uint32(hr) != 0x80010106 /*RPC_E_CHANGED_MODE*/ {
		fail(fmt.Errorf("loopback: CoInitializeEx: 0x%x", hr))
		return
	}

	// Default render endpoint -> IAudioClient in shared loopback mode.
	var enumerator *comObject
	if hr, _, _ := procCoCreateInst.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	); int32(hr) < 0 || enumerator == nil {
		fail(fmt.Errorf("loopback: create device enumerator: 0x%x", hr))
		return
	}
	defer comRelease(enumerator)

	var device *comObject // IMMDeviceEnumerator::GetDefaultAudioEndpoint(eRender=0, eConsole=0) = vtbl 4
	if hr := int32(comCall(enumerator, 4, 0, 0, uintptr(unsafe.Pointer(&device)))); hr < 0 || device == nil {
		fail(fmt.Errorf("loopback: no default render endpoint: 0x%x", hr))
		return
	}
	defer comRelease(device)

	var client *comObject // IMMDevice::Activate = vtbl 3
	if hr := int32(comCall(device, 3, uintptr(unsafe.Pointer(&iidIAudioClient)), clsctxAll, 0, uintptr(unsafe.Pointer(&client)))); hr < 0 || client == nil {
		fail(fmt.Errorf("loopback: activate IAudioClient: 0x%x", hr))
		return
	}
	defer comRelease(client)

	var pwfx *waveFormatEx // IAudioClient::GetMixFormat = vtbl 8
	if hr := int32(comCall(client, 8, uintptr(unsafe.Pointer(&pwfx)))); hr < 0 || pwfx == nil {
		fail(fmt.Errorf("loopback: GetMixFormat: 0x%x", hr))
		return
	}
	defer func() { _, _, _ = procCoTaskMemFree.Call(uintptr(unsafe.Pointer(pwfx))) }()

	sampleFmt, err := ffmpegSampleFmt(pwfx)
	if err != nil {
		fail(err)
		return
	}
	blockAlign := int(pwfx.BlockAlign)
	rate := int(pwfx.SamplesPerSec)
	channels := int(pwfx.Channels)

	// IAudioClient::Initialize = vtbl 3 (shared, LOOPBACK, 1s buffer).
	if hr := int32(comCall(client, 3, audclntShareModeShared, audclntStreamFlagsLoopback,
		loopbackBufferDuration100ns, 0, uintptr(unsafe.Pointer(pwfx)), 0)); hr < 0 {
		fail(fmt.Errorf("loopback: Initialize: 0x%x", hr))
		return
	}

	var capt *comObject // IAudioClient::GetService = vtbl 14
	if hr := int32(comCall(client, 14, uintptr(unsafe.Pointer(&iidIAudioCaptureClient)), uintptr(unsafe.Pointer(&capt)))); hr < 0 || capt == nil {
		fail(fmt.Errorf("loopback: GetService(IAudioCaptureClient): 0x%x", hr))
		return
	}
	defer comRelease(capt)

	// The pipe must exist BEFORE ffmpeg spawns (it opens the path like a file).
	pipePath := `\\.\pipe\` + pipeName
	hPipe, err := windows.CreateNamedPipe(
		windows.StringToUTF16Ptr(pipePath),
		windows.PIPE_ACCESS_OUTBOUND,
		windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
		1, 1<<20, 0, 0, nil,
	)
	if err != nil {
		fail(fmt.Errorf("loopback: create pipe: %w", err))
		return
	}
	pipe := os.NewFile(uintptr(hPipe), pipePath)
	defer func() { _ = pipe.Close() }()

	if hr := int32(comCall(client, 10)); hr < 0 { // IAudioClient::Start = vtbl 10
		fail(fmt.Errorf("loopback: Start: 0x%x", hr))
		return
	}
	defer comCall(client, 11) // IAudioClient::Stop

	ready <- audioInitResult{AudioInput{PipePath: pipePath, SampleFmt: sampleFmt, SampleRate: rate, Channels: channels}, nil}

	// Block until ffmpeg opens the pipe (Stop() self-connects to unblock).
	if err := windows.ConnectNamedPipe(hPipe, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
		return
	}
	select {
	case <-s.stop:
		return
	default:
	}

	// Capture loop: drain real packets; synthesize silence across gaps so the
	// track stays wall-clock continuous (see file comment).
	start := time.Now()
	var framesWritten int64
	silence := make([]byte, blockAlign*rate/10) // 100ms of zeros
	var mu sync.Mutex                           // belt-and-braces around pipe writes
	writeFrames := func(b []byte) bool {
		mu.Lock()
		defer mu.Unlock()
		_, err := pipe.Write(b)
		return err == nil
	}

	for {
		select {
		case <-s.stop:
			return
		case <-time.After(10 * time.Millisecond):
		}

		for {
			var packetFrames uint32 // IAudioCaptureClient::GetNextPacketSize = vtbl 5
			if hr := int32(comCall(capt, 5, uintptr(unsafe.Pointer(&packetFrames)))); hr < 0 || packetFrames == 0 {
				break
			}
			var (
				data   *byte
				frames uint32
				flags  uint32
			)
			// IAudioCaptureClient::GetBuffer = vtbl 3
			if hr := int32(comCall(capt, 3, uintptr(unsafe.Pointer(&data)), uintptr(unsafe.Pointer(&frames)), uintptr(unsafe.Pointer(&flags)), 0, 0)); hr < 0 {
				break
			}
			n := int(frames) * blockAlign
			buf := make([]byte, n)
			if flags&audclntBufferFlagsSilent == 0 && data != nil {
				copy(buf, unsafe.Slice(data, n))
			}
			comCall(capt, 4, uintptr(frames)) // ReleaseBuffer
			if !writeFrames(buf) {
				return // ffmpeg closed its end — recording stopped
			}
			framesWritten += int64(frames)
		}

		// Gap fill: if delivered frames lag wall-clock by >100ms (nothing is
		// playing), pad with silence in 100ms chunks to hold A/V sync.
		expected := int64(time.Since(start).Seconds() * float64(rate))
		for expected-framesWritten > int64(rate/10) {
			if !writeFrames(silence) {
				return
			}
			framesWritten += int64(rate / 10)
		}
	}
}

// ffmpegSampleFmt maps the endpoint mix format to ffmpeg's raw-format name.
func ffmpegSampleFmt(w *waveFormatEx) (string, error) {
	tag := int(w.FormatTag)
	if tag == waveFormatExtensible {
		// WAVEFORMATEXTENSIBLE: SubFormat GUID sits at byte offset 24 from the
		// struct start (PACKED WAVEFORMATEX is 18 bytes + 2 Samples + 4
		// dwChannelMask). unsafe.Sizeof(waveFormatEx) is 20 (Go pads to 4),
		// so the offset MUST be the literal C layout, not Sizeof. The GUID's
		// Data1 is the classic wave tag.
		sub := (*comGUID)(unsafe.Pointer(uintptr(unsafe.Pointer(w)) + 24))
		tag = int(sub.Data1)
	}
	switch {
	case tag == waveFormatIEEEFloat && w.BitsPerSample == 32:
		return "f32le", nil
	case tag == waveFormatPCM && w.BitsPerSample == 16:
		return "s16le", nil
	default:
		return "", fmt.Errorf("loopback: unsupported mix format (tag=%d bits=%d)", tag, w.BitsPerSample)
	}
}

// syscall_NewLazyDLL exists so this file's deps stay greppable in one place.
var syscall_NewLazyDLL = windows.NewLazySystemDLL
