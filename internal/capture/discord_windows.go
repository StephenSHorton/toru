//go:build windows

package capture

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// discordExes are the Discord client builds whose audio the replay buffer can
// split out (stable, PTB, Canary).
var discordExes = map[string]bool{
	"discord.exe":       true,
	"discordptb.exe":    true,
	"discordcanary.exe": true,
}

// DiscordPID returns the ROOT Discord process (the one whose parent is not
// itself a Discord process), or 0 when Discord isn't running. Voice audio is
// played by a child process, so the process-loopback client must target the
// root to cover the whole tree.
func DiscordPID() uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(snap) }()

	parent := map[uint32]uint32{}
	var discord []uint32
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		if discordExes[strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))] {
			discord = append(discord, pe.ProcessID)
			parent[pe.ProcessID] = pe.ParentProcessID
		}
	}
	for _, pid := range discord {
		if _, parentIsDiscord := parent[parent[pid]]; !parentIsDiscord {
			return pid
		}
	}
	return 0
}
