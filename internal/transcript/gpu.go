package transcript

import (
	"strconv"
	"strings"
)

// gpu.go finds out whether this PC has a GPU whisper can run on. Only
// NVIDIA's CUDA build of whisper.cpp is published for Windows, so that is
// what counts: an NVIDIA GPU of compute capability 5.0 or newer (what the
// build is compiled for) with a driver new enough for CUDA 12.4.

// GPU is the graphics card the GPU build would run on. Name is "" when
// this PC has none it can use.
type GPU struct {
	Name   string // "NVIDIA GeForce RTX 5080"
	Driver string // "621.40"
}

// minDriver is the oldest Windows NVIDIA driver that runs CUDA 12.4
// programs (NVIDIA's CUDA 12.4 release notes: 551.61).
var minDriver = [2]int{551, 61}

// parseNvidiaSMI reads the first line of
//
//	nvidia-smi --query-gpu=name,driver_version,compute_cap --format=csv,noheader
//
// ("NVIDIA GeForce RTX 5080, 621.40, 12.0") and returns that GPU if the
// GPU build can use it.
func parseNvidiaSMI(out string) GPU {
	line, _, _ := strings.Cut(strings.TrimSpace(out), "\n")
	f := strings.Split(line, ",")
	if len(f) != 3 {
		return GPU{}
	}
	name, driver, capa := strings.TrimSpace(f[0]), strings.TrimSpace(f[1]), strings.TrimSpace(f[2])
	if name == "" || !atLeast(driver, minDriver) || !atLeast(capa, [2]int{5, 0}) {
		return GPU{}
	}
	return GPU{Name: name, Driver: driver}
}

// atLeast reports whether a "major.minor" version is at least want.
func atLeast(v string, want [2]int) bool {
	a, b, _ := strings.Cut(v, ".")
	major, err := strconv.Atoi(a)
	if err != nil {
		return false
	}
	minor := 0
	if b != "" {
		if minor, err = strconv.Atoi(b); err != nil {
			return false
		}
	}
	return major > want[0] || major == want[0] && minor >= want[1]
}
