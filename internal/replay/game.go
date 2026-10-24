package replay

import (
	"path/filepath"
	"strings"
	"unicode"
)

// DesktopFolder is where clips go when no game is in focus (ShadowPlay's
// convention).
const DesktopFolder = "Desktop"

// foregroundApp describes the window in focus when the save hotkey fires.
type foregroundApp struct {
	ExePath     string // full image path ("" if the process couldn't be opened)
	Title       string // window title
	Description string // exe version-info FileDescription / ProductName
	Fullscreen  bool   // window covers its whole monitor (exclusive or borderless)
	PID         uint32 // owning process
}

// gameLibraries are install roots whose next path segment is the game's
// folder, which is usually its proper name ("steamapps\common\Counter-Strike
// Global Offensive\game\bin\win64\cs2.exe").
var gameLibraries = []string{
	`\steamapps\common\`,
	`\epic games\`,
	`\xboxgames\`,
	`\riot games\`,
	`\gog galaxy\games\`,
	`\gog games\`,
	`\ea games\`,
	`\ubisoft game launcher\games\`,
	`\battle.net\games\`,
}

// notGames are apps that often run fullscreen (video, presentations) or live
// inside a game library folder but aren't games. Lower-case exe names.
var notGames = map[string]bool{
	"explorer.exe": true, "applicationframehost.exe": true, "searchhost.exe": true,
	"chrome.exe": true, "msedge.exe": true, "firefox.exe": true, "brave.exe": true,
	"opera.exe": true, "opera_gx.exe": true, "vivaldi.exe": true, "arc.exe": true,
	"vlc.exe": true, "mpc-hc64.exe": true, "mpc-be64.exe": true, "potplayermini64.exe": true,
	"spotify.exe": true, "discord.exe": true, "discordptb.exe": true, "discordcanary.exe": true,
	"steam.exe": true, "steamwebhelper.exe": true, "epicgameslauncher.exe": true,
	"riotclientux.exe": true, "riotclientservices.exe": true, "battle.net.exe": true,
	"eadesktop.exe": true, "galaxyclient.exe": true, "upc.exe": true,
	"obs64.exe": true, "toru.exe": true, "toru-replay.exe": true,
	"windowsterminal.exe": true, "code.exe": true, "powerpnt.exe": true,
	"photos.exe": true, "microsoft.photos.exe": true, "zoom.exe": true, "ms-teams.exe": true,
}

// launcherFolders are library sub-folders that hold launchers, not games.
var launcherFolders = map[string]bool{
	"launcher": true, "riot client": true, "directxredist": true,
	"steamworks shared": true, "steamvr": true,
}

// gameFolderFor returns the folder name to file a clip under, or "" when the
// focused app isn't a game. A game is a fullscreen app (minus known video
// players/browsers) or anything run from a game library, windowed or not.
func gameFolderFor(app foregroundApp) string {
	exe := strings.ToLower(filepath.Base(app.ExePath))
	if app.ExePath != "" && notGames[exe] {
		return ""
	}
	if name, ok := libraryGameName(app.ExePath); ok {
		return name
	}
	if !app.Fullscreen {
		return ""
	}
	for _, candidate := range []string{cleanDescription(app.Description), app.Title, exeDisplayName(app.ExePath)} {
		if name := sanitizeFolder(candidate); name != "" {
			return name
		}
	}
	return ""
}

// libraryGameName extracts the game folder from a game-library install path.
func libraryGameName(exePath string) (string, bool) {
	if exePath == "" {
		return "", false
	}
	lower := strings.ToLower(exePath)
	for _, root := range gameLibraries {
		i := strings.Index(lower, root)
		if i < 0 {
			continue
		}
		rest := exePath[i+len(root):]
		folder, _, found := strings.Cut(rest, `\`)
		if !found || launcherFolders[strings.ToLower(folder)] {
			return "", false
		}
		if name := sanitizeFolder(folder); name != "" {
			return name, true
		}
	}
	return "", false
}

// cleanDescription drops version-info descriptions that name the engine or a
// bootstrapper instead of the game.
func cleanDescription(d string) string {
	l := strings.ToLower(strings.TrimSpace(d))
	for _, generic := range []string{"unreal engine", "bootstrap", "unity player", "launcher", "game executable"} {
		if strings.Contains(l, generic) {
			return ""
		}
	}
	return strings.TrimSpace(d)
}

// exeDisplayName turns "FortniteClient-Win64-Shipping.exe" into "FortniteClient".
func exeDisplayName(exePath string) string {
	name := strings.TrimSuffix(filepath.Base(exePath), filepath.Ext(exePath))
	for _, suffix := range []string{"-Win64-Shipping", "-WinGDK-Shipping", "-Win64-Test", "_x64", "-x64"} {
		if trimmed := strings.TrimSuffix(name, suffix); trimmed != "" {
			name = trimmed
		}
	}
	return name
}

// sanitizeFolder makes name safe as a Windows folder name: no reserved
// characters or control chars, no trailing dots/spaces, collapsed spaces,
// at most 80 runes. Returns "" if nothing usable is left.
func sanitizeFolder(name string) string {
	var b strings.Builder
	space := false
	for _, r := range name {
		if r < 32 || strings.ContainsRune(`<>:"/\|?*`, r) || unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				b.WriteRune(' ')
			}
			space = true
			continue
		}
		space = false
		b.WriteRune(r)
	}
	out := []rune(strings.TrimRight(b.String(), ". "))
	if len(out) > 80 {
		out = out[:80]
	}
	s := strings.TrimRight(string(out), ". ")
	switch strings.ToUpper(s) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "LPT1", "LPT2", "LPT3":
		return ""
	}
	return s
}
