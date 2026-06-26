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
	"os"
	"slices"

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
	// overlay-v2 Go->JS events: engage resets a reused window to capture mode with
	// the fresh backdrop; edit morphs the same window into the annotation editor.
	// RegisterEvent is init-time + constant-name + panics on dup — these names are
	// new, so safe.
	application.RegisterEvent[overlay.MonitorSession](overlay.EventOverlayEngage)
	application.RegisterEvent[overlay.OverlayEditPayload](overlay.EventOverlayEdit)
	// overlay:cropRect relays the ONE shared cross-monitor crop (virtual-desktop
	// physical px) between the per-monitor windows so a straddle selection moves as
	// a single rect across the seam.
	application.RegisterEvent[capture.Rect](overlay.EventOverlayCropRect)
}

func main() {
	// startedAtLogin is true when Toru was launched by the Windows "Run at login"
	// registry entry, which SettingsService.SetLaunchAtLogin writes WITH the
	// --startup flag. In that case we boot SILENT in the tray (install the systray
	// + prewarm overlays, but do NOT open the Settings/home window) so signing in
	// doesn't flash a window. Every other launch opens Settings as before.
	startedAtLogin := slices.Contains(os.Args[1:], startupArg)

	// Per-Monitor-V2 DPI awareness MUST be set before any window is created so
	// screen coordinates and gdigrab capture come back in true physical pixels.
	dpi.EnsurePerMonitorV2()

	// The shared capture seam. RealCapturer does real still (screenshot)
	// capture via kbinani/screenshot and delegates the video path to the real
	// FFmpeg Recorder (ddagrab GPU path with gdigrab software fallback; VP9/
	// WebM by default per the codec policy in internal/capture/encoders.go).
	recorder := capture.NewRecorder()
	capturer := capture.NewRealCapturer(recorder)

	// Services bound to the frontend.
	overlaySvc := overlay.NewService(capturer)
	exportSvc := export.NewService()
	shotSvc := shot.New()
	vidSvc := vid.New()
	windowsSvc := &WindowsService{cap: capturer}
	updateSvc := update.New(updateRepo, version)

	// App-level preferences (currently just "launch at Windows login"). Backed by
	// Wails' AutostartManager; app is injected after application.New below.
	settingsSvc := &SettingsService{}

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
			application.NewService(settingsSvc),
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
		// Toru is a single-instance tray app. Without this, launching it again
		// (e.g. the user double-clicks the desktop icon while the login-started
		// instance is already resident in the tray) would spawn a SECOND tray icon
		// and a SECOND global keyboard hook — and the hotkey trampoline explicitly
		// assumes one instance (internal/hotkey/hook_windows.go). Instead, the
		// second launch hands off to the running instance, which surfaces the
		// Settings/home window, then the second process exits.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.stephenshorton.toru",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				windowsSvc.OpenSettings()
			},
		},
	})

	// Inject the running app into services that emit events / open windows / quit.
	overlaySvc.SetApp(app)
	windowsSvc.app = app
	windowsSvc.overlay = overlaySvc
	updateSvc.SetApp(app)
	settingsSvc.app = app // for app.Autostart (launch-at-login)

	// overlay-v2: a SINGLE-monitor screenshot is annotated IN PLACE on the same
	// overlay surface (single-surface morph via OverlayService.EnterEdit) — no
	// separate editor window. A STRADDLE screenshot (crop spanning >1 monitor) can't
	// morph in place, so EnterEditMulti stitches the region and opens it in the
	// standalone editor window via this opener.
	overlaySvc.SetEditorOpener(windowsSvc.OpenEditor)
	//
	// Video keeps its Go-side window opener: StartRecording dismisses the overlay
	// windows (record the live region, not the dim), which destroys the calling
	// JS context — so the recording pill (timer + Stop) MUST be opened from Go.
	overlaySvc.SetRecordingControlsOpener(windowsSvc.OpenRecordingControls)
	// A FAILED start (e.g. ffmpeg missing) also needs a Go-opened surface, since the
	// overlay is dismissed before the error is known — otherwise the user just sees a
	// blank screen. And a SUCCESSFUL start opens the click-through glowing border that
	// outlines the recorded region until StopRecording closes it.
	overlaySvc.SetRecordingErrorOpener(windowsSvc.OpenRecordingError)
	overlaySvc.SetRecordingFrameOpener(windowsSvc.OpenRecordingFrame)
	overlaySvc.SetRecordingFrameCloser(windowsSvc.CloseRecordingFrame)
	// Audio capture is a privacy-sensitive OPT-IN, per SOURCE: the recorder
	// starts with no audio selected; the overlay's Audio picker pushes the
	// user's selection (system mix / individual apps / microphone) through
	// the bound SetAudioSources (the frozen Capturer seam carries no flag).
	overlaySvc.SetAudioConfigSetter(recorder.SetAudioConfig)

	// Global Escape-to-cancel. The capture overlay is a transparent, frameless,
	// always-on-top window that may not hold WebView2 keyboard focus, so the in-page
	// DOM Esc handler can be missed (e.g. triggering capture over a fullscreen app).
	// We route Escape through the SAME low-level keyboard hook that powers
	// Win+Shift+S: the overlay arms it on engage (SetEscapeArmer -> ArmEscape) and an
	// armed Escape press fires Cancel. The hook never SWALLOWS Escape, so this can't
	// affect Escape anywhere else. Wire BOTH before Register so the action exists the
	// instant the hook installs.
	overlaySvc.SetEscapeArmer(keys.ArmEscape)
	keys.SetEscapeAction(func() { _ = overlaySvc.Cancel() })

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

		// Show the Settings/home window ONCE so the user sees Toru is running —
		// UNLESS this was a launch-at-login boot (--startup), where we stay silent
		// in the tray and let the user reach the home window from the tray icon.
		// Do NOT auto-pop the capture overlay on launch anymore.
		if !startedAtLogin {
			windowsSvc.OpenSettings()
		}

		// Pre-warm the reused overlay windows so their handles are ready. This only
		// creates the WebviewWindow objects (no navigation, no paint, no flicker);
		// the FIRST capture pays the one-time webview-navigation cost, and every
		// subsequent RE-engage is instant (freeze + Show only). The Screen cache is
		// populated here (we are inside app.Run()), so DPI bounds are correct.
		overlaySvc.PrewarmWindows()

		// Forced auto-update: keeping Toru current is part of using it, so we check
		// + silently install on every startup — INCLUDING a silent launch-at-login
		// boot (no window), which is the case the frontend updater can't cover. The
		// app is idle at startup, so this never interrupts a capture/recording. If
		// an update exists the app downloads it, quits, and the installer relaunches
		// the new version (project.nsi). Fire-and-forget; errors are logged, not
		// fatal. dev builds short-circuit (CheckForUpdate returns nil).
		go updateSvc.AutoUpdate()
	})

	// Best-effort: close the reused overlay windows on app shutdown. Process exit
	// frees everything regardless, so this is purely cosmetic belt-and-suspenders.
	app.OnShutdown(func() { overlaySvc.Teardown() })

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
