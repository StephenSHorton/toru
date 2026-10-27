//go:build !windows

package toast

// Toru only ships on Windows; these keep cross-compiles and tests building.

// Post is a no-op off Windows.
func Post(Note) {}

// Close is a no-op off Windows.
func Close(string) {}

// Show is a no-op off Windows.
func Show(string, string, bool) {}

// HoldClickThrough is a no-op off Windows.
func HoldClickThrough() (release func()) { return func() {} }

// Visible always reports false off Windows.
func Visible() bool { return false }

func optionsChanged(Options) {}

func playWAV([]byte) {}
