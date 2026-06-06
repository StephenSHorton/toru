//go:build windows

package capture

import (
	"syscall"
	"unsafe"
)

// dxgi_windows.go maps GDI display enumeration to DXGI output indices.
//
// ffmpeg's ddagrab selects its monitor with EnumOutputs(output_idx) on the
// capture device's adapter. That DXGI order is NOT guaranteed to match the
// EnumDisplayMonitors (kbinani) order behind contract MonitorID — observed
// fully INVERTED in the wild (kbinani 0/1 == DXGI 1/0), which made "record
// monitor A" capture monitor B. The only trustworthy bridge is matching each
// DXGI output's DesktopCoordinates against the display's virtual-desktop
// bounds, which is what dxgiOutputIndexFor does.

var (
	modDXGI                = syscall.NewLazyDLL("dxgi.dll")
	procCreateDXGIFactory1 = modDXGI.NewProc("CreateDXGIFactory1")
)

// iidIDXGIFactory1 = {770aae78-f26f-4dba-a829-253c83d1b387}
var iidIDXGIFactory1 = struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}{0x770aae78, 0xf26f, 0x4dba, [8]byte{0xa8, 0x29, 0x25, 0x3c, 0x83, 0xd1, 0xb3, 0x87}}

type winRECT struct{ Left, Top, Right, Bottom int32 }

// dxgiOutputDesc mirrors DXGI_OUTPUT_DESC.
type dxgiOutputDesc struct {
	DeviceName         [32]uint16
	DesktopCoordinates winRECT
	AttachedToDesktop  int32
	Rotation           int32
	Monitor            uintptr
}

// comObject views any COM interface pointer through its vtable.
type comObject struct {
	vtbl *[32]uintptr
}

// comCall invokes vtable method idx on a COM object.
func comCall(obj *comObject, idx int, args ...uintptr) uintptr {
	full := append([]uintptr{uintptr(unsafe.Pointer(obj))}, args...)
	r, _, _ := syscall.SyscallN(obj.vtbl[idx], full...)
	return r
}

func comRelease(obj *comObject) { comCall(obj, 2) } // IUnknown::Release

// dxgiOutputIndexFor returns the DXGI output index (== ddagrab output_idx) of
// the output on the DEFAULT adapter whose DesktopCoordinates equal d's bounds.
// ok=false when DXGI is unavailable (headless/RDP), the bounds match no output
// (rotation/mixed-DPI virtualization edge cases), or the output sits on a
// non-default adapter — callers must fall back rather than guess.
//
// Vtable slots: IDXGIFactory::EnumAdapters=7, IDXGIAdapter::EnumOutputs=7,
// IDXGIOutput::GetDesc=7 (each after IUnknown 0-2 + IDXGIObject 3-6).
func dxgiOutputIndexFor(d DisplayBounds) (int, bool) {
	var factory *comObject
	hr, _, _ := procCreateDXGIFactory1.Call(
		uintptr(unsafe.Pointer(&iidIDXGIFactory1)),
		uintptr(unsafe.Pointer(&factory)),
	)
	if int32(hr) < 0 || factory == nil {
		return 0, false
	}
	defer comRelease(factory)

	var adapter *comObject
	if int32(comCall(factory, 7, 0, uintptr(unsafe.Pointer(&adapter)))) < 0 || adapter == nil {
		return 0, false
	}
	defer comRelease(adapter)

	for i := 0; i < 16; i++ { // DXGI_ERROR_NOT_FOUND ends real enumerations; 16 is a sanity bound
		var output *comObject
		if int32(comCall(adapter, 7, uintptr(i), uintptr(unsafe.Pointer(&output)))) < 0 || output == nil {
			break
		}
		var desc dxgiOutputDesc
		hrDesc := int32(comCall(output, 7, uintptr(unsafe.Pointer(&desc))))
		comRelease(output)
		if hrDesc < 0 {
			continue
		}
		c := desc.DesktopCoordinates
		if int(c.Left) == d.X && int(c.Top) == d.Y &&
			int(c.Right-c.Left) == d.W && int(c.Bottom-c.Top) == d.H {
			return i, true
		}
	}
	return 0, false
}
