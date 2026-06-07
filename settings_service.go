package main

import (
	"fmt"

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
// keyboard shortcuts. Right now that's just "launch at Windows login", backed
// directly by Wails' AutostartManager — on Windows the registry Run key IS the
// source of truth, so there is nothing extra to persist in settings.json.
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
