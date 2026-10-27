package safety

import (
	"testing"

	"github.com/StephenSHorton/toru/internal/clicker/engine"
)

// fakeSystem is a scripted System.
type fakeSystem struct {
	x, y      int32
	cursorOK  bool
	mons      []Rect
	fg        Foreground
	switcher  bool
	down      map[uint16]bool
	elevated  bool
	monsCalls int
}

func (f *fakeSystem) Cursor() (int32, int32, bool)  { return f.x, f.y, f.cursorOK }
func (f *fakeSystem) Monitors() []Rect              { f.monsCalls++; return f.mons }
func (f *fakeSystem) Foreground() Foreground        { return f.fg }
func (f *fakeSystem) TaskSwitcherActive() bool      { return f.switcher }
func (f *fakeSystem) PhysicallyDown(vk uint16) bool { return f.down[vk] }
func (f *fakeSystem) SelfElevated() bool            { return f.elevated }

const holdVK = 0x75 // F6

// everything trips at (5, 5) on a 1000x1000 monitor: a stop zone, a corner, an
// edge, the task switcher, the block list, the hold release, leaving start zone
// 1 and a pause zone. Each step of the test clears the winner to expose the next.
func everythingTrips() (Config, *fakeSystem) {
	cfg := Config{
		Corners: allCorners(20),
		Edges:   allEdges(20, true),
		Zones: []Zone{
			{Rect: Rect{0, 0, 10, 10}, Action: ZoneStop, Index: 0},
			{Rect: Rect{500, 500, 600, 600}, Action: ZoneStart, Index: 1},
			{Rect: Rect{0, 0, 50, 50}, Action: ZonePause, Index: 2},
		},
		TaskSwitcher:   true,
		Process:        ProcessRule{Enabled: true, Allow: false, Exes: []string{"Game"}},
		HoldTriggerVK:  holdVK,
		StartZoneIndex: 1,
	}
	sys := &fakeSystem{
		x: 5, y: 5, cursorOK: true,
		mons:     []Rect{{0, 0, 1000, 1000}},
		fg:       Foreground{Exe: "game.exe", OK: true},
		switcher: true,
		down:     map[uint16]bool{},
	}
	return cfg, sys
}

func TestGuardOrder(t *testing.T) {
	cfg, sys := everythingTrips()
	steps := []struct {
		want  engine.Verdict
		clear func()
	}{
		{engine.Verdict{Kind: engine.Stop, Code: "zoneStop", Reason: "Stop zone 1"},
			func() { cfg.Zones[0].Action = ZonePause }},
		{engine.Verdict{Kind: engine.Stop, Code: "corner", Reason: "Top-left corner failsafe"},
			func() { cfg.Corners.Enabled = false }},
		{engine.Verdict{Kind: engine.Stop, Code: "edge", Reason: "Top edge failsafe"},
			func() { cfg.Edges.Enabled = false }},
		{engine.Verdict{Kind: engine.Stop, Code: "taskSwitcher", Reason: "Alt+Tab"},
			func() { sys.switcher = false }},
		{engine.Verdict{Kind: engine.Stop, Code: "processList", Reason: "game.exe is in the block list"},
			func() { cfg.Process.Enabled = false }},
		{engine.Verdict{Kind: engine.Stop, Code: "holdRelease", Reason: "Hotkey released"},
			func() { sys.down[holdVK] = true }},
		{engine.Verdict{Kind: engine.Stop, Code: "zoneLeft", Reason: "Left start zone 2"},
			func() { cfg.StartZoneIndex = -1 }},
		{engine.Verdict{Kind: engine.Pause, Reason: "Pause zone 1"},
			func() { cfg.Zones = nil }},
		{engine.Verdict{}, nil},
	}
	for i, s := range steps {
		got := NewGuard(cfg, sys).Check(0, 0)
		if got != s.want {
			t.Fatalf("step %d: got %+v, want %+v", i, got, s.want)
		}
		if s.clear != nil {
			s.clear()
		}
	}
}

func TestGuardStartZoneCarvesPause(t *testing.T) {
	sys := &fakeSystem{x: 60, y: 60, cursorOK: true}
	cfg := Config{Zones: []Zone{
		{Rect: Rect{0, 0, 100, 100}, Action: ZonePause, Index: 0},
		{Rect: Rect{50, 50, 150, 150}, Action: ZoneStart, Index: 1},
	}, StartZoneIndex: -1}
	if v := NewGuard(cfg, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("start over pause: %+v", v)
	}
	sys.x, sys.y = 10, 10
	if v := NewGuard(cfg, sys).Check(0, 0); v.Kind != engine.Pause || v.Reason != "Pause zone 1" {
		t.Fatalf("pause only: %+v", v)
	}
}

