package clicker

import (
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/StephenSHorton/toru/internal/clicker/safety"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// ClickerService is the Wails-bound auto clicker API used by the hub's Auto
// Clicker page, the tray and the clicker hotkeys.
//
// JS binding name: ClickerService.*
type ClickerService struct {
	mu       sync.Mutex
	app      *application.App
	keys     bindingSource
	onChange func()
	emit     func(name string, data any) // nil until SetApp (tests install a recorder)
	d        deps

	settings Settings
	stats    Stats

	// run controller (controller.go)
	cur          *run   // the countdown or run in progress, nil when idle
	gen          uint64 // bumped per run; a stale run's late result is dropped
	state        RunState
	countdownEnd time.Time
	last         Status // the last run's totals and stop reason
	lastEmit     time.Time
	lastPill     string
	pubMu        sync.Mutex // serializes publish (controller.go); taken before mu
	injectRefs   int
	shiftRefs    int

	// pickers (picks.go)
	picking    bool
	pickCancel func()

	// start-zone monitor (monitor.go)
	monStop chan struct{}

	// closed is set by Shutdown so a late hotkey or page call cannot start a
	// run after the final stop.
	closed bool

	// runs counts live run goroutines, detached ones included, so Shutdown
	// can give them a bounded moment to release held input.
	runs sync.WaitGroup
}

// NewService returns an idle service holding the default settings; Init loads
// the persisted ones.
func NewService() *ClickerService {
	return newService(realDeps())
}

func newService(d deps) *ClickerService {
	return &ClickerService{d: d, settings: Normalize(DefaultSettings()), state: StateIdle}
}

// SetApp injects the running app (event emits, dialogs).
//
//wails:ignore
func (s *ClickerService) SetApp(app *application.App) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.app = app
	if app == nil {
		return
	}
	s.emit = func(name string, data any) { app.Event.Emit(name, data) }
	s.d.saveDialog = func(_, suggested string) (string, error) {
		dlg := app.Dialog.SaveFile().SetFilename(suggested).
			AddFilter("Auto clicker preset", "*.json").
			AddFilter("All Files", "*.*")
		if win := app.Window.Current(); win != nil {
			dlg = dlg.AttachToWindow(win)
		}
		path, err := dlg.PromptForSingleSelection()
		if err != nil {
			return "", nil // cancel is not an error for the page
		}
		return path, nil
	}
	s.d.openDialog = func(title string) (string, error) {
		dlg := app.Dialog.OpenFile().
			CanChooseFiles(true).
			CanChooseDirectories(false).
			SetTitle(title).
			AddFilter("Auto clicker preset", "*.json").
			AddFilter("All Files", "*.*")
		if win := app.Window.Current(); win != nil {
			dlg = dlg.AttachToWindow(win)
		}
		path, err := dlg.PromptForSingleSelection()
		if err != nil {
			return "", nil
		}
		return path, nil
	}
}

// SetHotkeys gives the service the shared hotkey Manager (conflict checks, the
// toggle label, arming the clicker hotkeys).
//
//wails:ignore
func (s *ClickerService) SetHotkeys(m *hotkey.Manager) {
	s.mu.Lock()
	if m == nil {
		s.keys = nil
	} else {
		s.keys = m
	}
	s.mu.Unlock()
}

// SetOnChange registers a callback for run-state and arm changes (tray rebuild).
//
//wails:ignore
func (s *ClickerService) SetOnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// Init loads settings and arms the hotkeys; called once from ApplicationStarted.
//
//wails:ignore
func (s *ClickerService) Init() {
	set := loadSettings()
	stats := loadStats()
	s.mu.Lock()
	s.settings = set
	s.stats = stats
	st := s.statusLocked()
	emit := s.emit
	s.mu.Unlock()
	s.applyArmed(set.Armed)
	s.syncMonitor()
	if emit != nil {
		emit(EventSettings, set)
		emit(EventStatus, st)
	}
	s.changed()
}

