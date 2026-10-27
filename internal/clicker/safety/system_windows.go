//go:build windows

package safety

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/StephenSHorton/toru/internal/hotkey"
	"golang.org/x/sys/windows"
)

// The live System. The guard asks it for everything at most every 10 ms, so the
// costly answers are cached: the monitor list for monitorTTL, and the
// foreground app's exe, class and elevation until the foreground window (or its
// process) changes. GetCursorPos and GetForegroundWindow themselves are cheap.

var (
	user32 = windows.NewLazySystemDLL("user32.dll")

	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
)

const (
	monitorTTL = time.Second

	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79

	vkTab   = 0x09
	vkAlt   = 0x12 // VK_MENU
	vkLAlt  = 0xA4
	vkRAlt  = 0xA5
	maxPath = 32768
)

// taskSwitcherPrefixes are foreground window classes of the Alt+Tab and Win+Tab
// UIs. XamlExplorerHostIslandWindow hosts Windows 11's Alt+Tab and Task View.
var taskSwitcherPrefixes = []string{
	"TaskSwitcherWnd",
	"TaskViewWindow",
	"WindowsSwitchWindow",
	"XamlExplorerHostIslandWindow",
}

type winSystem struct {
	mu     sync.Mutex
	mons   []Rect
	monsAt time.Time

	fgHwnd windows.HWND
	fgPid  uint32
	fg     Foreground

	selfOnce sync.Once
	self     bool
}

// NewSystem returns the live Windows System.
func NewSystem() System { return &winSystem{} }

func (s *winSystem) Cursor() (x, y int32, ok bool) {
	var pt struct{ X, Y int32 }
	r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	return pt.X, pt.Y, r != 0
}

// Monitors returns every display's rcMonitor sorted by (top, left), cached for
// monitorTTL. The returned slice is shared: callers must not modify it.
func (s *winSystem) Monitors() []Rect {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.mons != nil && time.Since(s.monsAt) < monitorTTL {
		return s.mons
	}
	s.mons = enumMonitors()
	s.monsAt = time.Now()
	return s.mons
}

func (s *winSystem) Foreground() Foreground {
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return Foreground{}
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil || pid == 0 {
		return Foreground{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if hwnd == s.fgHwnd && pid == s.fgPid {
		return s.fg
	}
	s.fgHwnd, s.fgPid = hwnd, pid
	s.fg = s.resolveForeground(hwnd, pid)
	return s.fg
}

func (s *winSystem) resolveForeground(hwnd windows.HWND, pid uint32) Foreground {
	fg := Foreground{Class: windowClass(hwnd), OK: true}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		fg.Elevated = s.deniedMeansElevated(err)
		return fg
	}
	defer windows.CloseHandle(h) //nolint:errcheck // best-effort close
	if path := processPath(h); path != "" {
		fg.Exe = exeName(path)
	}
	var tok windows.Token
	if err := windows.OpenProcessToken(h, windows.TOKEN_QUERY, &tok); err != nil {
		fg.Elevated = s.deniedMeansElevated(err)
		return fg
	}
	fg.Elevated = tok.IsElevated()
	tok.Close() //nolint:errcheck // best-effort close
	return fg
}

// deniedMeansElevated: an unelevated Toru can read the token of the user's own
// admin apps (TokenElevation answers for those), but is refused outright by
// SYSTEM and protected processes. Those sit above Toru's integrity level too, the
// same boundary (UIPI) that drops our input, so access denied counts as elevated
// for the warning.
func (s *winSystem) deniedMeansElevated(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) && !s.SelfElevated()
}

func (s *winSystem) TaskSwitcherActive() bool {
	if fg := s.Foreground(); fg.OK && isTaskSwitcherClass(fg.Class) {
		return true
	}
	// TODO(S5): hotkey.PhysicallyDown is the S0 stub (always false), so only
	// the foreground-class check trips today. S5 must answer the generic
	// VK_MENU (0x12) as well as the sided 0xA4/0xA5 queried here.
	altDown := hotkey.PhysicallyDown(vkAlt) || hotkey.PhysicallyDown(vkLAlt) || hotkey.PhysicallyDown(vkRAlt)
	return altDown && hotkey.PhysicallyDown(vkTab)
}

