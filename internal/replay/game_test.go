package replay

import (
	"path/filepath"
	"testing"
)

func TestGameFolderFor(t *testing.T) {
	cases := []struct {
		name string
		app  foregroundApp
		want string
	}{
		{"steam windowed", foregroundApp{ExePath: `D:\SteamLibrary\steamapps\common\Counter-Strike Global Offensive\game\bin\win64\cs2.exe`}, "Counter-Strike Global Offensive"},
		{"epic", foregroundApp{ExePath: `C:\Program Files\Epic Games\Fortnite\FortniteGame\Binaries\Win64\FortniteClient-Win64-Shipping.exe`, Fullscreen: true}, "Fortnite"},
		{"riot", foregroundApp{ExePath: `C:\Riot Games\VALORANT\live\ShooterGame\Binaries\Win64\VALORANT-Win64-Shipping.exe`}, "VALORANT"},
		{"epic launcher is not a game", foregroundApp{ExePath: `C:\Program Files (x86)\Epic Games\Launcher\Portal\Binaries\Win64\EpicGamesLauncher.exe`, Fullscreen: true}, ""},
		{"fullscreen uses description", foregroundApp{ExePath: `C:\Games\mc\javaw.exe`, Description: "Minecraft", Title: "Minecraft 1.21", Fullscreen: true}, "Minecraft"},
		{"generic description falls back to title", foregroundApp{ExePath: `C:\X\Game-Win64-Shipping.exe`, Description: "BootstrapPackagedGame", Title: "Hollow: Knight?", Fullscreen: true}, "Hollow Knight"},
		{"no title falls back to exe", foregroundApp{ExePath: `C:\X\Elden-Win64-Shipping.exe`, Fullscreen: true}, "Elden"},
		{"fullscreen browser is not a game", foregroundApp{ExePath: `C:\Program Files\Google\Chrome\Application\chrome.exe`, Title: "YouTube", Fullscreen: true}, ""},
		{"windowed non-library app", foregroundApp{ExePath: `C:\Tools\notepad.exe`, Title: "notes"}, ""},
	}
	for _, c := range cases {
		if got := gameFolderFor(c.app); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSanitizeFolder(t *testing.T) {
	for in, want := range map[string]string{
		`Half-Life 2`:          "Half-Life 2",
		`What: "Now"?  ...`:    "What Now",
		"  tabs\tand\nlines  ": "tabs and lines",
		`CON`:                  "",
		`...`:                  "",
	} {
		if got := sanitizeFolder(in); got != want {
			t.Errorf("sanitizeFolder(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClipLocation(t *testing.T) {
	st := DefaultSettings()
	st.SaveDir = `D:\Clips`
	if dir, prefix := clipLocation(st, "VALORANT"); dir != filepath.Join(`D:\Clips`, "VALORANT") || prefix != "VALORANT" {
		t.Errorf("game: %s %s", dir, prefix)
	}
	if dir, prefix := clipLocation(st, ""); dir != filepath.Join(`D:\Clips`, DesktopFolder) || prefix != "Replay" {
		t.Errorf("desktop: %s %s", dir, prefix)
	}
	st.GameFolders = false
	if dir, prefix := clipLocation(st, "VALORANT"); dir != `D:\Clips` || prefix != "VALORANT" {
		t.Errorf("folders off: %s %s", dir, prefix)
	}
}
