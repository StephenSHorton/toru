//go:build !windows

package transcript

// probeGPU: the engine only runs on Windows.
func probeGPU() GPU { return GPU{} }
