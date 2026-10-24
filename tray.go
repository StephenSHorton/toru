// Tray menu construction lives here (package main) because the system-tray
// icon is //go:embed'd in main.go (embed paths can't use "..") and the menu
// needs to call WindowsService openers + history + app.Quit.
package main

import (
	"os/exec"
	"runtime"
	"strings"

	"github.com/StephenSHorton/toru/internal/history"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/StephenSHorton/toru/internal/replay"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// trayController owns the live system-tray icon + its right-click menu. The
// menu is rebuilt whenever a new capture lands in history so "Recent" stays
// current without a separate refresh action.
type trayController struct {
	tray        *application.SystemTray
	app         *application.App
	history     *history.Store
	windows     *WindowsService
	openOverlay func()
	replay      *replay.ReplayService
	keys        *hotkey.Manager
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

	t.addReplayItems(menu)

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
	menu.Add("Open Toru…").OnClick(func(*application.Context) {
		if t.windows != nil {
			t.windows.OpenSettings()
		}
	})
	menu.Add("Quit Toru").OnClick(func(*application.Context) {
		t.app.Quit()
	})

	t.tray.SetMenu(menu)
}

// addReplayItems adds the instant-replay toggle, and while it's on, the save
// action labelled with its hotkey.
func (t *trayController) addReplayItems(menu *application.Menu) {
	if t.replay == nil {
		return
	}
	st := t.replay.GetSettings()
	if st.Enabled && st.Mode == replay.ModeManual {
		label := "Start recording"
		if t.replay.GetRecorderState().Manual {
			label = "Stop recording"
		}
		if t.keys != nil {
			if b, ok := t.keys.Binding("recordToggle"); ok && b.Key != 0 {
				label += " (" + comboLabel(b) + ")"
			}
		}
		menu.Add(label).OnClick(func(*application.Context) {
			go func() {
				_, _ = t.replay.ToggleRecording()
				t.rebuildMenu()
			}()
		})
	}
	if st.Enabled {
		label := "Save instant replay"
		if t.keys != nil {
			if b, ok := t.keys.Binding("replaySave"); ok && b.Key != 0 {
				label += " (" + comboLabel(b) + ")"
			}
		}
		menu.Add(label).OnClick(func(*application.Context) {
			go func() { _, _ = t.replay.SaveReplay() }()
		})
	}
	menu.AddCheckbox("Game recording", st.Enabled).OnClick(func(*application.Context) {
		next := t.replay.GetSettings()
		next.Enabled = !next.Enabled
		go func() { _, _ = t.replay.SetSettings(next) }() // SetOnChange rebuilds the menu
	})
	menu.Add("Open replays folder…").OnClick(func(*application.Context) {
		_ = t.replay.OpenSaveDir()
	})
}

// comboLabel renders a binding as "Ctrl+Shift+S" / "Alt+F10" for menu labels.
func comboLabel(b hotkey.Binding) string {
	var parts []string
	if b.Win {
		parts = append(parts, "⊞")
	}
	if b.Ctrl {
		parts = append(parts, "Ctrl")
	}
	if b.Alt {
		parts = append(parts, "Alt")
	}
	if b.Shift {
		parts = append(parts, "Shift")
	}
	return strings.Join(append(parts, hotkey.KeyName(b.Key)), "+")
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

// openCapturesFolder reveals the configured library folder in Explorer (or the OS
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
