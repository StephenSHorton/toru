package hotkey

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/StephenSHorton/toru/internal/inputtag"
)

// sim drives handleKey / handleMouse with crafted events, standing in for
// Windows: a fake GetAsyncKeyState that, like the real one, only sees events
// the hook let through (swallowed ones never reach it) and does see injected
// ones.
type sim struct {
	t     *testing.T
	m     *Manager
	async map[uint32]bool
	own   bool // the cursor is over a Toru window
}

func newSim(t *testing.T, bindings map[string]Binding) *sim {
	t.Helper()
	s := &sim{t: t, m: New(), async: map[uint32]bool{}}
	for id, b := range bindings {
		s.m.bindings[id] = b
		s.m.labels[id] = id
		s.m.order = append(s.m.order, id)
	}
	resetEngine()
	oldAsync, oldOwn := asyncKeyDown, overOwnWindow
	asyncKeyDown = func(vk uint32) bool { return s.async[vk] }
	overOwnWindow = func(x, y int32) bool { return s.own }
	kbdHookUp.Store(true)
	mouseHookUp.Store(true)
	actMu.Lock()
	oldActive := active
	active = s.m
	actMu.Unlock()
	t.Cleanup(func() {
		asyncKeyDown, overOwnWindow = oldAsync, oldOwn
		kbdHookUp.Store(false)
		mouseHookUp.Store(false)
		actMu.Lock()
		active = oldActive
		actMu.Unlock()
		resetEngine()
	})
	return s
}

// resetEngine clears every piece of package-level hook state.
func resetEngine() {
	for i := range phys.down {
		phys.down[i].Store(false)
		phys.swallowed[i].Store(false)
		phys.injected[i].Store(false)
	}
	phys.injecting.Store(false)
	for i := range mouseSwallowUp {
		mouseSwallowUp[i].Store(false)
	}
	actMu.Lock()
	chordHeld, heldVK, escHeld = false, 0, false
	if recTimer != nil {
		recTimer.Stop()
		recTimer = nil
	}
	recCh = nil
	clear(recSwallow)
	recMods, recBestMods = Binding{}, Binding{}
	recHeld, recBest = nil, nil
	recModPeak, recModsDown = 0, 0
	actMu.Unlock()
}

// idleManager returns a Manager with the given actions registered but the
// engine NOT started, so a unit test never installs a real system-wide hook.
func idleManager(actions map[string]Binding) *Manager {
	m := New()
	for id, b := range actions {
		m.bindings[id] = b
		m.callbacks[id] = func() {}
		m.labels[id] = id
		m.defaults[id] = actionMeta[id].Default
		m.order = append(m.order, id)
	}
	return m
}

// key sends a physical key event and reports whether it was swallowed.
func (s *sim) key(vk uint32, down bool) bool {
	return s.keyEv(keyEvent{vk: vk, down: down})
}

func (s *sim) keyEv(ev keyEvent) bool {
	sw := handleKey(ev)
	if !sw {
		s.async[sidedVK(ev.vk, ev.scan, ev.flags)] = ev.down
	}
	return sw
}

// inject sends a Toru-tagged key event (the auto clicker's SendInput).
func (s *sim) inject(vk uint32, down bool) {
	if handleKey(keyEvent{vk: vk, down: down, extra: inputtag.Toru}) {
		s.t.Fatalf("a tagged event (vk %#x) was swallowed", vk)
	}
	s.async[vk] = down
}

func (s *sim) mouse(vk uint32, down bool) bool {
	sw := handleMouse(mouseEvent{vk: vk, down: down})
	if !sw {
		s.async[vk] = down
	}
	return sw
}

// fired drains the dispatch channel.
func (s *sim) fired() []string {
	var out []string
	for {
		select {
		case a := <-s.m.sig:
			out = append(out, a)
		default:
			return out
		}
	}
}

func (s *sim) wantFired(want ...string) {
	s.t.Helper()
	got := s.fired()
	if len(got) != len(want) {
		s.t.Fatalf("fired %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			s.t.Fatalf("fired %v, want %v", got, want)
		}
	}
}

