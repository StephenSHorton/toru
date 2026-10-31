// Tray menu construction lives here (package main) because the system-tray
// icon is //go:embed'd in main.go (embed paths can't use "..") and the menu
// needs to call WindowsService openers + history + app.Quit.
package main

import (
	"os/exec"
	"runtime"
	"strings"
	"sync"

	toast "github.com/StephenSHorton/go-toast"
	"github.com/StephenSHorton/toru/internal/clicker"
	"github.com/StephenSHorton/toru/internal/history"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/StephenSHorton/toru/internal/replay"
	"github.com/StephenSHorton/toru/internal/update"
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
	// openSendFiles opens the hub on the Send Files page.
	openSendFiles func()
	// update supplies the "Restart to update to vX" item while an update is ready.
	update *update.UpdateService
	// announced is the update version whose "ready" notification has been
	// posted, so it is posted once per version per run. rebuildMenu runs from
	// many goroutines, hence the lock.
	announceMu sync.Mutex
	announced  string
	// warned is whether the card for announced says Install stops the
	// current recording.
	warned bool
	// clicker backs the "Auto clicker" check item; openClicker opens its hub page.
	clicker     *clicker.ClickerService
	openClicker func()
	// openVideoEditor opens the hub's Video Editor page.
	openVideoEditor func()
}

// rebuildMenu constructs a fresh tray menu and installs it. Safe to call from
// any goroutine — Wails marshals SetMenu/window ops as needed, and history.List
// is itself mutex-guarded.
func (t *trayController) rebuildMenu() {
	if t == nil || t.tray == nil || t.app == nil {
		return
	}
	menu := application.NewMenu()

	// A downloaded update sits at the top until the user restarts (or quits,
	// which installs it without relaunching).
	if t.update != nil {
		if st := t.update.GetStatus(); st.Phase == update.PhaseReady {
			menu.Add("Restart to update to v" + st.Version).OnClick(func(*application.Context) {
				go t.restartToUpdate()
			})
			menu.AddSeparator()
			// The updater's onChange rebuilds this menu on every transition, so
			// this is also where the ready notification goes up.
			t.announceUpdate(st)
		}
	}

	menu.Add("Capture (⊞ Shift S)").OnClick(func(*application.Context) {
		if t.openOverlay != nil {
			t.openOverlay()
		}
	})

	t.addReplayItems(menu)
	// Send Files: the hub's file-transfer page, where a drop/picker issues the code.
	menu.Add("Send files…").OnClick(func(*application.Context) {
		if t.openSendFiles != nil {
			t.openSendFiles()
		}
	})

	t.addClickerItems(menu)

	menu.Add("Video editor").OnClick(func(*application.Context) {
		if t.openVideoEditor != nil {
			t.openVideoEditor()
		}
	})

	menu.AddSeparator()

	// Recent captures (newest first). Clicking re-opens the annotation editor
	// (screenshot) or trim editor (recording).
	items := []history.Item{}
	if t.history != nil {
		items = t.history.List()
		if len(items) > history.MaxItems {
			items = items[:history.MaxItems]
		}
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

// restartToUpdate is the one install path for the tray item and the update
// notification's Install button: launch the installer and quit. A tray-only
// user has no banner to read a failure from, and the service only logs it and
// reverts to ready, so say why nothing happened.
func (t *trayController) restartToUpdate() {
	if err := t.update.RestartToUpdate(); err != nil {
		toast.Post(toast.Note{ID: "update", Kind: toast.Error, Title: "Couldn't start the update", Body: err.Error()})
	}
}

// announceUpdate posts the sticky "Toru vX is ready" notification, once per
// version per run (rebuildMenu calls it on every rebuild while ready).
func (t *trayController) announceUpdate(st update.Status) {
	t.announceMu.Lock()
	if t.announced == st.Version {
		t.announceMu.Unlock()
		return
	}
	t.announced = st.Version
	t.announceMu.Unlock()
	t.postUpdateCard(st, t.recording(), false)
}

// postUpdateCard posts the update card, warning that Install stops the
// current recording when warn. The app has no confirm step before restarting,
// only the banner's warning, so the card carries the same one. A sticky card
// can stay up long after it was posted, so Install checks again: a Game
// session started since then re-posts the card with the warning instead of
// installing, and the next press is a deliberate one.
func (t *trayController) postUpdateCard(st update.Status, warn, silent bool) {
	t.announceMu.Lock()
	t.warned = warn
	t.announceMu.Unlock()

	body := "Install restarts Toru. Or keep working — it installs when you quit."
	if warn {
		body = "Install restarts Toru and stops the current recording. Or it installs when you quit."
	}
	install := func() {
		t.announceMu.Lock()
		warned := t.warned
		t.announceMu.Unlock()
		if !warned && t.recording() {
			t.postUpdateCard(st, true, true) // a reply to the click: no second chime
			return
		}
		t.restartToUpdate()
	}
	n := toast.Note{
		ID:     "update",
		Title:  "Toru v" + st.Version + " is ready",
		Body:   body,
		Kind:   toast.Info,
		Sticky: true,
		Silent: silent,
		Actions: []toast.Action{
			{Label: "Install", Run: install},
			{Label: "Later"}, // just closes the card; it still installs on quit
		},
	}
	if t.windows != nil {
		n.OnClick = t.windows.OpenSettings // the hub's update banner
	}
	toast.Post(n)
}

// recording reports whether a Game session or Hotkey recording is capturing
// now, which installing an update would stop.
func (t *trayController) recording() bool {
	return t.replay != nil && t.replay.GetRecorderState().Recording
}

// addReplayItems adds the instant-replay toggle, and while it's on, the save
// action labelled with its hotkey.
func (t *trayController) addReplayItems(menu *application.Menu) {
	if t.replay == nil {
		return
	}
	st := t.replay.GetSettings()
	if st.Enabled && t.replay.CaptureTrouble() != "" {
		// The buffer keeps retrying; this says so where a gamer looks.
		menu.Add("Game recording: capture keeps stopping, retrying").SetEnabled(false)
	}
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
	menu.Add("Open clips folder…").OnClick(func(*application.Context) {
		_ = t.replay.OpenSaveDir()
	})
}

// comboLabel renders a binding compactly for menu labels: "Ctrl+Shift+S",
// "Alt+F10", chords and sides included ("LCtrl+A+S").
func comboLabel(b hotkey.Binding) string {
	label := strings.ReplaceAll(hotkey.ComboLabel(b), " + ", "+")
	return strings.Replace(label, "Win", "⊞", 1)
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
		t.windows.OpenLibraryEditor(it.Path, it.ID)
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

// addClickerItems adds the auto clicker toggle (checked while a run is active)
// and a shortcut to its settings page.
func (t *trayController) addClickerItems(menu *application.Menu) {
	if t.clicker == nil {
		return
	}
	label := "Auto clicker"
	st := t.clicker.Status()
	if st.Hotkey != "" {
		label += " (" + st.Hotkey + ")"
	}
	running := st.State != clicker.StateIdle
	menu.AddCheckbox(label, running).OnClick(func(*application.Context) {
		go t.clicker.TrayToggle()
	})
	menu.Add("Auto clicker settings…").OnClick(func(*application.Context) {
		if t.openClicker != nil {
			t.openClicker()
		}
	})
}
