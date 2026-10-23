//go:build windows

package replay

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// videosDir resolves the user's Videos known folder (follows OneDrive or a
// moved Videos folder), falling back to %USERPROFILE%\Videos.
func videosDir() string {
	if p, err := windows.KnownFolderPath(windows.FOLDERID_Videos, 0); err == nil && p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Videos")
}

// playSaveSound plays the system "asterisk" sound (success) or "hand" sound
// (failure). MessageBeep is async, so it never delays the hotkey path.
func playSaveSound(ok bool) {
	const mbIconAsterisk, mbIconHand = 0x40, 0x10
	t := uintptr(mbIconAsterisk)
	if !ok {
		t = mbIconHand
	}
	_, _, _ = procMessageBeep.Call(t)
}

var procMessageBeep = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBeep")
