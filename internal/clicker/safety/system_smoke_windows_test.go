//go:build windows

package safety

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Read-only smoke test of the live System and ListProcesses: it queries the
// cursor, monitors, foreground app and window list and injects nothing. Opt-in:
//
//	TORU_SMOKE=1 go test ./internal/clicker/safety/ -run Smoke -v
func TestSmokeSystem(t *testing.T) {
	if os.Getenv("TORU_SMOKE") != "1" {
		t.Skip("set TORU_SMOKE=1 to query the live desktop")
	}
	s := NewSystem()

	x, y, ok := s.Cursor()
	if !ok {
		t.Fatal("GetCursorPos failed")
	}
	mons := s.Monitors()
	if len(mons) == 0 {
		t.Fatal("no monitors")
	}
	for _, m := range mons {
		if m.R <= m.L || m.B <= m.T {
			t.Errorf("empty monitor rect %+v", m)
		}
	}
	t.Logf("cursor (%d,%d), monitors %+v", x, y, mons)

	start := time.Now()
	for range 1000 {
		s.Cursor()
		s.Monitors()
		s.Foreground()
		s.TaskSwitcherActive()
	}
	t.Logf("1000 cursor+monitors+foreground+switcher reads: %v", time.Since(start))

	fg := s.Foreground()
	t.Logf("foreground %+v, self elevated %v", fg, s.SelfElevated())

	procs, err := ListProcesses()
	if err != nil {
		t.Fatal(err)
	}
	icons := 0
	for _, p := range procs {
		if len([]rune(p.Title)) > maxTitleRunes {
			t.Errorf("%s title too long: %q", p.Exe, p.Title)
		}
		if !strings.HasSuffix(p.Exe, ".exe") || p.Exe != strings.ToLower(p.Exe) {
			t.Errorf("exe not normalized: %q", p.Exe)
		}
		if p.IconURL != "" {
			icons++
		}
		t.Logf("%-28s icon=%-5v %q", p.Exe, p.IconURL != "", p.Title)
	}
	t.Logf("%d processes, %d with icons", len(procs), icons)
	start = time.Now()
	if _, err := ListProcesses(); err != nil {
		t.Fatal(err)
	}
	t.Logf("cached ListProcesses: %v", time.Since(start))
}
