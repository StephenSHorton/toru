package clicker

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

var errTest = errors.New("test error")

func TestNormalizeCadence(t *testing.T) {
	cases := []struct {
		name     string
		in       Cadence
		high     bool
		wantRate float64
		wantUnit RateUnit
		wantIv   float64
	}{
		{"default", Cadence{Mode: CadenceRate, Rate: 25, Unit: UnitSecond}, false, 25, UnitSecond, 40},
		{"rate per minute", Cadence{Mode: CadenceRate, Rate: 30, Unit: UnitMinute}, false, 30, UnitMinute, 2000},
		{"rate per hour", Cadence{Mode: CadenceRate, Rate: 1, Unit: UnitHour}, false, 1, UnitHour, 3600e3},
		{"rate per day", Cadence{Mode: CadenceRate, Rate: 2, Unit: UnitDay}, false, 2, UnitDay, 43200e3},
		{"rate ceiling", Cadence{Mode: CadenceRate, Rate: 900, Unit: UnitSecond}, false, 500, UnitSecond, 2},
		{"rate ceiling high", Cadence{Mode: CadenceRate, Rate: 2000, Unit: UnitSecond}, true, 1000, UnitSecond, 1},
		{"ceiling is per unit", Cadence{Mode: CadenceRate, Rate: 60000, Unit: UnitMinute}, false, 500, UnitMinute, 120},
		{"zero rate -> default", Cadence{Mode: CadenceRate, Rate: 0, Unit: UnitSecond}, false, 25, UnitSecond, 40},
		{"nan rate -> default", Cadence{Mode: CadenceRate, Rate: math.NaN(), Unit: UnitSecond}, false, 25, UnitSecond, 40},
		{"bad unit", Cadence{Mode: CadenceRate, Rate: 10, Unit: "w"}, false, 10, UnitSecond, 100},
		{"interval 40ms", Cadence{Mode: CadenceInterval, IntervalMs: 40}, false, 25, UnitSecond, 40},
		{"interval 1s", Cadence{Mode: CadenceInterval, IntervalMs: 1000}, false, 1, UnitSecond, 1000},
		{"interval 2min", Cadence{Mode: CadenceInterval, IntervalMs: 120e3}, false, 30, UnitHour, 120e3},
		{"interval 90s is 40/h exactly", Cadence{Mode: CadenceInterval, IntervalMs: 90e3}, false, 40, UnitHour, 90e3},
		{"interval 1 day", Cadence{Mode: CadenceInterval, IntervalMs: 86400e3}, false, 1, UnitDay, 86400e3},
		{"interval floor normal", Cadence{Mode: CadenceInterval, IntervalMs: 1}, false, 500, UnitSecond, 2},
		{"interval floor high", Cadence{Mode: CadenceInterval, IntervalMs: 0.2}, true, 1000, UnitSecond, 1},
		{"interval rounds to ms", Cadence{Mode: CadenceInterval, IntervalMs: 33.4}, false, 30, UnitSecond, 33},
		{"bad mode -> rate", Cadence{Mode: "x", Rate: 4, Unit: UnitSecond}, false, 4, UnitSecond, 250},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeCadence(c.in, c.high)
			if math.Abs(got.Rate-c.wantRate) > 1e-9 || got.Unit != c.wantUnit || got.IntervalMs != c.wantIv {
				t.Fatalf("got %+v, want rate %v/%s interval %v", got, c.wantRate, c.wantUnit, c.wantIv)
			}
			if again := normalizeCadence(got, c.high); again != got {
				t.Fatalf("not idempotent: %+v -> %+v", got, again)
			}
		})
	}
}

func TestIntervalUsesExactRate(t *testing.T) {
	c := normalizeCadence(Cadence{Mode: CadenceRate, Rate: 3, Unit: UnitSecond}, false)
	if c.IntervalMs != 333 {
		t.Fatalf("displayed interval = %v", c.IntervalMs)
	}
	if iv := intervalMs(c); math.Abs(iv-1000.0/3) > 1e-9 {
		t.Fatalf("run interval = %v, want exact 333.33", iv)
	}
}