func TestPlainBindingUnchanged(t *testing.T) {
	s := newSim(t, map[string]Binding{"overlay": DefaultOverlay, "replaySave": DefaultReplaySave})
	s.key(vkLWin, true)
	s.key(vkLShift, true)
	if !s.key('S', true) {
		t.Fatal("Win+Shift+S down must be swallowed")
	}
	if !s.key('S', true) {
		t.Fatal("auto-repeat must be swallowed too")
	}
	s.wantFired("overlay") // once, not per repeat
	if s.key('S', false) {
		t.Fatal("the key-up is never swallowed")
	}
	s.key(vkLShift, false)
	s.key(vkLWin, false)

	// Exact: Ctrl+Alt+F10 is not Alt+F10.
	s.key(vkLControl, true)
	s.key(vkLMenu, true)
	if s.key(0x79, true) {
		t.Fatal("Ctrl+Alt+F10 must not match Alt+F10")
	}
	s.key(0x79, false)
	s.key(vkLControl, false)
	if !s.key(0x79, true) {
		t.Fatal("Alt+F10 should match")
	}
	s.wantFired("replaySave")
}

func TestTaggedEventsIgnored(t *testing.T) {
	s := newSim(t, map[string]Binding{"replaySave": DefaultReplaySave})
	s.inject(vkLMenu, true)
	s.inject(0x79, true)
	s.wantFired()
	if phys.down[vkLMenu].Load() || phys.down[0x79].Load() {
		t.Fatal("tagged events must not reach the physical table")
	}
	s.inject(0x79, false)
	s.inject(vkLMenu, false)

	// Nor a recording.
	actMu.Lock()
	ch := startRecordLocked()
	actMu.Unlock()
	s.inject('A', true)
	s.inject('A', false)
	select {
	case r := <-ch:
		t.Fatalf("a tagged key ended the recording: %+v", r)
	default:
	}
	endRecord()
}

func TestTableSidedMapping(t *testing.T) {
	s := newSim(t, nil)
	cases := []struct {
		ev   keyEvent
		want uint32
	}{
		{keyEvent{vk: vkShift, scan: 0x2A}, vkLShift},
		{keyEvent{vk: vkShift, scan: scanRShift}, vkRShift},
		{keyEvent{vk: vkControl}, vkLControl},
		{keyEvent{vk: vkControl, flags: llkhExtended}, vkRControl},
		{keyEvent{vk: vkMenu}, vkLMenu},
		{keyEvent{vk: vkMenu, flags: llkhExtended}, vkRMenu},
		{keyEvent{vk: vkRControl}, vkRControl},
		{keyEvent{vk: 'Q'}, 'Q'},
	}
	for _, c := range cases {
		ev := c.ev
		ev.down = true
		s.keyEv(ev)
		if !phys.down[c.want].Load() {
			t.Errorf("%+v should mark %#x down", c.ev, c.want)
		}
		ev.down = false
		s.keyEv(ev)
		if phys.down[c.want].Load() {
			t.Errorf("%+v up should clear %#x", c.ev, c.want)
		}
	}
	s.mouse(vkXButton1, true)
	if !phys.down[vkXButton1].Load() {
		t.Error("the mouse hook should mark Mouse4 down")
	}
}

func TestChordFiresOnceOnCompletion(t *testing.T) {
	chord := canonical(Binding{Ctrl: true, Key: 'S', Extra: [4]rune{'A'}})
	s := newSim(t, map[string]Binding{"chord": chord})
	s.key(vkLControl, true)
	if s.key('A', true) {
		t.Fatal("the first chord key must pass through")
	}
	s.wantFired()
	if !s.key('S', true) {
		t.Fatal("the completing key must be swallowed")
	}
	s.key('S', true) // auto-repeat
	s.wantFired("chord")
	s.key('S', false)
	s.key('A', false)
	s.key(vkLControl, false)

	// Either order completes it.
	s.key(vkLControl, true)
	s.key('S', true)
	if !s.key('A', true) {
		t.Fatal("A completes S+A")
	}
	s.wantFired("chord")
	s.key('A', false)
	s.key('S', false)
	s.key(vkLControl, false)

	// Without Ctrl it doesn't.
	s.key('A', true)
	if s.key('S', true) {
		t.Fatal("chord without its modifier must not fire")
	}
	s.wantFired()
}

func TestSidedMatching(t *testing.T) {
	b := Binding{Ctrl: true, Sides: SideLCtrl, Key: 'K'}
	s := newSim(t, map[string]Binding{"k": b})
	s.key(vkRControl, true)
	if s.key('K', true) {
		t.Fatal("RCtrl must not satisfy an LCtrl binding")
	}
	s.key('K', false)
	s.key(vkLControl, true)
	if s.key('K', true) {
		t.Fatal("exact: LCtrl + RCtrl held must not match LCtrl only")
	}
	s.key('K', false)
	s.key(vkRControl, false)
	if !s.key('K', true) {
		t.Fatal("LCtrl + K should match")
	}
	s.wantFired("k")
}