func TestGuardStartZoneLeft(t *testing.T) {
	sys := &fakeSystem{x: 60, y: 60, cursorOK: true}
	cfg := Config{Zones: []Zone{
		{Rect: Rect{0, 0, 10, 10}, Action: ZonePause, Index: 0},
		{Rect: Rect{50, 50, 150, 150}, Action: ZoneStart, Index: 3},
	}, StartZoneIndex: 3}
	g := NewGuard(cfg, sys)
	if v := g.Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("inside start zone: %+v", v)
	}
	sys.x = 150
	if v := g.Check(0, 0); v.Code != "zoneLeft" || v.Reason != "Left start zone 4" {
		t.Fatalf("left start zone: %+v", v)
	}
	// StartZoneIndex naming a zone that is not a start zone does nothing.
	cfg.StartZoneIndex = 0
	if v := NewGuard(cfg, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("non-start zone index: %+v", v)
	}
	cfg.StartZoneIndex = 7
	if v := NewGuard(cfg, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("missing zone index: %+v", v)
	}
	// A zero StartZoneIndex is not "unset": when zone 0 is a start zone it arms
	// zoneLeft, so callers whose run was not started by a zone must pass -1.
	zero := Config{Zones: []Zone{{Rect: Rect{0, 0, 10, 10}, Action: ZoneStart, Index: 0}}}
	if v := NewGuard(zero, sys).Check(0, 0); v.Code != "zoneLeft" || v.Reason != "Left start zone 1" {
		t.Fatalf("zero StartZoneIndex with start zone 0: %+v", v)
	}
	zero.StartZoneIndex = -1
	if v := NewGuard(zero, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("StartZoneIndex -1: %+v", v)
	}
	// An unknown cursor never stops a run.
	cfg.StartZoneIndex = 3
	sys.cursorOK = false
	if v := NewGuard(cfg, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("no cursor: %+v", v)
	}
}

func TestGuardNoCursorSkipsGeometry(t *testing.T) {
	cfg, sys := everythingTrips()
	sys.cursorOK = false
	sys.switcher = false
	cfg.Process.Enabled = false
	sys.down[holdVK] = true
	if v := NewGuard(cfg, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("got %+v", v)
	}
	if sys.monsCalls != 0 {
		t.Errorf("Monitors called %d times without a cursor", sys.monsCalls)
	}
}

func TestGuardSkipsMonitorsWhenOff(t *testing.T) {
	sys := &fakeSystem{x: 5, y: 5, cursorOK: true, mons: []Rect{{0, 0, 100, 100}}}
	if v := NewGuard(Config{StartZoneIndex: -1}, sys).Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("got %+v", v)
	}
	if sys.monsCalls != 0 {
		t.Errorf("Monitors called with corners and edges off")
	}
}

func TestProcessRule(t *testing.T) {
	cases := []struct {
		name  string
		allow bool
		exes  []string
		fg    Foreground
		want  string
	}{
		{"block listed", false, []string{"game.exe"}, Foreground{Exe: "game.exe", OK: true}, "game.exe is in the block list"},
		{"block other", false, []string{"game.exe"}, Foreground{Exe: "notepad.exe", OK: true}, ""},
		{"allow listed", true, []string{"game.exe"}, Foreground{Exe: "game.exe", OK: true}, ""},
		{"allow other", true, []string{"game.exe"}, Foreground{Exe: "notepad.exe", OK: true}, "notepad.exe is not in the allow list"},
		{"allow empty list", true, nil, Foreground{Exe: "game.exe", OK: true}, "game.exe is not in the allow list"},
		{"normalized entry", false, []string{"  Game  "}, Foreground{Exe: "game.exe", OK: true}, "game.exe is in the block list"},
		{"path entry", false, []string{`C:\Games\GAME.EXE`}, Foreground{Exe: "game.exe", OK: true}, "game.exe is in the block list"},
		{"unknown foreground", true, []string{"game.exe"}, Foreground{}, ""},
		{"unreadable exe", true, []string{"game.exe"}, Foreground{OK: true, Class: "X"}, ""},
	}
	for _, tc := range cases {
		cfg := Config{Process: ProcessRule{Enabled: true, Allow: tc.allow, Exes: tc.exes}, StartZoneIndex: -1}
		sys := &fakeSystem{fg: tc.fg}
		v := NewGuard(cfg, sys).Check(0, 0)
		if v.Reason != tc.want {
			t.Errorf("%s: reason %q, want %q", tc.name, v.Reason, tc.want)
		}
		if tc.want != "" && (v.Kind != engine.Stop || v.Code != "processList") {
			t.Errorf("%s: verdict %+v", tc.name, v)
		}
	}
}

func TestNormalizeExe(t *testing.T) {
	for in, want := range map[string]string{
		"Game":           "game.exe",
		"game.EXE":       "game.exe",
		" a b.exe ":      "a b.exe",
		`C:\x\Steam.exe`: "steam.exe",
		"":               "",
		"   ":            "",
		`C:\dir\`:        "",
		"javaw.exe":      "javaw.exe",
	} {
		if got := NormalizeExe(in); got != want {
			t.Errorf("NormalizeExe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWarning(t *testing.T) {
	sys := &fakeSystem{fg: Foreground{Exe: "admin.exe", Elevated: true, OK: true}}
	g := NewGuard(Config{StartZoneIndex: -1}, sys)
	if w := g.Warning(); w != "admin.exe runs as administrator; Windows blocks clicks into it" {
		t.Errorf("elevated target: %q", w)
	}
	sys.elevated = true
	if w := g.Warning(); w != "" {
		t.Errorf("Toru elevated too: %q", w)
	}
	sys.elevated = false
	sys.fg.Elevated = false
	if w := g.Warning(); w != "" {
		t.Errorf("unelevated target: %q", w)
	}
	sys.fg = Foreground{Elevated: true, OK: true}
	if w := g.Warning(); w != "The app in front runs as administrator; Windows blocks clicks into it" {
		t.Errorf("unnamed elevated target: %q", w)
	}
}

func TestNilSystemContinues(t *testing.T) {
	cfg, _ := everythingTrips()
	g := NewGuard(cfg, nil)
	if v := g.Check(0, 0); v.Kind != engine.Continue {
		t.Fatalf("nil system: %+v", v)
	}
	if w := g.Warning(); w != "" {
		t.Fatalf("nil system warning: %q", w)
	}
}
