// Toru — a macOS-style screenshot & screen-recording tool for Windows 11.
//
// Architecture (see screen-capture-app-plan.md / CONTRIBUTING.md):
//   - internal/capture  : the SHARED seam. ONE contract (contract.go), ONE
//     Capture() entrypoint. Both editors import only this.
//   - internal/overlay  : shared dim/crop overlay + screen source-of-truth.
//   - internal/export   : shared copy-to-clipboard + save-as (both media types).
//   - internal/shot     : Developer 1 — screenshot editor helpers.
//   - internal/vid      : Developer 2 — video record + trim.
package main

import (
	"embed"
	"log"

	"github.com/StephenSHorton/toru/internal/capture"
	"github.com/StephenSHorton/toru/internal/dpi"
	"github.com/StephenSHorton/toru/internal/export"
	"github.com/StephenSHorton/toru/internal/hotkey"
	"github.com/StephenSHorton/toru/internal/overlay"
	"github.com/StephenSHorton/toru/internal/shot"
	"github.com/StephenSHorton/toru/internal/update"
	"github.com/StephenSHorton/toru/internal/vid"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

// trayIconPNG is the system-tray icon. //go:embed requires the embedded path to
// live under the embedding package's directory (no parent ".." paths), so the
// embed MUST be in package main here — this is why the tray is built on the App
// in main.go rather than in a separate internal/tray package. The Windows tray
// (w32.CreateSmallHIconFromImage) magic-byte-checks PNG/ICO and auto-scales the
// 1024px PNG down to the system small-icon size, so no manual .ico is needed.
//
//go:embed build/appicon.png
var trayIconPNG []byte

// version is the running app version, injected at release build time via
// -ldflags "-X main.version=X.Y.Z" (see build/windows/Taskfile.yml). It stays
// "dev" for local/dev builds, which the updater treats as "never offer updates".
var version = "dev"

// updateRepo is the GitHub repo the in-app updater checks for releases.
const updateRepo = "StephenSHorton/toru"

func init() {
	// Typed event payloads picked up by the binding generator.
	application.RegisterEvent[capture.CaptureResult](overlay.EventCaptureDone)
}

func main() {
	// Per-Monitor-V2 DPI awareness MUST be set before any window is created so
	// screen coordinates and gdigrab capture come back in true physical pixels.
	dpi.EnsurePerMonitorV2()

	// The shared capture seam. RealCapturer does real still (screenshot)
	// capture via kbinani/screenshot and delegates the video path to the
	// StubCapturer until the FFmpeg pipeline lands — the contract does not move.
	capturer := capture.NewRealCapturer(&capture.StubCapturer{})

	// Services bound to the frontend.
	overlaySvc := overlay.NewService(capturer)
	exportSvc := export.NewService()
	shotSvc := shot.New()
	vidSvc := vid.New()
	windowsSvc := &WindowsService{cap: capturer}
	updateSvc := update.New(updateRepo, version)

	// Global hotkeys. The Manager owns a low-level keyboard hook (WH_KEYBOARD_LL)
	// so it can capture AND swallow Win-key combos — the default is Win+Shift+S,
	// which RegisterHotKey can't claim from the Snipping Tool. HotkeyService
	// exposes the combo-builder to the React Shortcuts panel.
	keys := hotkey.New()
	hotkeySvc := hotkey.NewService(keys)

	app := application.New(application.Options{
		Name:        "Toru",
		Description: "macOS-style screenshot & screen recording for Windows",
		Services: []application.Service{
			application.NewService(overlaySvc),
			application.NewService(exportSvc),
			application.NewService(shotSvc),
			application.NewService(vidSvc),
			application.NewService(windowsSvc),
			application.NewService(updateSvc),
			application.NewService(hotkeySvc),
		},
		Assets: application.AssetOptions{
			Handler:    application.AssetFileServerFS(assets),
			Middleware: overlaySvc.ShotMiddleware(),
		},
		// Closing every overlay window (Cancel/Esc/commit) drops the live window
		// count to zero. On Windows that posts WM_QUIT the instant windowMap
		// empties (application_windows.go), which would race-quit the app before
		// the Hub/Editor window is created — and would reliably kill an
		// in-progress video recording (StartRecording dismisses the overlay and
		// opens no window). Keep the app alive across a fully-windowless moment.
		Windows: application.WindowsOptions{
			DisableQuitOnLastWindowClosed: true,
		},
	})

	// Inject the running app into services that emit events / open windows / quit.
	overlaySvc.SetApp(app)
	windowsSvc.app = app
	windowsSvc.overlay = overlaySvc
	updateSvc.SetApp(app)

	// Overlay -> Windows callback: a committed screenshot opens the editor.
	// Passed as a Go-only func (not JS-bound). Cancel/Esc no longer opens any
	// window — it just dismisses the overlay back to idle (the tray).
	overlaySvc.SetEditorOpener(windowsSvc.OpenEditor)

	// Install the global hotkey hook AFTER injection (windowsSvc.overlay is set
	// above), so a hotkey press that lands before app.Run can still open the
	// overlay (OpenOverlay also nil-guards w.overlay). Register the persisted
	// "overlay" binding (default Win+Shift+S); the first Register installs the
	// low-level keyboard hook on a dedicated OS thread.
	overlayBinding := hotkey.LoadBinding("overlay", hotkey.DefaultOverlay)
	_ = keys.Register("overlay", overlayBinding, windowsSvc.OpenOverlay)
	defer keys.Close()

	// LAUNCH -> TRAY + SETTINGS. Toru is a tray ("menu-bar style") app: on launch
	// it installs the system-tray icon and opens the Settings/home window ONCE so
	// the user can see Toru is running, change the shortcut, and hit Capture. It
	// no longer auto-pops the fullscreen capture overlay — capture is reached via
	// Win+Shift+S, the tray menu's Capture item, or the tray's double-click.
	//
	// This MUST run on events.Common.ApplicationStarted, NOT synchronously before
	// app.Run(): Wails only builds the platform app (running==true) and populates
	// the Screen cache inside Run() via newPlatformApp. SystemTray.New early-runs
	// only once running==true (runOrDeferToAppRun); any window opened here reads a
	// populated Screen.GetAll() so DPI/scale is correct (the same EMPTY-cache
	// hazard that the overlay path guards against). The listener fires exactly
	// once per process, so New() is called exactly once (no duplicate tray icons).
	// The menu/click callbacks run on goroutines, but Window.NewWithOptions and
	// app.Quit marshal to the main thread internally, so the direct calls are safe.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		// Build the menu-bar/tray home. Safe here: app.running==true and the
		// Screen cache is populated (newPlatformApp ran inside app.Run()).
		systray := app.SystemTray.New()
		systray.SetIcon(trayIconPNG) // PNG bytes; Win32 decodes via CreateSmallHIconFromImage + auto-scales
		systray.SetTooltip("Toru — screen capture (⊞ Shift S)")
		menu := application.NewMenu()
		menu.Add("Capture (⊞ Shift S)").OnClick(func(*application.Context) { windowsSvc.OpenOverlay() })
		menu.Add("Settings…").OnClick(func(*application.Context) { windowsSvc.OpenSettings() })
		menu.AddSeparator()
		menu.Add("Quit Toru").OnClick(func(*application.Context) { app.Quit() })
		systray.SetMenu(menu)
		systray.OnClick(func() { windowsSvc.OpenSettings() })      // left-click = open home (menu-bar feel)
		systray.OnDoubleClick(func() { windowsSvc.OpenOverlay() }) // double-click = quick capture
		// Right-click opens the menu automatically (Wails smart default).

		// Show the Settings/home window ONCE so the user sees Toru is running.
		// Do NOT auto-pop the capture overlay on launch anymore.
		windowsSvc.OpenSettings()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
