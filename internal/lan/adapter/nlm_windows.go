package adapter

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"
)

// nlm_windows.go marks the adapter's network Private through the Network
// List Manager. Without it a new adapter is an "Unidentified network" on the
// Public firewall profile. The approach is Tailscale's setPrivateNetwork
// (wgengine/router/osrouter/ifconfig_windows.go and wgengine/winnet, BSD-3),
// reimplemented here: enumerate network connections, match the adapter GUID,
// SetCategory(Private) on its network.

const clsidNetworkListManager = "{DCB00C01-570F-4A9B-8D69-199FDBA5723B}"

var iidINetworkConnection = ole.NewGUID("{DCB00005-570F-4A9B-8D69-199FDBA5723B}")

// NLM_NETWORK_CATEGORY values.
const (
	CategoryPublic  = 0
	CategoryPrivate = 1
	CategoryDomain  = 2
)

// Vtable layouts from netlistmgr.h; both are dual interfaces, so the
// methods follow IDispatch's seven slots.
type iNetworkConnectionVtbl struct {
	ole.IDispatchVtbl
	GetNetwork            uintptr
	IsConnectedToInternet uintptr
	IsConnected           uintptr
	GetConnectivity       uintptr
	GetConnectionID       uintptr
	GetAdapterID          uintptr
	GetDomainType         uintptr
}

type iNetworkVtbl struct {
	ole.IDispatchVtbl
	GetName                    uintptr
	SetName                    uintptr
	GetDescription             uintptr
	SetDescription             uintptr
	GetNetworkID               uintptr
	GetDomainType              uintptr
	GetNetworkConnections      uintptr
	GetTimeCreatedAndConnected uintptr
	IsConnectedToInternet      uintptr
	IsConnected                uintptr
	GetConnectivity            uintptr
	GetCategory                uintptr
	SetCategory                uintptr
}

// withCOM runs fn on a locked OS thread with COM initialized (MTA).
func withCOM(fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED); err != nil {
		var oe *ole.OleError
		if !errors.As(err, &oe) || oe.Code() != 1 { // S_FALSE: already initialized
			return fmt.Errorf("CoInitializeEx: %w", err)
		}
	}
	defer ole.CoUninitialize()
	return fn()
}

func hresult(r uintptr) error {
	if int32(r) < 0 {
		return ole.NewError(r)
	}
	return nil
}

// networkOf finds the INetwork for the adapter with GUID g, or nil if NLA has
// no connection for it yet. The caller releases it.
func networkOf(g windows.GUID) (*ole.IDispatch, error) {
	unk, err := oleutil.CreateObject(clsidNetworkListManager)
	if err != nil {
		return nil, fmt.Errorf("create NetworkListManager: %w", err)
	}
	defer unk.Release()
	nlm, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		return nil, err
	}
	defer nlm.Release()
	v, err := oleutil.CallMethod(nlm, "GetNetworkConnections")
	if err != nil {
		return nil, fmt.Errorf("GetNetworkConnections: %w", err)
	}
	defer func() { _ = v.Clear() }()
	enum := v.ToIDispatch()
	if enum == nil {
		return nil, errors.New("GetNetworkConnections: not IDispatch")
	}
	want := strings.ToUpper(g.String())
	var found *ole.IDispatch
	err = oleutil.ForEach(enum, func(item *ole.VARIANT) error {
		defer func() { _ = item.Clear() }() // ForEach hands over a reference and never releases it
		if found != nil {
			return nil
		}
		iu := item.ToIUnknown()
		if iu == nil {
			return nil
		}
		nc, err := iu.QueryInterface(iidINetworkConnection)
		if err != nil {
			return err
		}
		defer nc.Release()
		vt := (*iNetworkConnectionVtbl)(unsafe.Pointer(nc.RawVTable))
		var id ole.GUID
		if err := hresult(syscallN(vt.GetAdapterID, uintptr(unsafe.Pointer(nc)), uintptr(unsafe.Pointer(&id)))); err != nil {
			return fmt.Errorf("GetAdapterId: %w", err)
		}
		if strings.ToUpper(id.String()) != want {
			return nil
		}
		var net *ole.IDispatch
		if err := hresult(syscallN(vt.GetNetwork, uintptr(unsafe.Pointer(nc)), uintptr(unsafe.Pointer(&net)))); err != nil {
			return fmt.Errorf("GetNetwork: %w", err)
		}
		found = net
		return nil
	})
	if err != nil {
		if found != nil {
			found.Release()
		}
		return nil, err
	}
	return found, nil
}

func syscallN(fn uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(fn, args...)
	return r
}

func getCategory(net *ole.IDispatch) (int32, error) {
	vt := (*iNetworkVtbl)(unsafe.Pointer(net.RawVTable))
	var cat int32
	if err := hresult(syscallN(vt.GetCategory, uintptr(unsafe.Pointer(net)), uintptr(unsafe.Pointer(&cat)))); err != nil {
		return 0, fmt.Errorf("GetCategory: %w", err)
	}
	return cat, nil
}

// setPrivate sets the adapter's network to Private (Domain is left alone).
// found is false while NLA has not produced a network for it.
func setPrivate(g windows.GUID) (found bool, err error) {
	err = withCOM(func() error {
		net, err := networkOf(g)
		if err != nil || net == nil {
			return err
		}
		defer net.Release()
		found = true
		cat, err := getCategory(net)
		if err != nil {
			return err
		}
		if cat == CategoryPrivate || cat == CategoryDomain {
			return nil
		}
		vt := (*iNetworkVtbl)(unsafe.Pointer(net.RawVTable))
		if err := hresult(syscallN(vt.SetCategory, uintptr(unsafe.Pointer(net)), CategoryPrivate)); err != nil {
			return fmt.Errorf("SetCategory: %w", err)
		}
		return nil
	})
	return found, err
}

// Category returns the adapter network's NLM category; found is false while
// NLA has no network for it. For the selftest and diagnostics.
func Category() (cat int32, found bool, err error) {
	err = withCOM(func() error {
		net, err := networkOf(GUID)
		if err != nil || net == nil {
			return err
		}
		defer net.Release()
		found = true
		cat, err = getCategory(net)
		return err
	})
	return cat, found, err
}