// Shutdown stops any run, releases held input and closes overlays.
//
//wails:ignore
func (s *ClickerService) Shutdown() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stopMonitor()
	s.CancelPick()
	s.stopRun(0, StopShutdown)
	waited := make(chan struct{})
	go func() { s.runs.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(shutdownWait):
	}
	if s.d.indicatorHide != nil {
		s.d.indicatorHide()
	}
	if s.d.overlayShutdown != nil {
		s.d.overlayShutdown()
	}
}

// HotkeyToggle is the clickerToggle hotkey callback (press edge). In toggle
// mode a press starts or stops; in hold mode a press starts and the guard stops
// the run once the key is released. Ignored while picking.
//
//wails:ignore
func (s *ClickerService) HotkeyToggle() {
	s.mu.Lock()
	active, picking, armed, mode := s.cur != nil, s.picking, s.settings.Armed, s.settings.HotkeyMode
	// A hold-mode run whose trigger key the guard watches is stopped by the
	// release; more press edges (key auto-repeat while held) must not stop it.
	guardOwnsStop := active && len(s.cur.safe.HoldTriggerVKs) > 0
	keys := s.keys
	s.mu.Unlock()
	if picking || !armed {
		return
	}
	if active {
		if !guardOwnsStop {
			s.stopRun(0, StopHotkey)
		}
		return
	}
	var holdVKs []uint16
	if mode == HotkeyHold && keys != nil && s.d.triggerVKs != nil {
		if b, ok := keys.Binding(actionToggle); ok {
			holdVKs = s.d.triggerVKs(b)
		}
	}
	if _, err := s.begin(srcHotkey, holdVKs, -1); err != nil {
		s.notifyStartError(err)
	}
}

// HotkeyArm is the clickerArm hotkey callback.
//
//wails:ignore
func (s *ClickerService) HotkeyArm() {
	s.mu.Lock()
	armed := s.settings.Armed
	s.mu.Unlock()
	if _, err := s.SetArmed(!armed); err != nil {
		log.Printf("clicker: arm hotkey: %v", err)
	}
}

// BindingsChanged republishes the status (its Hotkey label and the running
// pill) and rebuilds the tray after a shortcut was rebound.
//
//wails:ignore
func (s *ClickerService) BindingsChanged() {
	s.publish(true)
	s.changed()
}

// StopFor stops a run (or countdown) for a host reason such as the capture
// overlay opening or a shortcut being recorded. No-op when idle.
//
//wails:ignore
func (s *ClickerService) StopFor(code StopCode) {
	s.stopRun(0, code)
}

// TrayToggle starts or stops a run from the tray (no countdown).
//
//wails:ignore
func (s *ClickerService) TrayToggle() {
	s.mu.Lock()
	active := s.cur != nil
	s.mu.Unlock()
	if active {
		s.stopRun(0, StopTray)
		return
	}
	if _, err := s.begin(srcTray, nil, -1); err != nil {
		s.notifyStartError(err)
	}
}

// notifyStartError shows why a hotkey or tray start failed: there is no page
// to show it on.
func (s *ClickerService) notifyStartError(err error) {
	log.Printf("clicker: start: %v", err)
	if s.d.toast != nil {
		s.d.toast("Auto clicker didn't start", userMessage(err), false)
	}
}

// userMessage strips the "clicker: CODE: " prefix.
func userMessage(err error) string {
	msg := err.Error()
	if rest, ok := strings.CutPrefix(msg, "clicker: "); ok {
		if i := strings.Index(rest, ": "); i >= 0 {
			return rest[i+2:]
		}
	}
	return msg
}

