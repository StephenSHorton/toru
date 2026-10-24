//go:build windows

package replay

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

// shellClasses are the desktop / taskbar windows: focus there means no app.
var shellClasses = map[string]bool{"Progman": true, "WorkerW": true, "Shell_TrayWnd": true}

// foreground returns the app whose window has focus, or false when it's the
// desktop or nothing.
func foreground() (foregroundApp, bool) {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return foregroundApp{}, false
	}
	h := w32.HWND(hwnd)
	if shellClasses[w32.GetClassName(h)] {
		return foregroundApp{}, false
	}
	app := foregroundApp{Title: w32.GetWindowText(h), Fullscreen: coversMonitor(h)}

	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err == nil && pid != 0 {
		app.PID = pid
		app.ExePath = processImagePath(pid)
	}
	if app.ExePath != "" {
		app.Description = versionString(app.ExePath, "FileDescription")
		if app.Description == "" {
			app.Description = versionString(app.ExePath, "ProductName")
		}
	}
	return app, true
}

// processAlive reports whether pid is still running.
func processAlive(pid uint32) bool {
	if pid == 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		// Access denied means it exists (e.g. protected anti-cheat process).
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// coversMonitor reports whether the window spans its whole monitor, which is
// true for exclusive fullscreen and borderless-windowed games alike.
func coversMonitor(h w32.HWND) bool {
	r := w32.GetWindowRect(h)
	mi := w32.GetMonitorInfoForWindow(h)
	if r == nil || mi == nil {
		return false
	}
	m := mi.RcMonitor
	return r.Left <= m.Left && r.Top <= m.Top && r.Right >= m.Right && r.Bottom >= m.Bottom
}

// processImagePath returns pid's exe path. PROCESS_QUERY_LIMITED_INFORMATION
// also works on elevated games from an unelevated Toru.
func processImagePath(pid uint32) string {
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	buf := make([]uint16, windows.MAX_LONG_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(proc, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// versionString reads one StringFileInfo value (e.g. "FileDescription") from
// the exe's version resource, in its first declared language.
func versionString(path, key string) string {
	size, err := windows.GetFileVersionInfoSize(path, nil)
	if err != nil || size == 0 {
		return ""
	}
	data := make([]byte, size)
	if err := windows.GetFileVersionInfo(path, 0, size, unsafe.Pointer(&data[0])); err != nil {
		return ""
	}
	sub := []string{}
	var trans *[2]uint16
	var transLen uint32
	if err := windows.VerQueryValue(unsafe.Pointer(&data[0]), `\VarFileInfo\Translation`, unsafe.Pointer(&trans), &transLen); err == nil && transLen >= 4 && trans != nil {
		sub = append(sub, fmt.Sprintf(`\StringFileInfo\%04x%04x\%s`, trans[0], trans[1], key))
	}
	sub = append(sub, `\StringFileInfo\040904b0\`+key, `\StringFileInfo\040904e4\`+key)
	for _, s := range sub {
		var val *uint16
		var n uint32
		if err := windows.VerQueryValue(unsafe.Pointer(&data[0]), s, unsafe.Pointer(&val), &n); err == nil && val != nil && n > 0 {
			return windows.UTF16PtrToString(val)
		}
	}
	return ""
}