func TestModifierAsKey(t *testing.T) {
	s := newSim(t, map[string]Binding{"rctrl": {Key: namedKeyBase + vkRControl}})
	if s.key(vkRControl, true) {
		t.Fatal("a modifier used as the key must never be swallowed")
	}
	s.wantFired("rctrl")
	s.key(vkRControl, false)
	s.key(vkLShift, true)
	s.key(vkRControl, true)
	s.wantFired() // exact: Shift is an extra modifier
}

func TestLooseVsExact(t *testing.T) {
	s := newSim(t, map[string]Binding{
		"exact": {Key: FKey(9)},
		"loose": {Ctrl: true, Key: FKey(8), Loose: true},
	})
	s.key(vkLShift, true)
	if s.key(0x78, true) {
		t.Fatal("exact F9 must not fire with Shift held")
	}
	s.key(0x78, false)
	s.key(vkLControl, true)
	if !s.key(0x77, true) {
		t.Fatal("loose Ctrl+F8 should fire with Shift held too")
	}
	s.wantFired("loose")
	s.key(0x77, false)
	s.key(vkLControl, false)
	if s.key(0x77, true) {
		t.Fatal("loose still needs its own modifiers")
	}
}

// The auto clicker injecting Shift (uppercase auto-press) must not stop the
// user's exact Ctrl+Y from matching while a run is injecting, and an injected
// Shift up must not hide a Shift the user physically holds.
func TestHybridModifierCheck(t *testing.T) {
	s := newSim(t, map[string]Binding{"stop": {Ctrl: true, Key: 'Y'}, "shiftStop": {Ctrl: true, Shift: true, Key: 'U'}})

	// Not injecting: GetAsyncKeyState decides, as it always did.
	s.inject(vkLShift, true)
	s.key(vkLControl, true)
	if s.key('Y', true) {
		t.Fatal("not injecting: the async state (Shift down) applies")
	}
	s.key('Y', false)
	s.inject(vkLShift, false)

	SetInjecting(true)
	defer SetInjecting(false)
	s.inject(vkLShift, true)
	if !s.key('Y', true) {
		t.Fatal("injecting: an injected Shift must not count as held")
	}
	s.wantFired("stop")
	s.key('Y', false)
	s.inject(vkLShift, false)

	// The user holds Shift; the clicker's Shift down/up clears the async bit.
	s.key(vkLShift, true)
	s.inject(vkLShift, true)
	s.inject(vkLShift, false)
	if s.async[vkLShift] {
		t.Fatal("test setup: async Shift should read up")
	}
	if !s.key('U', true) {
		t.Fatal("injecting: the physically held Shift must still count")
	}
	s.wantFired("shiftStop")
	if !PhysicallyDown(vkShift) || !PhysicallyDown(vkLShift) {
		t.Fatal("PhysicallyDown(Shift) should follow the table while injecting")
	}
	s.key('U', false)
	s.key(vkLShift, false)
	if PhysicallyDown(vkShift) {
		t.Fatal("Shift released")
	}
}

func TestPhysicallyDownRules(t *testing.T) {
	s := newSim(t, map[string]Binding{"m4": {Key: namedKeyBase + vkXButton1}})

	// No hook: GetAsyncKeyState.
	kbdHookUp.Store(false)
	s.async['Q'] = true
	if !PhysicallyDown('Q') {
		t.Fatal("without the hook PhysicallyDown reads GetAsyncKeyState")
	}
	kbdHookUp.Store(true)
	delete(s.async, 'Q')

	// A swallowed down never reaches the async state: the table decides.
	if !s.mouse(vkXButton1, true) {
		t.Fatal("matched Mouse4 down should be swallowed")
	}
	if s.async[vkXButton1] {
		t.Fatal("test setup: swallowed down must not reach async")
	}
	if !PhysicallyDown(vkXButton1) {
		t.Fatal("a swallowed held button is still physically down")
	}
	if !s.mouse(vkXButton1, false) {
		t.Fatal("the up of a swallowed down is swallowed too")
	}
	if PhysicallyDown(vkXButton1) {
		t.Fatal("released")
	}

	// A release the hook never saw (elevated window focused): async wins.
	phys.note('H', true)
	s.async['H'] = false
	if PhysicallyDown('H') {
		t.Fatal("a stale table entry must not keep a key held")
	}

	if TriggerVK(Binding{Key: namedKeyBase + vkXButton1}) != vkXButton1 || TriggerVK(Binding{}) != 0 || TriggerVK(DefaultClickerToggle) != 'Y' {
		t.Fatal("TriggerVK")
	}
}

