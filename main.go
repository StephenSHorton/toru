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
	"github.com/StephenSHorton/toru/internal/tray"
	"github.com/StephenSHorton/toru/internal/update"
	"github.com/StephenSHorton/toru/internal/vid"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

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
	// capture via kbinani/screenshot and delegates the video path to the real
	// FFmpeg Recorder (ddagrab GPU path with gdigrab software fallback; VP9/
	// WebM by default per the codec policy in internal/capture/encoders.go).
	capturer := capture.NewRealCapturer(capture.NewRecorder())

	// Services bound to the frontend.
	overlaySvc := overlay.NewService(capturer)
	exportSvc := export.NewService()
	shotSvc := shot.New()
	vidSvc := vid.New()
	windowsSvc := &WindowsService{cap: capturer}
	updateSvc := update.New(updateRepo, version)

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

	// Overlay -> Windows callbacks: Cancel/Esc returns to the Hub; a committed
	// screenshot opens the editor. Passed as Go-only funcs (not JS-bound).
	overlaySvc.SetHubOpener(windowsSvc.OpenHub)
	overlaySvc.SetEditorOpener(windowsSvc.OpenEditor)
	overlaySvc.SetRecordingControlsOpener(windowsSvc.OpenRecordingControls)

	// Tray + global hotkeys. The registrar is a stub until the Phase 0 spike
	// wires real RegisterHotKey/WM_HOTKEY; wiring it here documents intent.
	trayCtl := tray.New()
	trayCtl.SetState(tray.Idle)

	keys := hotkey.New()
	_ = keys.Register("overlay", hotkey.DefaultOverlay, windowsSvc.OpenOverlay)
	defer keys.Close()

	// LAUNCH -> OVERLAY. Opening Toru immediately paints the real all-monitors
	// frozen-still dim+crop overlay (BeginSession freezes every monitor BEFORE
	// any window is shown), with a crop pre-placed on the primary. Esc/Cancel
	// tears it all down and opens the dev Hub (which keeps both editors reachable
	// during Phase 0).
	//
	// This MUST run on ApplicationStarted, NOT synchronously before app.Run():
	// Wails only builds the platform app (and populates the Screen cache) inside
	// Run() via newPlatformApp. Calling BeginSession before then would read an
	// EMPTY Screen.GetAll(), so every monitor's ScaleFactor/IsPrimary and DIP
	// window bounds would silently fall back to scale=1.0 — breaking sizing and
	// crop math on every HiDPI monitor (the launch path is the whole feature).
	// The listener runs on a goroutine; Window.NewWithOptions marshals window
	// creation to the main thread internally, so this is safe.
	app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		windowsSvc.OpenOverlay()
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
