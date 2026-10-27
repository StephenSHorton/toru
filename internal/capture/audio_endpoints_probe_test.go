//go:build windows

package capture

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Probe-only helpers: per-endpoint loopback for TestStreamAudioLeakProbe.

// renderEndpoint is one active output device.
type renderEndpoint struct {
	id, name  string
	isDefault bool
}

var pkeyDeviceFriendlyName = struct {
	fmtid comGUID
	pid   uint32
}{comGUID{0xa45c254e, 0xdf1c, 0x4efd, [8]byte{0x80, 0x20, 0x67, 0xd1, 0x46, 0xa8, 0x50, 0xe0}}, 14}

var procPropVariantClear = modOle32.NewProc("PropVariantClear")

// listRenderEndpoints returns the active render endpoints (id + friendly
// name), marking the default one. Best-effort: COM failures return nil.
func listRenderEndpoints() []renderEndpoint {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded); int32(hr) < 0 && uint32(hr) != 0x80010106 {
		return nil
	}
	var enumerator *comObject
	if hr, _, _ := procCoCreateInst.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidIMMDeviceEnumerator)),
		uintptr(unsafe.Pointer(&enumerator)),
	); int32(hr) < 0 || enumerator == nil {
		return nil
	}
	defer comRelease(enumerator)

	deviceID := func(d *comObject) string { // IMMDevice::GetId = vtbl 5
		var p *uint16
		if hr := int32(comCall(d, 5, uintptr(unsafe.Pointer(&p)))); hr < 0 || p == nil {
			return ""
		}
		defer func() { _, _, _ = procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p))) }()
		return windows.UTF16PtrToString(p)
	}
	var def string
	var d *comObject // GetDefaultAudioEndpoint(eRender, eConsole) = vtbl 4
	if hr := int32(comCall(enumerator, 4, 0, 0, uintptr(unsafe.Pointer(&d)))); hr >= 0 && d != nil {
		def = deviceID(d)
		comRelease(d)
	}

	var coll *comObject // EnumAudioEndpoints(eRender, DEVICE_STATE_ACTIVE) = vtbl 3
	if hr := int32(comCall(enumerator, 3, 0, 1, uintptr(unsafe.Pointer(&coll)))); hr < 0 || coll == nil {
		return nil
	}
	defer comRelease(coll)
	var count uint32 // IMMDeviceCollection::GetCount = vtbl 3
	if hr := int32(comCall(coll, 3, uintptr(unsafe.Pointer(&count)))); hr < 0 {
		return nil
	}
	var out []renderEndpoint
	for i := uint32(0); i < count; i++ {
		var dev *comObject // Item = vtbl 4
		if hr := int32(comCall(coll, 4, uintptr(i), uintptr(unsafe.Pointer(&dev)))); hr < 0 || dev == nil {
			continue
		}
		ep := renderEndpoint{id: deviceID(dev)}
		var store *comObject // IMMDevice::OpenPropertyStore(STGM_READ) = vtbl 4
		if hr := int32(comCall(dev, 4, 0, uintptr(unsafe.Pointer(&store)))); hr >= 0 && store != nil {
			var pv [24]byte // PROPVARIANT: vt at +0, pwszVal at +8
			// IPropertyStore::GetValue = vtbl 5
			if hr := int32(comCall(store, 5, uintptr(unsafe.Pointer(&pkeyDeviceFriendlyName)), uintptr(unsafe.Pointer(&pv[0])))); hr >= 0 {
				if *(*uint16)(unsafe.Pointer(&pv[0])) == 31 { // VT_LPWSTR
					ep.name = windows.UTF16PtrToString(*(**uint16)(unsafe.Pointer(&pv[8])))
				}
				_, _, _ = procPropVariantClear.Call(uintptr(unsafe.Pointer(&pv[0])))
			}
			comRelease(store)
		}
		comRelease(dev)
		ep.isDefault = ep.id != "" && ep.id == def
		out = append(out, ep)
	}
	return out
}

// startEndpointLoopbackAudio is startLoopbackAudio on a specific render
// endpoint (an IMMDevice id from listRenderEndpoints).
func startEndpointLoopbackAudio(deviceID, pipeName string) (audioSource, error) {
	ready := make(chan audioInitResult, 1)
	s := &audioStream{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go s.pump(deviceID, pipeName, ready)
	res := <-ready
	if res.err != nil {
		return nil, res.err
	}
	s.AudioInput = res.in
	return s, nil
}
