//go:build !windows

package safety

// Toru only ships on Windows; these keep cross-compiles and tests building.

// NewSystem returns a System that reports nothing off Windows.
func NewSystem() System { return nopSystem{} }

// ListProcesses lists nothing off Windows.
func ListProcesses() ([]ProcessInfo, error) { return nil, nil }
