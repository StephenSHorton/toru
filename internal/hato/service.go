package hato

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/skip2/go-qrcode"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// HatoService is the Wails-bound Hato API: the transfer Manager plus the
// desktop glue the page needs (native pickers, the receive-folder preference,
// Explorer, a QR of the code). Dialogs follow internal/history exactly:
// application.Get().Dialog…AttachToWindow(app.Window.Current()) so the picker
// is modal to the hub window that asked for it.
//
// JS binding name: HatoService.*
type HatoService struct {
	m   *Manager
	app *application.App
}

// NewService returns an idle service. SetApp must follow application.New.
// It also sweeps wormhole-william scratch zips left in %TEMP% by an earlier
// run (a folder send that was stopped or failed before its code; the library
// keeps that handle open, so only the next process can delete the file).
func NewService() *HatoService {
	go func() {
		if n := sweepStaleTempZips(staleTempZipAge); n > 0 {
			log.Printf("hato: removed %d stale scratch zip(s) from %%TEMP%%", n)
		}
	}()
	return &HatoService{m: NewManager()}
}

// SetApp injects the running app and routes Manager events through it.
//
//wails:ignore
func (s *HatoService) SetApp(app *application.App) {
	s.app = app
	if app != nil {
		s.m.SetEmitter(func(name string, data any) { app.Event.Emit(name, data) })
	}
}

// Shutdown cancels both transfers (wired to app.OnShutdown).
//
//wails:ignore
func (s *HatoService) Shutdown() {
	s.m.Shutdown()
}

// StartSend begins sending the file or folder at path, replacing any active
// send. Progress arrives on hato:send.
func (s *HatoService) StartSend(path string) error {
	return s.m.StartSend(path)
}

// StopSend cancels the active send (phase → "stopped").
func (s *HatoService) StopSend() error {
	return s.m.StopSend()
}

// SendStatus returns the current send state; the page fetches it on mount so a
// transfer started from the tray or a Library card is already on screen.
func (s *HatoService) SendStatus() SendState {
	return s.m.SendStatus()
}

// StartReceive receives the transfer behind code into the configured folder.
func (s *HatoService) StartReceive(code string) error {
	return s.m.StartReceive(code, loadReceiveDir())
}

// CancelReceive cancels the active receive (phase → "cancelled").
func (s *HatoService) CancelReceive() error {
	return s.m.CancelReceive()
}

// ReceiveStatus returns the current receive state.
func (s *HatoService) ReceiveStatus() ReceiveState {
	return s.m.ReceiveStatus()
}

// PickSendFile opens a native file picker and returns the chosen path, or ""
// when the user cancels. It does NOT start the send — the page decides.
func (s *HatoService) PickSendFile() (string, error) {
	return pickPath("Choose a file to send", true)
}

// PickSendFolder is PickSendFile for a folder.
func (s *HatoService) PickSendFolder() (string, error) {
	return pickPath("Choose a folder to send", false)
}

// pickPath runs the shared open dialog. Cancel is reported as ("", nil): the
// frontend treats it as "nothing happened", not a failure to toast.
func pickPath(title string, files bool) (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("hato: no running application")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseFiles(files).
		CanChooseDirectories(!files).
		SetTitle(title)
	if win := app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		return "", nil
	}
	return chosen, nil
}

// GetReceiveDir returns the folder incoming transfers are saved to.
func (s *HatoService) GetReceiveDir() string {
	return loadReceiveDir()
}

// IsDefaultReceiveDir reports whether the receive folder is the built-in
// default (Downloads), so the page can grey out "Reset default".
func (s *HatoService) IsDefaultReceiveDir() bool {
	return samePath(loadReceiveDir(), DefaultReceiveDir())
}

// PickReceiveDir opens a folder picker and persists the choice. Returns the
// new folder, or "" if the user cancelled (nothing changes).
func (s *HatoService) PickReceiveDir() (string, error) {
	app := application.Get()
	if app == nil {
		return "", errors.New("hato: no running application")
	}
	dlg := app.Dialog.OpenFile().
		CanChooseDirectories(true).
		CanChooseFiles(false).
		CanCreateDirectories(true).
		SetTitle("Choose where Hato saves received files").
		SetDirectory(loadReceiveDir())
	if win := app.Window.Current(); win != nil {
		dlg = dlg.AttachToWindow(win)
	}
	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		return "", nil
	}
	if err := os.MkdirAll(chosen, 0o755); err != nil {
		return "", err
	}
	if err := saveReceiveDir(chosen); err != nil {
		return "", err
	}
	return loadReceiveDir(), nil
}

// ResetReceiveDir restores the default receive folder.
func (s *HatoService) ResetReceiveDir() error {
	return saveReceiveDir("")
}

// OpenReceiveDir shows the receive folder in Explorer.
func (s *HatoService) OpenReceiveDir() error {
	dir := loadReceiveDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return openFolder(dir)
}

// RevealReceived opens Explorer with the last completed receive selected
// (file or folder). Errors when nothing has finished yet or the item has
// since been moved.
func (s *HatoService) RevealReceived() error {
	st := s.m.ReceiveStatus()
	if st.Phase != RecvDone || st.OutPath == "" {
		return errors.New("hato: nothing received yet")
	}
	if _, err := os.Stat(st.OutPath); err != nil {
		return fmt.Errorf("hato: %w", err)
	}
	return revealInExplorer(st.OutPath)
}

// CodeQR renders the plain code text as a QR PNG and returns it base64-encoded
// (no data: prefix; the page adds it). The QR holds just the code — not a URL —
// so a phone camera scan drops the exact string into `wormhole receive`.
func (s *HatoService) CodeQR(code string) (string, error) {
	code = NormalizeCode(code)
	if code == "" {
		return "", errors.New("hato: no code")
	}
	png, err := qrcode.Encode(code, qrcode.Medium, 256)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(png), nil
}

// HandleDrop is the hub window's WindowFilesDropped sink (windows.go), not a
// JS API — the page already has StartSend. The first dropped path starts a
// send (one send slot, so extra paths are ignored rather than queued). A
// synchronous rejection (vanished path, drive root) has no caller to return
// to, so it is published as an error-phase hato:send when no send is live, and
// the page shows "Couldn't send …" instead of a drop that appears to do nothing.
//
//wails:ignore
func (s *HatoService) HandleDrop(paths []string) {
	if len(paths) == 0 {
		return
	}
	if err := s.m.StartSend(paths[0]); err != nil {
		log.Printf("hato: drop: %v", err)
		s.m.FailIdleSend(paths[0], err)
	}
}
