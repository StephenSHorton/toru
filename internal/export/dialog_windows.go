//go:build windows

package export

import (
	"fmt"
	"io"
	"os"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// saveAsDialog shows a native Save-As dialog (Windows IFileSaveDialog via the
// Wails v3 runtime), seeded with suggestedName, and copies srcPath to the
// chosen destination. It returns the chosen path, or ("", nil) on cancel.
//
// API (verified against wails v3.0.0-alpha.98):
//
//	application.Get().Dialog.SaveFile().
//	    SetFilename(name).AddFilter("PNG Image", "*.png").
//	    PromptForSingleSelection() (string, error)
//
// Cancel contract: the underlying go-common-file-dialog returns an error
// (cfd.ErrorCancelled) on cancel, so PromptForSingleSelection returns
// ("", err). We treat ANY error OR an empty path as a cancel and return
// ("", nil) — never surfacing a benign cancel as a failure to JS.
//
// This dispatches to the main thread internally via the Wails runtime, so it
// only works while app.Run() is live. It is reached exclusively through the JS
// binding (a running app), never from a no-app unit test — see the build tag.
func saveAsDialog(srcPath, suggestedName string) (string, error) {
	app := application.Get()
	if app == nil {
		return "", fmt.Errorf("saveAs: no running application")
	}

	dlg := app.Dialog.SaveFile().
		SetFilename(suggestedName).
		AddFilter("PNG Image", "*.png")

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
