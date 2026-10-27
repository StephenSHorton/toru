//go:build !windows

package sys

// Non-Windows stubs: the clicker only runs on Windows.

import (
	"errors"
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
)

var errNotBuilt = errors.New("clicker/sys: not built yet")

func NewInjector() engine.Injector                       { return nil }
func NewClock() (engine.Clock, func(), error)            { return nil, func() {}, errNotBuilt }
func NewFallbackClock() (engine.Clock, func())           { return nil, func() {} }
func BeginRun() (end func(), warning string)             { return func() {}, "" }
func KeyScan(keyName string) (uint16, bool, bool, error) { return 0, false, false, errNotBuilt }
func CapsLock() bool                                     { return false }
func KeyRepeat() engine.KeyRepeat                        { return engine.KeyRepeat{} }
func DoubleClickTime() time.Duration                     { return 500 * time.Millisecond }
func VirtualScreen() (x, y, w, h int32)                  { return 0, 0, 0, 0 }
func CursorPos() (x, y int32, ok bool)                   { return 0, 0, false }