// GetSettings returns the current settings.
func (s *ClickerService) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings normalizes, saves and applies next, returning what was stored.
// Presets and the active preset are kept as stored (they change only through
// the preset methods, which honour the running lock). When the failsafe
// geometry or the click points changed, they flash on screen.
func (s *ClickerService) SetSettings(next Settings) (Settings, error) {
	s.mu.Lock()
	prev := s.settings
	next.Presets = prev.Presets
	next.ActivePresetID = prev.ActivePresetID
	next = Normalize(next)
	s.settings = next
	s.mu.Unlock()

	err := saveSettings(next)
	if prev.Armed != next.Armed {
		if !next.Armed {
			s.stopRun(0, StopDisarmed)
		}
		s.applyArmed(next.Armed)
		s.publish(true)
		s.changed()
	}
	s.syncMonitor()
	s.previewChanges(prev, next)
	if err != nil {
		return next, clickerErrf(ErrWinAPI, "Couldn't save the clicker settings: %v", err)
	}
	return next, nil
}

// previewChanges flashes the failsafe areas and click points that changed.
func (s *ClickerService) previewChanges(prev, next Settings) {
	if !next.ShowSafetyOverlay {
		return
	}
	if safetyGeometryChanged(prev, next) && s.d.showSafety != nil {
		s.d.showSafety(safetyScene(next, s.monitors()), false)
	}
	cp := next.Profile.ClickPoints
	if cp.Enabled && len(cp.Points) > 0 && pointsChanged(prev.Profile.ClickPoints, cp) && s.d.showPoints != nil {
		s.d.showPoints(scenePoints(cp.Points), false)
	}
}

// PreviewSafety flashes the failsafe areas on screen for a few seconds (even
// with ShowSafetyOverlay off: the user asked for it).
func (s *ClickerService) PreviewSafety() {
	s.mu.Lock()
	set := s.settings
	s.mu.Unlock()
	if s.d.showSafety != nil {
		s.d.showSafety(safetyScene(set, s.monitors()), false)
	}
}

// PointWarnings returns the indexes of click points that sit inside an active
// corner or edge failsafe on the current displays: a run would stop as soon
// as the cursor reached them. Empty when none do (or the displays are unknown).
func (s *ClickerService) PointWarnings() []int {
	s.mu.Lock()
	set := s.settings
	s.mu.Unlock()
	out := []int{}
	safe := buildSafetyConfig(set, nil, -1)
	if !safe.Corners.Enabled && !safe.Edges.Enabled {
		return out
	}
	mons := s.monitors()
	for i, pt := range set.Profile.ClickPoints.Points {
		if safety.PointInFailsafe(safe.Corners, safe.Edges, mons, pt.X, pt.Y) {
			out = append(out, i)
		}
	}
	return out
}

// monitors returns the display rects (nil when unknown).
func (s *ClickerService) monitors() []safety.Rect {
	if s.d.newSystem == nil {
		return nil
	}
	sysq := s.d.newSystem()
	if sysq == nil {
		return nil
	}
	return sysq.Monitors()
}

// ResetSettings restores defaults, keeping presets (none is active after).
func (s *ClickerService) ResetSettings() (Settings, error) {
	s.mu.Lock()
	def := DefaultSettings()
	def.Presets = s.settings.Presets
	s.settings.ActivePresetID = "" // SetSettings keeps the stored one
	s.mu.Unlock()
	return s.SetSettings(def)
}

// commitFromGo stores settings changed by Go (preset ops, pickers, arming),
// saves them and emits clicker:settings.
func (s *ClickerService) commitFromGo(next Settings) error {
	s.mu.Lock()
	s.settings = next
	emit := s.emit
	s.mu.Unlock()
	err := saveSettings(next)
	if emit != nil {
		emit(EventSettings, next)
	}
	if err != nil {
		return clickerErrf(ErrWinAPI, "Couldn't save the clicker settings: %v", err)
	}
	return nil
}

// Status returns the live run state.
func (s *ClickerService) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

