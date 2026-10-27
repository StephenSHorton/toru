package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/StephenSHorton/toru/internal/toast"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// startupArg is the command-line flag Toru passes to ITSELF when it is registered
// to launch at Windows login (see SettingsService.SetLaunchAtLogin). On a login
// launch main.go sees this flag and starts SILENT in the tray: it installs the
// systray + prewarms the overlay windows but does NOT open the Settings/home
// window, so Toru "auto-minimises to the tray" on boot instead of flashing a
// window in the user's face every time they sign in.
const startupArg = "--startup"

// autostartID is the registry value name written under
// HKCU\Software\Microsoft\Windows\CurrentVersion\Run. Pinning it (instead of
// letting Wails slugify Options.Name) keeps a single, stable entry even if the
// app's display name ever changes, and matches what disable() looks for.
const autostartID = "Toru"

// SettingsService is the Wails-bound API for app-level preferences that aren't
// keyboard shortcuts: "launch at Windows login", backed directly by Wails'
// AutostartManager (on Windows the registry Run key IS the source of truth, so
// nothing is persisted for it), and the notification settings, persisted to
// %AppData%\toru\notifications.json.
//
// JS binding name: SettingsService.*
type SettingsService struct {
	// app is injected after application.New (mirrors WindowsService) because the
	// App doesn't exist yet when the service list is built.
	app *application.App
}

// GetLaunchAtLogin reports whether Toru is currently registered to start at user
// login. It reads the live registry state via Wails' AutostartManager, so it
// reflects reality even if the entry was added/removed outside the app.
func (s *SettingsService) GetLaunchAtLogin() (bool, error) {
	if s.app == nil {
		return false, fmt.Errorf("settings: app not ready")
	}
	return s.app.Autostart.IsEnabled()
}

// SetLaunchAtLogin enables or disables launch-at-login. When enabling, Toru is
// registered WITH the --startup argument so the login launch starts silently in
// the tray (no Settings window — see startupArg). Disabling removes the registry
// entry. Re-enabling is safe: Wails overwrites the existing value.
func (s *SettingsService) SetLaunchAtLogin(enabled bool) error {
	if s.app == nil {
		return fmt.Errorf("settings: app not ready")
	}
	if !enabled {
		return s.app.Autostart.Disable()
	}
	return s.app.Autostart.EnableWithOptions(application.AutostartOptions{
		Identifier: autostartID,
		Arguments:  []string{startupArg},
	})
}

// notifyMu serializes notifications.json load-modify-save.
var notifyMu sync.Mutex

func notificationsPath() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "toru")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(dir, "notifications.json"), nil
}

// loadNotifications returns the persisted notification settings, or the
// defaults when the file is missing or unreadable. Missing fields keep their
// default values; toast.SetOptions fixes out-of-range ones.
func loadNotifications() toast.Options {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	o := toast.DefaultOptions()
	path, err := notificationsPath()
	if err != nil {
		return o
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return o
	}
	// Tolerate a UTF-8 BOM (Notepad, PowerShell 5's Set-Content).
	raw = bytes.TrimPrefix(raw, []byte("\uFEFF"))
	if json.Unmarshal(raw, &o) != nil {
		return toast.DefaultOptions()
	}
	return o
}

// saveNotifications writes o atomically (tmp + rename).
func saveNotifications(o toast.Options) error {
	notifyMu.Lock()
	defer notifyMu.Unlock()
	path, err := notificationsPath()
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(o, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "notifications-*.json.tmp")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// GetNotifications returns the notification settings in effect.
func (s *SettingsService) GetNotifications() toast.Options {
	return toast.CurrentOptions()
}

// SetNotifications applies and persists the notification settings, returning
// them as applied (out-of-range values fixed). They apply even when saving
// fails; the error says they won't survive a restart.
func (s *SettingsService) SetNotifications(o toast.Options) (toast.Options, error) {
	toast.SetOptions(o)
	applied := toast.CurrentOptions()
	if err := saveNotifications(applied); err != nil {
		return applied, fmt.Errorf("save notification settings: %w", err)
	}
	return applied, nil
}

// TestNotification posts a sample card with its sound, so the user sees where
// cards land and hears the volume. Repeated presses replace it.
func (s *SettingsService) TestNotification() {
	toast.Post(toast.Note{
		ID:    "test",
		Title: "Notifications are on",
		Body:  "Toru's notifications show up here, over every window, without taking focus",
		Kind:  toast.Success,
	})
}

// PreviewNotificationSound plays the notification sound at the current
// volume (the volume slider calls it on release).
func (s *SettingsService) PreviewNotificationSound() {
	toast.PlaySound(toast.Success)
}
