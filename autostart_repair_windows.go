//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// repairLaunchAtLogin re-points a stale "Start with Windows" entry at this
// executable. The Settings switch only reads as on when the Run entry's exe
// is the running one, so an entry left pointing elsewhere (a moved install,
// a test build that later auto-updated into the installed copy) showed the
// box unchecked while Windows still launched the old path. It only rewrites
// when the registered exe no longer exists or this is the installed copy, so
// a portable/dev build never hijacks a working installed entry.
func repairLaunchAtLogin() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return
	}
	defer func() { _ = key.Close() }()
	val, _, err := key.GetStringValue(autostartID)
	if err != nil {
		return // not enabled
	}
	registered := commandExe(val)
	if strings.EqualFold(registered, exe) {
		return
	}
	_, statErr := os.Stat(registered)
	if statErr == nil && !isInstalledCopy(exe) {
		return
	}
	_ = key.SetStringValue(autostartID, `"`+exe+`" `+startupArg)
}

// isInstalledCopy reports whether exe is the installer's copy
// (%LOCALAPPDATA%\Programs\Toru\toru.exe).
func isInstalledCopy(exe string) bool {
	local := os.Getenv("LOCALAPPDATA")
	return local != "" && strings.EqualFold(filepath.Dir(exe), filepath.Join(local, "Programs", "Toru"))
}

// commandExe returns the executable of a Run-key command line (first token,
// honouring quotes).
func commandExe(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if strings.HasPrefix(cmd, `"`) {
		if end := strings.Index(cmd[1:], `"`); end >= 0 {
			return cmd[1 : end+1]
		}
		return strings.Trim(cmd, `"`)
	}
	if exe, _, found := strings.Cut(cmd, " "); found {
		return exe
	}
	return cmd
}
