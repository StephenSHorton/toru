package capture

import "sort"

// audio_appmix.go holds the platform-free half of the per-app stream audio
// mix (audio_appmix_windows.go): which process trees to capture.

// appMixRoots picks the PIDs to give one INCLUDE-tree process-loopback client
// each, from the PIDs owning audio sessions. parent maps pid -> parent pid
// (a process snapshot); banned are tree roots whose audio must never be
// captured (Toru, audio mixers). A session pid is dropped when:
//   - it is banned or inside a banned tree (Toru's WebView2 call audio, a
//     mixer's mic feed);
//   - its OWN tree contains a banned pid (explorer.exe, or a launcher that
//     started Toru or Sonar: including it would include them too);
//   - an ancestor is already picked (its tree covers this one; capturing both
//     would play this app twice).
func appMixRoots(sessions []uint32, parent map[uint32]uint32, banned []uint32) []uint32 {
	isBanned := map[uint32]bool{}
	for _, b := range banned {
		if b != 0 {
			isBanned[b] = true
		}
	}
	ancestors := func(pid uint32) []uint32 {
		var out []uint32
		seen := map[uint32]bool{pid: true}
		for p := parent[pid]; p != 0 && !seen[p] && len(out) < 64; p = parent[p] {
			seen[p] = true
			out = append(out, p)
		}
		return out
	}
	// Every ancestor of a banned pid: its tree contains that banned pid.
	holdsBanned := map[uint32]bool{}
	for b := range isBanned {
		for _, a := range ancestors(b) {
			holdsBanned[a] = true
		}
	}
	keep := map[uint32]bool{}
	for _, pid := range sessions {
		if pid == 0 || isBanned[pid] || holdsBanned[pid] {
			continue
		}
		inside := false
		for _, a := range ancestors(pid) {
			if isBanned[a] {
				inside = true
				break
			}
		}
		if !inside {
			keep[pid] = true
		}
	}
	var out []uint32
	for pid := range keep {
		covered := false
		for _, a := range ancestors(pid) {
			if keep[a] {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, pid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