func TestMouseHotkey(t *testing.T) {
	s := newSim(t, map[string]Binding{"m4": {Key: namedKeyBase + vkXButton1}, "midShift": {Shift: true, Key: namedKeyBase + vkMButton}})
	if !s.mouse(vkXButton1, true) || !s.mouse(vkXButton1, false) {
		t.Fatal("matched Mouse4 down+up must be swallowed")
	}
	s.wantFired("m4")
	if s.mouse(vkMButton, true) || s.mouse(vkMButton, false) {
		t.Fatal("Middle without Shift doesn't match and must pass through")
	}
	s.wantFired()

	// Over Toru's own window the match is ignored and nothing is swallowed.
	s.own = true
	if s.mouse(vkXButton1, true) || s.mouse(vkXButton1, false) {
		t.Fatal("over a Toru window Mouse4 must pass through")
	}
	s.wantFired()
	s.own = false

	// Toru's own injected clicks are invisible.
	if handleMouse(mouseEvent{vk: vkXButton1, down: true, extra: inputtag.Toru}) {
		t.Fatal("tagged mouse event swallowed")
	}
	s.wantFired()
}

func TestConflicts(t *testing.T) {
	m := New()
	reg := func(id string, b Binding) {
		m.bindings[id] = b
		m.labels[id] = "label " + id
		m.order = append(m.order, id)
	}
	reg("replaySave", DefaultReplaySave)
	reg("toggle", DefaultClickerToggle)
	reg("lctrlK", Binding{Ctrl: true, Sides: SideLCtrl, Key: 'K'})
	reg("chord", canonical(Binding{Key: FKey(5), Extra: [4]rune{'A'}}))
	reg("loose", Binding{Key: FKey(7), Loose: true})
	reg("m5", Binding{Key: namedKeyBase + vkXButton2})

	cases := []struct {
		b    Binding
		want string
	}{
		{Binding{Alt: true, Key: FKey(10)}, "label replaySave"},
		{Binding{Key: FKey(10)}, ""},
		{Binding{Ctrl: true, Shift: true, Key: 'Y'}, "label toggle"},
		{Binding{Shift: true, Key: 'Y'}, ""},
		{Binding{Ctrl: true, Key: 'K'}, "label lctrlK"}, // generic Ctrl may be the left one
		{Binding{Ctrl: true, Sides: SideRCtrl, Key: 'K'}, ""},
		{Binding{Key: 'A'}, ""}, // one key can't complete a chord
		{canonical(Binding{Key: 'A', Extra: [4]rune{FKey(5)}}), "label chord"},
		{Binding{Shift: true, Key: FKey(7)}, "label loose"},
		{Binding{Key: namedKeyBase + vkXButton2}, "label m5"},
	}
	for _, c := range cases {
		got, ok := m.Conflicts(c.b)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("Conflicts(%s) = %q %v, want %q", comboLabel(c.b), got, ok, c.want)
		}
	}
	m.inactive["toggle"] = true
	if _, ok := m.Conflicts(DefaultClickerToggle); ok {
		t.Error("inactive actions don't conflict")
	}
	delete(m.inactive, "toggle")
	if _, ok := m.ConflictsExcept(DefaultClickerToggle, "toggle"); ok {
		t.Error("ConflictsExcept skips the given action")
	}
}

func TestValidateChords(t *testing.T) {
	ok := []Shortcut{
		{Key: "Mouse4"},
		{Key: "MouseMiddle"},
		{Key: "RCtrl"},
		{Key: "LCtrl", Extra: []string{"LShift"}},
		{Key: "A", Extra: []string{"F5"}},            // a non-typing key gates it
		{Ctrl: true, Key: "A", Extra: []string{"S"}}, // modifier
		{Key: "A", Sides: []string{"lctrl"}},         // a side implies Ctrl
		{Shift: true, Key: "Mouse4"},                 // mouse with a modifier
		{Ctrl: true, Key: "1", Extra: []string{"2", "3", "4", "5"}},
	}
	for _, sc := range ok {
		if err := validateShortcut(sc); err != nil {
			t.Errorf("%+v should be valid: %v", sc, err)
		}
	}
	bad := []Shortcut{
		{Key: "A", Extra: []string{"S"}},                                 // only typing keys
		{Ctrl: true, Key: "A", Extra: []string{"A"}},                     // duplicate
		{Ctrl: true, Key: "1", Extra: []string{"2", "3", "4", "5", "6"}}, // 6 keys
		{Ctrl: true, Key: "LCtrl"},                                       // held and a key
		{Ctrl: true, Key: "A", Extra: []string{"Tab"}},
		{Key: "A", Sides: []string{"lsuper"}},
		{Key: ""},
	}
	for _, sc := range bad {
		if err := validateShortcut(sc); err == nil {
			t.Errorf("%+v should be rejected", sc)
		}
	}
}