// isTaskSwitcherClass also matches shell CoreWindows (Start, search, the action
// centre): leaving the game for any of them should stop a run too.
func isTaskSwitcherClass(class string) bool {
	for _, p := range taskSwitcherPrefixes {
		if strings.HasPrefix(class, p) {
			return true
		}
	}
	return strings.Contains(class, "CoreWindow")
}

// PhysicallyDown ignores Toru's own injected input (see hotkey.PhysicallyDown).
func (s *winSystem) PhysicallyDown(vk uint16) bool { return hotkey.PhysicallyDown(vk) }

func (s *winSystem) SelfElevated() bool {
	s.selfOnce.Do(func() { s.self = windows.GetCurrentProcessToken().IsElevated() })
	return s.self
}

// ---------------------------------------------------------------------------
// Monitor enumeration. syscall.NewCallback slots are never freed, so the
// callback is made once and fills a package buffer under enumMonMu.
// ---------------------------------------------------------------------------

var (
	enumMonMu  sync.Mutex
	enumMonBuf []Rect
	enumMonCB  = syscall.NewCallback(func(hmon, _, _, _ uintptr) uintptr {
		var mi struct {
			Size          uint32
			Monitor, Work struct{ L, T, R, B int32 }
			Flags         uint32
		}
		mi.Size = uint32(unsafe.Sizeof(mi))
		if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r != 0 {
			m := mi.Monitor
			enumMonBuf = append(enumMonBuf, Rect{L: m.L, T: m.T, R: m.R, B: m.B})
		}
		return 1
	})
)

// enumMonitors lists rcMonitor of every display, falling back to the virtual
// screen as one monitor when enumeration finds none.
func enumMonitors() []Rect {
	enumMonMu.Lock()
	enumMonBuf = nil
	procEnumDisplayMonitors.Call(0, 0, enumMonCB, 0) //nolint:errcheck // result read from the buffer
	mons := enumMonBuf
	enumMonBuf = nil
	enumMonMu.Unlock()

	if len(mons) == 0 {
		x := metric(smXVirtualScreen)
		y := metric(smYVirtualScreen)
		return []Rect{{L: x, T: y, R: x + metric(smCXVirtualScreen), B: y + metric(smCYVirtualScreen)}}
	}
	sort.Slice(mons, func(i, j int) bool {
		if mons[i].T != mons[j].T {
			return mons[i].T < mons[j].T
		}
		return mons[i].L < mons[j].L
	})
	return mons
}

func metric(i uintptr) int32 {
	r, _, _ := procGetSystemMetrics.Call(i)
	return int32(r)
}

// ---------------------------------------------------------------------------
// Window and process helpers shared with ListProcesses.
// ---------------------------------------------------------------------------

func windowClass(hwnd windows.HWND) string {
	var buf [256]uint16
	n, err := windows.GetClassName(hwnd, &buf[0], int32(len(buf)))
	if err != nil || n <= 0 {
		return ""
	}
	return windows.UTF16ToString(buf[:n])
}

// processPath is the full image path of an open process handle, or "".
func processPath(h windows.Handle) string {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n := uint32(len(buf))
		err := windows.QueryFullProcessImageName(h, 0, &buf[0], &n)
		if err == nil {
			return windows.UTF16ToString(buf[:n])
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || len(buf) >= maxPath {
			return ""
		}
		buf = make([]uint16, maxPath)
	}
}

// exeName is the lowercase file name of a full image path.
func exeName(path string) string {
	if i := strings.LastIndexAny(path, `\/`); i >= 0 {
		path = path[i+1:]
	}
	return strings.ToLower(path)
}