// Start starts a run from the page, after StartDelayMs of countdown (Esc or
// Stop cancels it), so the run does not click the Start button itself.
func (s *ClickerService) Start() (Status, error) {
	return s.begin(srcPage, nil, -1)
}

// Stop stops a run or cancels a countdown.
func (s *ClickerService) Stop() Status {
	return s.stopAndWait(StopUser, shutdownWait)
}

// Toggle starts (without countdown) or stops a run.
func (s *ClickerService) Toggle() (Status, error) {
	s.mu.Lock()
	active := s.cur != nil
	s.mu.Unlock()
	if active {
		return s.stopAndWait(StopUser, shutdownWait), nil
	}
	return s.begin(srcToggle, nil, -1)
}

// SetPaused pauses or resumes a running clicker.
func (s *ClickerService) SetPaused(paused bool) Status {
	return s.setPaused(paused)
}

// SetArmed arms or disarms the clicker hotkeys; disarming stops a run.
func (s *ClickerService) SetArmed(armed bool) (Settings, error) {
	s.mu.Lock()
	changed := s.settings.Armed != armed
	// Store the flag before stopping, so a start racing the disarm sees it
	// (begin rechecks Armed under the lock) instead of slipping in between.
	s.settings.Armed = armed
	next := s.settings
	s.mu.Unlock()
	if !changed {
		return next, nil
	}
	if !armed {
		s.stopRun(0, StopDisarmed)
	}
	err := s.commitFromGo(next)
	s.applyArmed(armed)
	s.syncMonitor()
	s.publish(true)
	s.changed()
	return next, err
}

// applyArmed turns the toggle combo's interception on or off. clickerArm
// always stays active so the user can re-arm.
func (s *ClickerService) applyArmed(armed bool) {
	s.mu.Lock()
	keys := s.keys
	s.mu.Unlock()
	if keys == nil {
		return
	}
	keys.SetActive(actionToggle, armed)
	keys.SetActive(actionArm, true)
}

// CursorPosition returns the cursor in physical virtual-desktop px.
func (s *ClickerService) CursorPosition() (Point, error) {
	x, y, ok := s.d.cursorPos()
	if !ok {
		return Point{}, clickerErr(ErrWinAPI, "Couldn't read the cursor position")
	}
	return Point{X: x, Y: y}, nil
}

// ListProcesses returns the apps with visible windows plus saved list entries
// (Running=false when they are not running now). Running apps come first in
// the order Windows lists them; saved-only entries follow, sorted.
func (s *ClickerService) ListProcesses() ([]ProcessInfo, error) {
	live, err := s.d.listProcesses()
	if err != nil {
		return nil, clickerErrf(ErrWinAPI, "Couldn't list running apps: %v", err)
	}
	s.mu.Lock()
	saved := s.settings.Profile.ProcessList.Entries
	s.mu.Unlock()
	out := make([]ProcessInfo, 0, len(live)+len(saved))
	seen := map[string]bool{}
	for _, p := range live {
		exe := normalizeExe(p.Exe)
		if exe == "" || seen[exe] {
			continue
		}
		seen[exe] = true
		out = append(out, ProcessInfo{Exe: exe, Title: clipTitle(p.Title), IconURL: p.IconURL, Running: true})
	}
	var extra []ProcessInfo
	for _, e := range saved {
		if !seen[e.Exe] {
			seen[e.Exe] = true
			extra = append(extra, ProcessInfo{Exe: e.Exe})
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Exe < extra[j].Exe })
	return append(out, extra...), nil
}

// clipTitle cuts a window title to 35 characters.
func clipTitle(t string) string {
	r := []rune(strings.TrimSpace(t))
	if len(r) > 35 {
		return string(r[:34]) + "…"
	}
	return string(r)
}

