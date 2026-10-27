package safety

// S0 placeholder: S3 replaces this file with the real implementation.

import "github.com/StephenSHorton/toru/internal/clicker/engine"

type nopGuard struct{}

func (nopGuard) Check(int64, int64) engine.Verdict { return engine.Verdict{} }
func (nopGuard) Warning() string                   { return "" }

// NewGuard builds the failsafe guard for one run.
func NewGuard(cfg Config, sys System) Guard { return nopGuard{} }

// NewSystem returns the live Windows System (a no-op one elsewhere).
func NewSystem() System { return nil }

// ZoneAt returns the winning zone action under (x, y) (stop > start > pause)
// and its index into zones, or (0, -1).
func ZoneAt(zones []Zone, x, y int32) (action uint8, idx int) { return 0, -1 }

// PointInFailsafe reports whether (x, y) falls in a corner or edge band, for the
// click-point warnings in the UI.
func PointInFailsafe(c Corners, e Edges, mons []Rect, x, y int32) bool { return false }

// ListProcesses returns running apps with a visible top-level window, with icons.
func ListProcesses() ([]ProcessInfo, error) { return nil, nil }
