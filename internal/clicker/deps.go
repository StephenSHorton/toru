package clicker

import (
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
	"github.com/StephenSHorton/toru/internal/clicker/overlay"
	"github.com/StephenSHorton/toru/internal/clicker/safety"
	"github.com/StephenSHorton/toru/internal/clicker/sys"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/StephenSHorton/toru/internal/toast"
)

// bindingSource is the part of hotkey.Manager the clicker uses. *hotkey.Manager
// satisfies it; tests pass a fake so no real keyboard hook is installed.
type bindingSource interface {
	Binding(id string) (hotkey.Binding, bool)
	Conflicts(b hotkey.Binding) (action string, conflict bool)
	SetActive(id string, on bool)
}

// deps are the service's seams to Windows, the engine, the overlays and Wails.
// The defaults call the real packages; tests replace individual fields.
type deps struct {
	// engine + input
	run             func(cfg engine.Config, inj engine.Injector, clk engine.Clock, g engine.Guard, paused func() bool, onProgress func(engine.Progress)) engine.Verdict
	newInjector     func() engine.Injector
	newClock        func() (engine.Clock, func(), error)
	newFallbackClk  func() (engine.Clock, func())
	beginRun        func() (end func(), warning string)
	keyScan         func(keyName string) (scan uint16, extended bool, isLetter bool, err error)
	capsLock        func() bool
	keyRepeat       func() engine.KeyRepeat
	doubleClickTime func() time.Duration
	cursorPos       func() (x, y int32, ok bool)

	// safety
	newGuard      func(cfg safety.Config, sys safety.System) safety.Guard
	newSystem     func() safety.System
	zoneAt        func(zones []safety.Zone, x, y int32) (action uint8, idx int)
	listProcesses func() ([]safety.ProcessInfo, error)

	// overlays and notifications
	showSafety       func(s overlay.SafetyScene, persistent bool)
	showPoints       func(pts []overlay.ScenePoint, persistent bool)
	startPick        func(kind overlay.PickKind, s overlay.SafetyScene, pts []overlay.ScenePoint, cb overlay.PickCallbacks) (cancel func(), err error)
	updatePickPoints func(pts []overlay.ScenePoint)
	indicatorShow    func(text string)
	indicatorHide    func()
	overlayShutdown  func()
	toast            func(title, body string, ok bool)

	// hotkey hooks
	physicallyDown func(vk uint16) bool
	triggerVKs     func(b hotkey.Binding) []uint16
	setInjecting   func(on bool)

	// host
	now func() time.Time
	// saveDialog asks where to write a file ("" = cancelled); openDialog asks
	// for a file to read ("" = cancelled). Both are wired to Wails in SetApp.
	saveDialog func(title, suggestedName string) (string, error)
	openDialog func(title string) (string, error)
}

// realDeps wires every seam to the production packages. Dialogs and emits need
// the running app and are filled in by SetApp.
func realDeps() deps {
	return deps{
		run:             engine.Run,
		newInjector:     sys.NewInjector,
		newClock:        sys.NewClock,
		newFallbackClk:  sys.NewFallbackClock,
		beginRun:        sys.BeginRun,
		keyScan:         sys.KeyScan,
		capsLock:        sys.CapsLock,
		keyRepeat:       sys.KeyRepeat,
		doubleClickTime: sys.DoubleClickTime,
		cursorPos:       sys.CursorPos,

		newGuard:      safety.NewGuard,
		newSystem:     safety.NewSystem,
		zoneAt:        safety.ZoneAt,
		listProcesses: safety.ListProcesses,

		showSafety:       overlay.ShowSafety,
		showPoints:       overlay.ShowPoints,
		startPick:        overlay.StartPick,
		updatePickPoints: overlay.UpdatePickPoints,
		indicatorShow:    overlay.IndicatorShow,
		indicatorHide:    overlay.IndicatorHide,
		overlayShutdown:  overlay.Shutdown,
		toast:            toast.Show,

		physicallyDown: hotkey.PhysicallyDown,
		triggerVKs:     hotkey.TriggerVKs,
		setInjecting:   hotkey.SetInjecting,

		now: time.Now,
		saveDialog: func(string, string) (string, error) {
			return "", clickerErr(ErrWinAPI, "no running application")
		},
		openDialog: func(string) (string, error) {
			return "", clickerErr(ErrWinAPI, "no running application")
		},
	}
}
