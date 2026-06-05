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
	"github.com/StephenSHorton/toru/internal/vid"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

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

	app := application.New(application.Options{
		Name:        "Toru",
		Description: "macOS-style screenshot & screen recording for Windows",
		Services: []application.Service{
			application.NewService(overlaySvc),
			application.NewService(exportSvc),
			application.NewService(shotSvc),
			application.NewService(vidSvc),
			application.NewService(windowsSvc),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	// Inject the running app into services that emit events / open windows.
	overlaySvc.SetApp(app)
	windowsSvc.app = app

	// Tray + global hotkeys. The registrar is a stub until the Phase 0 spike
	// wires real RegisterHotKey/WM_HOTKEY; wiring it here documents intent.
	trayCtl := tray.New()
	trayCtl.SetState(tray.Idle)

	keys := hotkey.New()
	_ = keys.Register("overlay", hotkey.DefaultOverlay, windowsSvc.OpenOverlay)
	defer keys.Close()

	// Dev hub window. Real app launches the overlay from a hotkey and lives in
	// the tray; the hub is a convenience to drive both editors during Phase 0.
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:            "Toru",
		URL:              "/",
		Width:            720,
		Height:           560,
		BackgroundColour: dark,
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