func TestShortcutWireRoundTrip(t *testing.T) {
	b := canonical(Binding{Ctrl: true, Shift: true, Sides: SideLCtrl, Key: 'S', Extra: [4]rune{'A'}, Loose: true})
	sc := bindingToShortcut("x", "X", b)
	if sc.Key != "A" || len(sc.Extra) != 1 || sc.Extra[0] != "S" || len(sc.Sides) != 1 || sc.Sides[0] != "lctrl" || !sc.Loose {
		t.Fatalf("wire = %+v", sc)
	}
	back, err := shortcutToBinding(sc)
	if err != nil || back != b {
		t.Fatalf("round trip = %+v %v, want %+v", back, err, b)
	}
	if plain := bindingToShortcut("o", "O", DefaultOverlay); plain.Sides == nil || plain.Extra == nil || len(plain.Sides)+len(plain.Extra) != 0 {
		t.Errorf("plain wire slices should be empty, non-nil: %+v", plain)
	}
	if got := comboLabel(b); got != "LCtrl + Shift + A + S" {
		t.Errorf("comboLabel = %q", got)
	}
	if got := comboLabel(Binding{Key: namedKeyBase + vkXButton1}); got != "Mouse4" {
		t.Errorf("comboLabel = %q", got)
	}
}

// isolateConfig points os.UserConfigDir at a temp dir.
func isolateConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	return filepath.Join(dir, "toru", "settings.json")
}

