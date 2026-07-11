// Tray menu construction lives here (package main) because the system-tray
// icon is //go:embed'd in main.go (embed paths can't use "..") and the menu
// needs to call WindowsService openers + history + app.Quit.
package main

import (
	"os/exec"
	"runtime"

	"github.com/StephenSHorton/toru/internal/history"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayController owns the live system-tray icon + its right-click menu. The
// menu is rebuilt whenever a new capture lands in history so "Recent" stays
// current without a separate refresh action.
type trayController struct {
	tray     *application.SystemTray
	app      *application.App
	history  *history.Store
	windows  *WindowsService
	openOverlay func()
}

// rebuildMenu constructs a fresh tray menu and installs it. Safe to call from
// any goroutine — Wails marshals SetMenu/window ops as needed, and history.List
// is itself mutex-guarded.
func (t *trayController) rebuildMenu() {
	if t == nil || t.tray == nil || t.app == nil {
		return
	}
	menu := application.NewMenu()

	menu.Add("Capture (⊞ Shift S)").OnClick(func(*application.Context) {
		if t.openOverlay != nil {
			t.openOverlay()
		}
	})

	menu.AddSeparator()

	// Recent captures (newest first). Clicking re-opens the annotation editor
	// (screenshot) or trim editor (recording).
	items := []history.Item{}
	if t.history != nil {
		items = t.history.List()
	}
	if len(items) == 0 {
		empty := menu.Add("No recent captures")
		empty.SetEnabled(false)
	} else {
		// Section header (disabled label — Windows tray menus have no real headers).
		hdr := menu.Add("Recent")
		hdr.SetEnabled(false)
		for _, it := range items {
			it := it // capture for OnClick closure
			menu.Add(it.Label).OnClick(func(*application.Context) {
				t.openRecent(it)
			})
		}
	}

	menu.Add("Open captures folder…").OnClick(func(*application.Context) {
		t.openCapturesFolder()
	})

	menu.AddSeparator()
	menu.Add("Settings…").OnClick(func(*application.Context) {
		if t.windows != nil {
			t.windows.OpenSettings()
		}
	})
	menu.Add("Quit Toru").OnClick(func(*application.Context) {
		t.app.Quit()
	})

	t.tray.SetMenu(menu)
}

// openRecent re-opens a capture from history: screenshots go to the annotation
// editor, recordings to the trim editor. Missing files are silently ignored
// (history will prune them on the next Add).
func (t *trayController) openRecent(it history.Item) {
	if t.windows == nil || it.Path == "" {
		return
	}
	switch it.Kind {
	case history.KindVideo:
		t.windows.OpenTrim(it.Path)
	default:
		t.windows.OpenEditor(it.Path)
	}
}

// openCapturesFolder reveals %AppData%/toru/captures in Explorer (or the OS
// equivalent). No-op if the directory couldn't be created.
func (t *trayController) openCapturesFolder() {
	if t.history == nil {
		return
	}
	dir := t.history.Dir()
	if dir == "" {
		return
	}
	// Windows: explorer.exe <dir>. Other OSes are stubs for cross-compile.
	if runtime.GOOS == "windows" {
		_ = exec.Command("explorer.exe", dir).Start()
		return
	}
	_ = exec.Command("xdg-open", dir).Start()
}