// presetOp runs a preset mutation under the running lock and commits it.
func (s *ClickerService) presetOp(op func(Settings) (Settings, error)) (Settings, error) {
	s.mu.Lock()
	cur := s.settings
	locked := s.cur != nil
	s.mu.Unlock()
	if locked {
		return cur, clickerErr(ErrPresetLocked, "Presets can't be changed while the clicker is running")
	}
	next, err := op(cur)
	if err != nil {
		return cur, err
	}
	err = s.commitFromGo(next)
	s.syncMonitor()
	s.previewChanges(cur, next)
	return next, err
}

// SavePreset stores the current profile as a new preset and makes it active.
func (s *ClickerService) SavePreset(name string) (Settings, error) {
	return s.presetOp(func(c Settings) (Settings, error) { return savePreset(c, name, s.d.now()) })
}

// ApplyPreset loads a preset's profile.
func (s *ClickerService) ApplyPreset(id string) (Settings, error) {
	return s.presetOp(func(c Settings) (Settings, error) { return applyPreset(c, id) })
}

// UpdatePreset overwrites a preset with the current profile.
func (s *ClickerService) UpdatePreset(id string) (Settings, error) {
	return s.presetOp(func(c Settings) (Settings, error) { return updatePreset(c, id, s.d.now()) })
}

// RenamePreset renames a preset.
func (s *ClickerService) RenamePreset(id, name string) (Settings, error) {
	return s.presetOp(func(c Settings) (Settings, error) { return renamePreset(c, id, name, s.d.now()) })
}

// DuplicatePreset copies a preset as "Name (2)", "Name (3)", ...
func (s *ClickerService) DuplicatePreset(id string) (Settings, error) {
	return s.presetOp(func(c Settings) (Settings, error) { return duplicatePreset(c, id, s.d.now()) })
}

// DeletePreset removes a preset.
func (s *ClickerService) DeletePreset(id string) (Settings, error) {
	return s.presetOp(func(c Settings) (Settings, error) { return deletePreset(c, id) })
}

// ExportPreset writes a preset file chosen in a save dialog; "" when cancelled.
// Allowed while running (it changes nothing).
func (s *ClickerService) ExportPreset(id string) (string, error) {
	s.mu.Lock()
	i := presetIndex(s.settings.Presets, id)
	var p Preset
	if i >= 0 {
		p = s.settings.Presets[i]
	}
	s.mu.Unlock()
	if i < 0 {
		return "", clickerErr(ErrPresetNotFound, "That preset no longer exists")
	}
	data, err := exportPresetJSON(p)
	if err != nil {
		return "", clickerErrf(ErrWinAPI, "Couldn't write the preset: %v", err)
	}
	path, err := s.d.saveDialog("Export preset", exportFileName(p.Name))
	if err != nil || path == "" {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", clickerErrf(ErrWinAPI, "Couldn't write the preset: %v", err)
	}
	return path, nil
}

// ImportPreset reads a Toru or Blur AutoClicker preset file chosen in an open
// dialog and adds it as a preset (not made active). A cancelled dialog returns
// the settings unchanged.
func (s *ClickerService) ImportPreset() (Settings, error) {
	s.mu.Lock()
	cur, locked := s.settings, s.cur != nil
	s.mu.Unlock()
	if locked {
		return cur, clickerErr(ErrPresetLocked, "Presets can't be changed while the clicker is running")
	}
	path, err := s.d.openDialog("Import preset")
	if err != nil || path == "" {
		return cur, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cur, clickerErrf(ErrImportFormat, "Couldn't read that file: %v", err)
	}
	return s.presetOp(func(c Settings) (Settings, error) {
		next, _, err := importPresetJSON(c, data, s.d.now())
		return next, err
	})
}

// Stats returns lifetime totals.
func (s *ClickerService) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// ResetStats zeroes the lifetime totals.
func (s *ClickerService) ResetStats() Stats {
	s.mu.Lock()
	s.stats = Stats{}
	s.mu.Unlock()
	if err := saveStats(Stats{}); err != nil {
		log.Printf("clicker: save stats: %v", err)
	}
	return Stats{}
}