func TestPersistenceRoundTrip(t *testing.T) {
	path := isolateConfig(t)
	b := canonical(Binding{Alt: true, Sides: SideRAlt, Key: FKey(3), Extra: [4]rune{namedKeyBase + vkXButton1}, Loose: true})
	if err := persistBinding("clickerToggle", b); err != nil {
		t.Fatal(err)
	}
	if got := LoadBinding("clickerToggle", DefaultClickerToggle); got != b {
		t.Fatalf("loaded %+v, want %+v", got, b)
	}
	// Plain bindings are written exactly as before (no new keys).
	if err := persistBinding("overlay", DefaultOverlay); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	var st struct {
		Shortcuts map[string]map[string]any `json:"shortcuts"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatal(err)
	}
	if n := len(st.Shortcuts["overlay"]); n != 5 {
		t.Errorf("plain entry has %d fields, want 5: %v", n, st.Shortcuts["overlay"])
	}
}

func TestPersistenceLegacyAndCleared(t *testing.T) {
	path := isolateConfig(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"shortcuts": {
		"overlay": {"ctrl": false, "shift": true, "alt": false, "win": true, "key": "S"},
		"replaySave": {"ctrl": true, "shift": false, "alt": true, "win": false, "key": "F9"},
		"recordToggle": {"ctrl": true, "shift": false, "alt": false, "win": false, "key": ""}
	}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LoadBinding("overlay", Binding{}); got != DefaultOverlay {
		t.Errorf("legacy overlay = %+v", got)
	}
	if got := LoadBinding("replaySave", DefaultReplaySave); got != (Binding{Ctrl: true, Alt: true, Key: FKey(9)}) {
		t.Errorf("legacy replaySave = %+v", got)
	}
	if got := LoadBinding("recordToggle", DefaultRecordToggle); got != DefaultRecordToggle {
		t.Errorf("an empty legacy key still means the default, got %+v", got)
	}
	if got := LoadBinding("timelineMarker", DefaultTimelineMarker); got != DefaultTimelineMarker {
		t.Errorf("never set = default, got %+v", got)
	}

	// Cleared is distinct from never set.
	m := idleManager(map[string]Binding{"timelineMarker": DefaultTimelineMarker})
	svc := NewService(m)
	if err := svc.ClearShortcut("timelineMarker"); err != nil {
		t.Fatal(err)
	}
	if got := LoadBinding("timelineMarker", DefaultTimelineMarker); got != (Binding{}) {
		t.Errorf("cleared should load unbound, got %+v", got)
	}
	if b, _ := m.Binding("timelineMarker"); b != (Binding{}) {
		t.Errorf("cleared live binding = %+v", b)
	}
	if err := svc.ClearShortcut("nope"); err == nil {
		t.Error("unknown action")
	}
	if err := svc.ResetShortcut("timelineMarker"); err != nil {
		t.Fatal(err)
	}
	if got := LoadBinding("timelineMarker", Binding{}); got != DefaultTimelineMarker {
		t.Errorf("reset after clear = %+v", got)
	}
}

func TestSetShortcutRefusesOverlap(t *testing.T) {
	isolateConfig(t)
	m := idleManager(map[string]Binding{"replaySave": DefaultReplaySave, "clickerToggle": DefaultClickerToggle})
	svc := NewService(m)
	if err := svc.SetShortcut("clickerToggle", Shortcut{Alt: true, Key: "F10"}); err == nil {
		t.Error("an identical combo must be refused")
	}
	if err := svc.SetShortcut("clickerToggle", Shortcut{Key: "F10", Loose: true}); err == nil {
		t.Error("a loose F10 would fire on Alt+F10 and must be refused")
	}
	if err := svc.SetShortcut("clickerToggle", Shortcut{Ctrl: true, Alt: true, Key: "F10"}); err != nil {
		t.Errorf("exact Ctrl+Alt+F10 is distinct: %v", err)
	}
}

func TestRecordStartCallback(t *testing.T) {
	m := New()
	svc := NewService(m)
	called := false
	svc.SetOnRecordStart(func() { called = true })
	_, _ = svc.RecordShortcut() // no hook in tests: returns an error straight away
	if !called {
		t.Fatal("SetOnRecordStart callback should run when recording starts")
	}
}

func TestRecordChordAndModifiers(t *testing.T) {
	s := newSim(t, nil)
	record := func() chan recordResult {
		actMu.Lock()
		defer actMu.Unlock()
		return startRecordLocked()
	}

	// A chord: Ctrl + A + S, released.
	ch := record()
	for _, e := range []struct {
		vk   uint32
		down bool
	}{{vkLControl, true}, {'A', true}, {'S', true}, {'A', true}, {'S', false}, {'A', false}, {vkLControl, false}} {
		if !s.key(e.vk, e.down) {
			t.Fatalf("vk %#x down=%v should be swallowed while recording", e.vk, e.down)
		}
	}
	if r := <-ch; !r.ok || r.b != canonical(Binding{Ctrl: true, Key: 'A', Extra: [4]rune{'S'}}) {
		t.Fatalf("chord recorded %+v", r)
	}

	// Modifier only: RCtrl tapped.
	ch = record()
	s.key(vkRControl, true)
	s.key(vkRControl, false)
	if r := <-ch; !r.ok || r.b != (Binding{Key: namedKeyBase + vkRControl}) {
		t.Fatalf("modifier-only recorded %+v", r)
	}

	// A mouse button with a modifier.
	ch = record()
	s.key(vkLShift, true)
	if !s.mouse(vkXButton1, true) || !s.mouse(vkXButton1, false) {
		t.Fatal("recorded mouse down+up must be swallowed")
	}
	s.key(vkLShift, false)
	if r := <-ch; !r.ok || r.b != (Binding{Shift: true, Key: namedKeyBase + vkXButton1}) {
		t.Fatalf("mouse recorded %+v", r)
	}

	// Held past the gather window: ends without a release.
	ch = record()
	s.key(vkLMenu, true)
	s.key('Z', true)
	select {
	case r := <-ch:
		if !r.ok || r.b != (Binding{Alt: true, Key: 'Z'}) {
			t.Fatalf("held chord recorded %+v", r)
		}
	case <-time.After(chordGather + time.Second):
		t.Fatal("the gather window should end the recording")
	}
	if !s.key('Z', false) || !s.key(vkLMenu, false) {
		t.Fatal("ups of keys swallowed during the recording are swallowed after it")
	}

	// The fifth key ends it at once.
	ch = record()
	s.key(vkLControl, true)
	for _, k := range []uint32{'1', '2', '3', '4', '5'} {
		s.key(k, true)
	}
	select {
	case r := <-ch:
		if !r.ok || len(chordKeys(r.b)) != 5 {
			t.Fatalf("5-key chord recorded %+v", r)
		}
	default:
		t.Fatal("the fifth key should end the recording")
	}
}
