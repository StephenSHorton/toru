package clicker

import (
	"fmt"
	"sync"

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
	keys     *hotkey.Manager
	onChange func()
	settings Settings
}

// NewService returns an idle service holding the persisted settings.
func NewService() *ClickerService {
	return &ClickerService{settings: Normalize(DefaultSettings())}
}

func notImplemented(what string) error {
	return fmt.Errorf("clicker: %s: %s is not built yet", ErrNotImplemented, what)
}

// SetApp injects the running app (event emits, dialogs).
//
//wails:ignore
func (s *ClickerService) SetApp(app *application.App) {
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

// SetHotkeys gives the service the shared hotkey Manager (conflict checks, the
// toggle label, arming the clicker hotkeys).
//
//wails:ignore
func (s *ClickerService) SetHotkeys(m *hotkey.Manager) {
	s.mu.Lock()
	s.keys = m
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
func (s *ClickerService) Init() {}

// Shutdown stops any run, releases held input and closes overlays.
//
//wails:ignore
func (s *ClickerService) Shutdown() {}

// HotkeyToggle is the clickerToggle hotkey callback (press edge).
//
//wails:ignore
func (s *ClickerService) HotkeyToggle() {}

// HotkeyArm is the clickerArm hotkey callback.
//
//wails:ignore
func (s *ClickerService) HotkeyArm() {}

// StopFor stops a run (or countdown) for a host reason such as the capture
// overlay opening or a shortcut being recorded. No-op when idle.
//
//wails:ignore
func (s *ClickerService) StopFor(code StopCode) {}

// TrayToggle starts or stops a run from the tray (no countdown).
//
//wails:ignore
func (s *ClickerService) TrayToggle() {}

// GetSettings returns the current settings.
func (s *ClickerService) GetSettings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetSettings normalizes, saves and applies next, returning what was stored.
func (s *ClickerService) SetSettings(next Settings) (Settings, error) {
	return Settings{}, notImplemented("SetSettings")
}

// ResetSettings restores defaults, keeping presets.
func (s *ClickerService) ResetSettings() (Settings, error) {
	return Settings{}, notImplemented("ResetSettings")
}

// Status returns the live run state.
func (s *ClickerService) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Status{State: StateIdle, ActivePoint: -1, Armed: s.settings.Armed}
}

// Start starts a run from the page, after StartDelayMs of countdown.
func (s *ClickerService) Start() (Status, error) {
	return Status{}, notImplemented("Start")
}

// Stop stops a run or cancels a countdown.
func (s *ClickerService) Stop() Status {
	return s.Status()
}

// Toggle starts (without countdown) or stops a run.
func (s *ClickerService) Toggle() (Status, error) {
	return Status{}, notImplemented("Toggle")
}

// SetPaused pauses or resumes a running clicker.
func (s *ClickerService) SetPaused(paused bool) Status {
	return s.Status()
}

// SetArmed arms or disarms the clicker hotkeys; disarming stops a run.
func (s *ClickerService) SetArmed(armed bool) (Settings, error) {
	return Settings{}, notImplemented("SetArmed")
}

// StartPointPick starts picking click points on screen. Results arrive as
// clicker:settings and clicker:pick events.
func (s *ClickerService) StartPointPick() error {
	return notImplemented("StartPointPick")
}

// StartZonePick starts drawing a zone; zoneID "" adds a new one.
func (s *ClickerService) StartZonePick(zoneID string) error {
	return notImplemented("StartZonePick")
}

// CancelPick ends an active pick without changes.
func (s *ClickerService) CancelPick() {}

// PreviewSafety flashes the failsafe areas on screen for a few seconds.
func (s *ClickerService) PreviewSafety() {}

// CursorPosition returns the cursor in physical virtual-desktop px.
func (s *ClickerService) CursorPosition() (Point, error) {
	return Point{}, notImplemented("CursorPosition")
}

// ListProcesses returns the apps with visible windows plus saved list entries.
func (s *ClickerService) ListProcesses() ([]ProcessInfo, error) {
	return nil, notImplemented("ListProcesses")
}

// SavePreset stores the current profile as a new preset.
func (s *ClickerService) SavePreset(name string) (Settings, error) {
	return Settings{}, notImplemented("SavePreset")
}

// ApplyPreset loads a preset's profile.
func (s *ClickerService) ApplyPreset(id string) (Settings, error) {
	return Settings{}, notImplemented("ApplyPreset")
}

// UpdatePreset overwrites a preset with the current profile.
func (s *ClickerService) UpdatePreset(id string) (Settings, error) {
	return Settings{}, notImplemented("UpdatePreset")
}

// RenamePreset renames a preset.
func (s *ClickerService) RenamePreset(id, name string) (Settings, error) {
	return Settings{}, notImplemented("RenamePreset")
}

// DuplicatePreset copies a preset under a free "(copy)" name.
func (s *ClickerService) DuplicatePreset(id string) (Settings, error) {
	return Settings{}, notImplemented("DuplicatePreset")
}

// DeletePreset removes a preset.
func (s *ClickerService) DeletePreset(id string) (Settings, error) {
	return Settings{}, notImplemented("DeletePreset")
}

// ExportPreset writes a preset file chosen in a save dialog; "" when cancelled.
func (s *ClickerService) ExportPreset(id string) (string, error) {
	return "", notImplemented("ExportPreset")
}

// ImportPreset reads a Toru or Blur AutoClicker preset file chosen in an open
// dialog and adds it as a preset.
func (s *ClickerService) ImportPreset() (Settings, error) {
	return Settings{}, notImplemented("ImportPreset")
}

// Stats returns lifetime totals.
func (s *ClickerService) Stats() Stats {
	return Stats{}
}

// ResetStats zeroes the lifetime totals.
func (s *ClickerService) ResetStats() Stats {
	return Stats{}
}
