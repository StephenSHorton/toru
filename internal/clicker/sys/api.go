// Package sys is the auto clicker's Win32 layer: SendInput injection (every
// event tagged with inputtag.Toru), a high-resolution waitable-timer Clock, key
// name to scan code lookup, and per-run thread/timer setup. Off Windows every
// function is a harmless stub so the module builds and vets everywhere.
//
// API (implemented per platform):
//
//	func NewInjector() engine.Injector
//	func NewClock() (clk engine.Clock, closeFn func(), err error) // err => the caller may use NewFallbackClock
//	func NewFallbackClock() (clk engine.Clock, closeFn func())    // low-precision timer; caller sets a warning
//	func BeginRun() (end func(), warning string)                  // lock thread, priority, power throttling; call on the run goroutine
//	func KeyScan(keyName string) (scan uint16, extended bool, isLetter bool, err error) // hotkey.KeyName spelling
//	func CapsLock() bool
//	func KeyRepeat() engine.KeyRepeat
//	func DoubleClickTime() time.Duration
//	func VirtualScreen() (x, y, w, h int32)
//	func CursorPos() (x, y int32, ok bool)
package sys
