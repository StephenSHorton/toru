//go:build windows

package export

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// saveAsDialog shows a native Save-As dialog (Windows IFileSaveDialog via the
// Wails v3 runtime), seeded with suggestedName, and copies srcPath to the
// chosen destination. It returns the chosen path, or ("", nil) on cancel.
//
// API (verified against wails v3.0.0-alpha.98):
//
//	application.Get().Dialog.SaveFile().
//	    SetFilename(name).AddFilter(...).
//	    PromptForSingleSelection() (string, error)
//
// Cancel contract: the underlying go-common-file-dialog returns an error
// (cfd.ErrorCancelled) on cancel, so PromptForSingleSelection returns
// ("", err). We treat ANY error OR an empty path as a cancel and return
// ("", nil) — never surfacing a benign cancel as a failure to JS.
//
// OVERLAY GOTCHA: Toru's capture/edit windows are AlwaysOnTop fullscreen
// overlays. Without temporarily dropping that flag the native Save dialog is
// created correctly but sits BEHIND the overlay, so the user thinks Save is a
// no-op. We lower AlwaysOnTop on every visible toru-overlay-* window for the
// duration of the dialog, then restore it.
func saveAsDialog(srcPath, suggestedName string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("saveAs: no running application")
	}

	restore := lowerOverlayAlwaysOnTop(app)
	defer restore()

	dlg := app.Dialog.SaveFile().SetFilename(suggestedName)
	addFiltersFor(dlg, suggestedName)

	// Prefer attaching to the focused window so the dialog is owned/modal to it
	// (and therefore stacks above it once AlwaysOnTop is cleared).
	if cur := app.Window.Current(); cur != nil {
		dlg.AttachToWindow(cur)
	}

	chosen, err := dlg.PromptForSingleSelection()
	if err != nil || chosen == "" {
		// Cancel (or any dialog error) => not an error to the caller.
		return "", nil
	}

	if err := copyFile(srcPath, chosen); err != nil {
		return "", fmt.Errorf("saveAs: copy to %q: %w", chosen, err)
	}
	return chosen, nil
}

// lowerOverlayAlwaysOnTop drops AlwaysOnTop on every visible overlay window
// (Name prefix "toru-overlay-") so a native file dialog can appear above them.
// The returned restore re-enables AlwaysOnTop on those same windows.
func lowerOverlayAlwaysOnTop(app *application.App) (restore func()) {
	var touched []application.Window
	for _, w := range app.Window.GetAll() {
		if w == nil || !w.IsVisible() {
			continue
		}
		name := w.Name()
		if !strings.HasPrefix(name, "toru-overlay-") {
			continue
		}
		w.SetAlwaysOnTop(false)
		touched = append(touched, w)
	}
	return func() {
		for _, w := range touched {
			// Window may have been destroyed mid-dialog (unlikely); SetAlwaysOnTop
			// is safe on a live window and we only ever touch ones we lowered.
			w.SetAlwaysOnTop(true)
		}
	}
}

// addFiltersFor seeds the Save dialog's type filters based on the suggested
// filename extension so video Save-As isn't stuck offering "*.png".
func addFiltersFor(dlg *application.SaveFileDialogStruct, suggestedName string) {
	ext := strings.ToLower(filepath.Ext(suggestedName))
	switch ext {
	case ".webm":
		dlg.AddFilter("WebM Video", "*.webm")
		dlg.AddFilter("All Files", "*.*")
	case ".mp4":
		dlg.AddFilter("MP4 Video", "*.mp4")
		dlg.AddFilter("All Files", "*.*")
	case ".gif":
		dlg.AddFilter("GIF Image", "*.gif")
		dlg.AddFilter("All Files", "*.*")
	default:
		// Screenshots (and anything unknown) default to PNG.
		dlg.AddFilter("PNG Image", "*.png")
		dlg.AddFilter("All Files", "*.*")
	}
}

// copyFile copies src to dst, truncating/creating dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