func TestNormalizeProfile(t *testing.T) {
	p := DefaultProfile()
	p.Input = "joystick"
	p.Button = "x9"
	p.Press = "tap"
	p.Key = " pageup "
	p.Uppercase = true
	p.DutyPercent = 150
	p.HoldGapMs = -5
	p.DoubleGapMs = 0
	p.Jitter.Percent = 200
	p.Limit = Limit{Kind: "both", Clicks: 0, Seconds: 0.2}
	p.Corners = Corners{Enabled: true, TL: -1, TR: 20000, BL: 5, BR: 0}
	p.Edges = Edges{Enabled: true, Top: -3, Right: 99999, Bottom: 7, Left: 0}
	p.ProcessList = ProcessList{Mode: "maybe", Entries: []ProcessEntry{
		{Exe: " Game ", Enabled: true}, {Exe: `C:\Games\GAME.EXE`}, {Exe: "  "}, {Exe: "tool.exe", Enabled: true},
	}}
	p.ClickPoints.Points = []ClickPoint{{ID: "a", Clicks: 0, Radius: -4}, {ID: "a", Clicks: 2_000_000, Radius: 20_000}}
	got := normalizeProfile(p, false)

	if got.Input != InputMouse || got.Button != ButtonLeft || got.Press != PressClick {
		t.Errorf("enums not fixed: %s %s %s", got.Input, got.Button, got.Press)
	}
	if got.Key != "PageUp" || got.Uppercase {
		t.Errorf("key = %q upper=%v, want PageUp without case", got.Key, got.Uppercase)
	}
	if got.DutyPercent != 100 || got.HoldGapMs != 0 || got.DoubleGapMs != 1 || got.Jitter.Percent != 100 {
		t.Errorf("clamps: duty %v gap %v dgap %v jitter %v", got.DutyPercent, got.HoldGapMs, got.DoubleGapMs, got.Jitter.Percent)
	}
	if got.Limit != (Limit{Kind: LimitNone, Clicks: 1, Seconds: 1}) {
		t.Errorf("limit = %+v", got.Limit)
	}
	if got.Corners != (Corners{Enabled: true, TL: 0, TR: 10000, BL: 5, BR: 0}) {
		t.Errorf("corners = %+v", got.Corners)
	}
	if got.Edges.Top != 0 || got.Edges.Right != 10000 || got.Edges.Bottom != 7 {
		t.Errorf("edges = %+v", got.Edges)
	}
	if got.ProcessList.Mode != ListBlock {
		t.Errorf("list mode = %s", got.ProcessList.Mode)
	}
	wantEntries := []ProcessEntry{{Exe: "game.exe", Enabled: true}, {Exe: "tool.exe", Enabled: true}}
	if !reflect.DeepEqual(got.ProcessList.Entries, wantEntries) {
		t.Errorf("entries = %+v", got.ProcessList.Entries)
	}
	pts := got.ClickPoints.Points
	if len(pts) != 2 || pts[0].ID != "a" || pts[1].ID == "a" || pts[1].ID == "" {
		t.Errorf("point ids = %+v", pts)
	}
	if pts[0].Clicks != 1 || pts[0].Radius != 0 || pts[1].Clicks != 999_999 || pts[1].Radius != 9_999 {
		t.Errorf("point clamps = %+v", pts)
	}
}

func TestNormalizeLetterKeyKeepsCase(t *testing.T) {
	p := DefaultProfile()
	p.Key, p.Uppercase = "a", true
	got := normalizeProfile(p, false)
	if got.Key != "A" || !got.Uppercase {
		t.Fatalf("got %q upper=%v", got.Key, got.Uppercase)
	}
	p.Key = "Tab" // not a hotkey name today: kept for Start to report
	if got := normalizeProfile(p, false); got.Key != "Tab" {
		t.Fatalf("unknown key = %q", got.Key)
	}
}

func TestDoubleClickRules(t *testing.T) {
	cases := []struct {
		name  string
		rate  float64
		press PressMode
		want  bool
	}{
		{"10 cps keeps it", 10, PressClick, true},
		{"11 cps turns it off", 11, PressClick, false},
		{"10.5 cps keeps it", 10.5, PressClick, true},
		{"hold mode turns it off", 1, PressHold, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := DefaultProfile()
			p.Cadence = Cadence{Mode: CadenceRate, Rate: c.rate, Unit: UnitSecond}
			p.Press = c.press
			p.DoubleClick = true
			if got := normalizeProfile(p, false).DoubleClick; got != c.want {
				t.Fatalf("DoubleClick = %v, want %v", got, c.want)
			}
		})
	}
	// Interval form counts too: 100 ms is 10/s.
	p := DefaultProfile()
	p.Cadence = Cadence{Mode: CadenceInterval, IntervalMs: 90}
	p.DoubleClick = true
	if normalizeProfile(p, false).DoubleClick {
		t.Fatal("90 ms interval (11.1/s) kept double click")
	}
}

