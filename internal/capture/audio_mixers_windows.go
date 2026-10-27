//go:build windows

package capture

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// audio_mixers_windows.go knows about user-mode audio mixers: apps that own
// virtual audio devices and play audio from their OWN process.
//
// Measured on a PC running SteelSeries Sonar (TestStreamAudioLeakProbe: a
// quiet tone from a child ffplay, default device Sonar - Gaming; four runs):
//   - the exclude-Toru capture starts fine there, so the old whole-mix
//     fallback was not what leaked. (That whole mix is the default endpoint,
//     a Sonar virtual device; its loopback never carried the tone and was
//     sometimes digital silence.)
//   - Sonar's re-render of the mix onto the real headset (the tone at
//     -42 dBFS on the Arctis endpoint) is invisible to process loopback:
//     include-Sonar had no tone (-121..-129 dBFS) and exclude-tone none
//     either, so call audio does NOT come back in through Sonar;
//   - Sonar's process DOES play one stream: the processed microphone, into
//     its "SteelSeries Sonar - Microphone" render endpoint (include-Sonar
//     total matched that endpoint's total in every run: -73.8/-73.8,
//     -80.7/-80.7, about -92/-93 in a quiet room). Process loopback hears
//     every process on every endpoint, so the exclude-Toru capture carried
//     the streamer's own voice: viewers heard them from the call and again
//     from the stream;
//   - include-tree captures are at the source and endpoint-independent: the
//     tone measured -53 dBFS via include-tone and via exclude-Sonar alike,
//     and the per-app mix carried an app's tone at the same -53 dBFS with a
//     banned tree's tone absent (TestStreamAudioAppMixProbe).
//
// Exclusion takes ONE process tree, so "everything except Toru and the
// mixer" is built from per-app include captures (audio_appmix_windows.go).

// mixerExes are those mixers, by executable. Only SteelSeries Sonar is
// measured; the rest are built the same way (virtual devices fed back out by
// their own process) but untested. Effects that live inside the Windows
// audio engine as APOs (Nahimic, Dolby Atmos, THX Spatial) are left out:
// they process each app's stream in place and play nothing of their own.
var mixerExes = map[string]string{
	"steelseriessonar.exe":              "SteelSeries Sonar",
	"voicemeeter.exe":                   "Voicemeeter",
	"voicemeeter_x64.exe":               "Voicemeeter",
	"voicemeeterpro.exe":                "Voicemeeter Banana",
	"voicemeeterpro_x64.exe":            "Voicemeeter Banana",
	"voicemeeter8.exe":                  "Voicemeeter Potato",
	"voicemeeter8x64.exe":               "Voicemeeter Potato",
	"wavelink.exe":                      "Elgato Wave Link",
	"wavelinkse.exe":                    "Elgato Wave Link",
	"nvidia broadcast.exe":              "NVIDIA Broadcast",
	"nvidia broadcast ui.exe":           "NVIDIA Broadcast",
	"rzsurroundvadstreamingservice.exe": "Razer Surround",
}

// audioMixer is one running mixer root process.
type audioMixer struct {
	name string
	pid  uint32
}

// processSnapshot returns every running process's parent pid and lowercase
// executable name.
func processSnapshot() (parent map[uint32]uint32, exe map[uint32]string) {
	parent, exe = map[uint32]uint32{}, map[uint32]string{}
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return parent, exe
	}
	defer func() { _ = windows.CloseHandle(snap) }()
	var pe windows.ProcessEntry32
	pe.Size = uint32(unsafe.Sizeof(pe))
	for err = windows.Process32First(snap, &pe); err == nil; err = windows.Process32Next(snap, &pe) {
		parent[pe.ProcessID] = pe.ParentProcessID
		exe[pe.ProcessID] = strings.ToLower(windows.UTF16ToString(pe.ExeFile[:]))
	}
	return parent, exe
}

// mixerRoots picks the root process of each running known mixer (the one
// whose parent is not the same mixer), so a process-loopback capture of that
// pid covers the mixer's whole tree.
func mixerRoots(parent map[uint32]uint32, exe map[uint32]string) []audioMixer {
	var out []audioMixer
	for pid, name := range exe {
		label, ok := mixerExes[name]
		if !ok {
			continue
		}
		if p := parent[pid]; p != 0 && mixerExes[exe[p]] == label {
			continue
		}
		out = append(out, audioMixer{label, pid})
	}
	return out
}

// runningAudioMixers is mixerRoots on a fresh process snapshot.
func runningAudioMixers() []audioMixer { return mixerRoots(processSnapshot()) }
