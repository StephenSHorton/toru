package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// whiteboard_theme.go is the whiteboard's Light/Dark choice (docs/whiteboard.md
// section 4). It's one choice for every board, saved in
// %AppData%\toru\whiteboard.json: the whiteboard window's toolbar switch sets
// it, every open board window follows it (whiteboard:theme), and the PNG, PDF
// and HTML exports are drawn in it, the agents' board.export too. The rest of
// Toru stays dark.

// The themes. Light is the default.
const (
	themeLight = "light"
	themeDark  = "dark"
)

// evWhiteboardTheme tells board windows the theme changed; its data is the theme.
const evWhiteboardTheme = "whiteboard:theme"

// whiteboardThemeDir is where whiteboard.json lives (a check sets it).
var whiteboardThemeDir = func() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg, "toru"), nil
}

var whiteboardThemeMu sync.Mutex

type whiteboardPrefs struct {
	Theme string `json:"theme"`
}

// parseWhiteboardTheme reads a theme's name; anything else is an error.
func parseWhiteboardTheme(s string) (string, error) {
	switch s {
	case themeLight, themeDark:
		return s, nil
	}
	return "", fmt.Errorf("no whiteboard theme %q (use light or dark)", s)
}

// loadWhiteboardTheme is the saved theme, or light when none is saved or the
// file can't be read.
func loadWhiteboardTheme() string {
	whiteboardThemeMu.Lock()
	defer whiteboardThemeMu.Unlock()
	dir, err := whiteboardThemeDir()
	if err != nil {
		return themeLight
	}
	raw, err := os.ReadFile(filepath.Join(dir, "whiteboard.json"))
	if err != nil {
		return themeLight
	}
	var p whiteboardPrefs
	if json.Unmarshal(bytes.TrimPrefix(raw, utf8BOM), &p) != nil {
		return themeLight
	}
	if t, err := parseWhiteboardTheme(p.Theme); err == nil {
		return t
	}
	return themeLight
}

// saveWhiteboardTheme writes the theme atomically (tmp + rename).
func saveWhiteboardTheme(theme string) error {
	theme, err := parseWhiteboardTheme(theme)
	if err != nil {
		return err
	}
	whiteboardThemeMu.Lock()
	defer whiteboardThemeMu.Unlock()
	dir, err := whiteboardThemeDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(whiteboardPrefs{Theme: theme}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "whiteboard-*.json.tmp")
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
	return os.Rename(tmp.Name(), filepath.Join(dir, "whiteboard.json"))
}

// whiteboardBackground is a board window's colour before its page paints:
// the theme's canvas (toru-ui-sdk.css --ui-background), so opening a board
// doesn't flash the other theme.
func whiteboardBackground(theme string) application.RGBA {
	if theme == themeDark {
		return application.NewRGB(0x32, 0x3b, 0x48)
	}
	return application.NewRGB(0xf9, 0xfa, 0xfb)
}

// Theme is the whiteboard's theme: "light" (the default) or "dark".
func (s *WhiteboardsService) Theme() string { return loadWhiteboardTheme() }

// SetTheme saves the whiteboard's theme ("light" or "dark") and tells every
// open board window.
func (s *WhiteboardsService) SetTheme(theme string) error {
	if err := saveWhiteboardTheme(theme); err != nil {
		return err
	}
	if s.emit != nil {
		s.emit(evWhiteboardTheme, theme)
	}
	return nil
}

// utf8BOM is skipped at the start of whiteboard.json (Notepad, PowerShell 5's
// Set-Content write one).
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}
