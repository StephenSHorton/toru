package stream

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/sys/windows"
)

// lan_windows.go: the service manager and the elevated helper. Toru runs
// unelevated, so it opens the service manager with SC_MANAGER_CONNECT and
// the service with only the rights it needs (svc/mgr's Connect and
// OpenService ask for full access, which only an administrator gets).

type winLanOS struct{}

func defaultLanOS() lanOS { return winLanOS{} }

func (winLanOS) supported() bool { return true }

// openLanService opens ToruLan with access, or reports it missing.
func openLanService(access uint32) (windows.Handle, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return 0, err
	}
	defer windows.CloseServiceHandle(m)
	name, err := windows.UTF16PtrFromString(api.ServiceName)
	if err != nil {
		return 0, err
	}
	return windows.OpenService(m, name, access)
}

func (winLanOS) query() lanSvcInfo {
	h, err := openLanService(windows.SERVICE_QUERY_STATUS)
	switch {
	case errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST):
		return lanSvcInfo{}
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return lanSvcInfo{installed: true, denied: true}
	case err != nil:
		logf("LAN service query: %v", err)
		return lanSvcInfo{}
	}
	defer windows.CloseServiceHandle(h)
	info := lanSvcInfo{installed: true}
	var st windows.SERVICE_STATUS
	if windows.QueryServiceStatus(h, &st) == nil {
		info.running = st.CurrentState == windows.SERVICE_RUNNING
	}
	// Starting it is the right that matters; check we have it.
	if hs, err := openLanService(windows.SERVICE_START); err == nil {
		windows.CloseServiceHandle(hs)
	} else if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		info.denied = true
	}
	return info
}

func (winLanOS) start() error {
	h, err := openLanService(windows.SERVICE_START)
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return errors.New("Windows won't let Toru start the LAN service; run Update LAN")
		}
		return fmt.Errorf("open the LAN service: %w", err)
	}
	defer windows.CloseServiceHandle(h)
	if err := windows.StartService(h, 0, nil); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("start the LAN service: %w", err)
	}
	return nil
}

func (winLanOS) runInstaller(verb string) error {
	exe, err := findLanExe()
	if err != nil {
		return err
	}
	code, err := runElevated(exe, verb, filepath.Dir(exe))
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("%s %s failed (exit code %d)", api.LanExe, verb, code)
	}
	return nil
}

// shellExecuteInfo is SHELLEXECUTEINFOW.
type shellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         windows.Handle
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     windows.Handle
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    windows.Handle
	dwHotKey     uint32
	hIcon        windows.Handle // union with hMonitor
	hProcess     windows.Handle
}

const (
	seeMaskNoCloseProcess = 0x00000040
	seeMaskNoAsync        = 0x00000100
	seeMaskFlagNoUI       = 0x00000400
)

var procShellExecuteExW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

// runElevated runs file with args through the "runas" verb (one UAC
// prompt), hidden, waits for it and returns its exit code. A declined
// prompt is errUACCancelled.
func runElevated(file, args, dir string) (uint32, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	f, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return 0, err
	}
	a, _ := windows.UTF16PtrFromString(args)
	d, _ := windows.UTF16PtrFromString(dir)
	info := shellExecuteInfo{
		fMask:        seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskFlagNoUI,
		lpVerb:       verb,
		lpFile:       f,
		lpParameters: a,
		lpDirectory:  d,
		nShow:        windows.SW_HIDE,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))
	r, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return 0, errUACCancelled
		}
		return 0, fmt.Errorf("run %s: %w", filepath.Base(file), callErr)
	}
	if info.hProcess == 0 {
		return 0, errors.New("run " + filepath.Base(file) + ": no process")
	}
	defer windows.CloseHandle(info.hProcess)
	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return 0, err
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return 0, err
	}
	return code, nil
}
