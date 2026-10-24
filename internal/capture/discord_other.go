//go:build !windows

package capture

// DiscordPID is Windows-only; see discord_windows.go.
func DiscordPID() uint32 { return 0 }