func TestNormalizeSettings(t *testing.T) {
	s := DefaultSettings()
	s.Version = 7
	s.HotkeyMode = "tap"
	s.StartDelayMs = 99_999
	s.NewPointClicks = 0
	s.NewPointRadius = -1
	s.StopZones = []StopZone{
		{ID: "z", W: 0, H: -5, Action: "explode"},
		{ID: "z", X: 5, Y: 6, W: 10, H: 10, Action: ZonePause},
	}
	s.Presets = []Preset{
		{ID: "p1", Name: "  Farm  ", CreatedAt: "garbage"},
		{ID: "p2", Name: "farm"},
		{ID: "p1", Name: "   "},
		{Name: "This name is far too long to fit in forty characters"},
	}
	s.ActivePresetID = "gone"
	got := Normalize(s)

	if got.Version != 1 || got.HotkeyMode != HotkeyToggle || got.StartDelayMs != 10_000 {
		t.Errorf("top-level: %d %s %d", got.Version, got.HotkeyMode, got.StartDelayMs)
	}
	if got.NewPointClicks != 1 || got.NewPointRadius != 0 {
		t.Errorf("new point defaults: %d %d", got.NewPointClicks, got.NewPointRadius)
	}
	z := got.StopZones
	if len(z) != 2 || z[0].W != 1 || z[0].H != 1 || z[0].Action != ZoneStop || z[1].ID == "z" || z[1].Action != ZonePause {
		t.Errorf("zones = %+v", z)
	}
	ps := got.Presets
	if len(ps) != 3 {
		t.Fatalf("presets = %+v", ps)
	}
	if ps[0].Name != "Farm" || ps[1].Name != "farm (2)" || ps[1].ID != "p2" {
		t.Errorf("names = %q %q", ps[0].Name, ps[1].Name)
	}
	if n := len([]rune(ps[2].Name)); n != maxPresetName || ps[2].ID == "" {
		t.Errorf("long name kept %d chars, id %q", n, ps[2].ID)
	}
	if ps[0].CreatedAt == "garbage" || ps[0].UpdatedAt == "" {
		t.Errorf("timestamps not fixed: %+v", ps[0])
	}
	if got.ActivePresetID != "" {
		t.Errorf("dangling active preset kept: %q", got.ActivePresetID)
	}
	if again := Normalize(got); !reflect.DeepEqual(again, got) {
		t.Errorf("Normalize not idempotent:\n%+v\n%+v", got, again)
	}
}

func TestNormalizeDefaultsUnchanged(t *testing.T) {
	d := DefaultSettings()
	if got := Normalize(d); !reflect.DeepEqual(got, d) {
		t.Fatalf("defaults changed by Normalize:\n%+v\n%+v", d, got)
	}
}

func TestDutyCap(t *testing.T) {
	cases := []struct{ cps, want float64 }{
		{25, 1}, {49.9, 1}, {50, 0.98}, {99, 0.98}, {100, 0.70}, {199, 0.70},
		{200, 0.30}, {500, 0.30}, {501, 0.01}, {1000, 0.01},
	}
	for _, c := range cases {
		if got := dutyCap(c.cps); got != c.want {
			t.Errorf("dutyCap(%v) = %v, want %v", c.cps, got, c.want)
		}
	}
}

func TestBuildEngineConfig(t *testing.T) {
	d := deps{
		keyScan:         func(string) (uint16, bool, bool, error) { return 0x10, false, true, nil },
		capsLock:        func() bool { return false },
		doubleClickTime: nil,
	}
	p := normalizeProfile(DefaultProfile(), false)
	p.DutyPercent = 45
	cfg, err := buildEngineConfig(p, &d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseIntervalNs != 40e6 || cfg.Mode != 1 || cfg.DutyFraction != 0.45 || cfg.JitterSigma != 0 || cfg.Keyboard {
		t.Fatalf("mouse cfg = %+v", cfg)
	}

	p.Cadence = normalizeCadence(Cadence{Mode: CadenceRate, Rate: 300, Unit: UnitSecond}, false)
	p.Jitter = Jitter{Enabled: true, Percent: 35}
	p.Limit = Limit{Kind: LimitTime, Seconds: 2.5, Clicks: 10}
	cfg, _ = buildEngineConfig(p, &d, 1)
	if cfg.DutyFraction != 0.30 || cfg.JitterSigma != 0.35 || cfg.TimeLimitNs != 2_500_000_000 || cfg.ClickLimit != 0 {
		t.Fatalf("capped cfg = %+v", cfg)
	}

	p = normalizeProfile(DefaultProfile(), false)
	p.Input, p.Key, p.Uppercase, p.Press = InputKeyboard, "Q", true, PressHold
	cfg, _ = buildEngineConfig(p, &d, 1)
	if !cfg.Keyboard || cfg.Scan != 0x10 || !cfg.WantUpper || cfg.WantLower || cfg.CapsLock == nil {
		t.Fatalf("key cfg = %+v", cfg)
	}

	p = normalizeProfile(DefaultProfile(), false)
	p.Cadence = normalizeCadence(Cadence{Mode: CadenceRate, Rate: 2, Unit: UnitSecond}, false)
	p.DoubleClick, p.DoubleGapMs = true, 60
	d.doubleClickTime = nil
	cfg, _ = buildEngineConfig(p, &d, 1)
	if cfg.DoubleGapNs != 60e6 {
		t.Fatalf("double gap = %d", cfg.DoubleGapNs)
	}
}

func TestFreshInstallDisarmedUntilPageOpened(t *testing.T) {
	if DefaultSettings().Armed {
		t.Fatal("a fresh install must start disarmed")
	}
	s := newRig(t).s // the rig has opened the page once already
	if got := s.GetSettings(); !got.Armed || !got.Introduced {
		t.Fatalf("first PageOpened arms and introduces: %+v", got)
	}
	if _, err := s.SetArmed(false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.PageOpened(); got.Armed {
		t.Fatal("later page opens must not re-arm a clicker the user disarmed")
	}
	next := s.GetSettings()
	next.Introduced = false // a stale page payload
	if got, _ := s.SetSettings(next); !got.Introduced {
		t.Fatal("SetSettings must not clear Introduced")
	}
}
