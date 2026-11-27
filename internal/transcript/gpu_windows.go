//go:build windows

package transcript

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// probeGPU asks the NVIDIA driver's own nvidia-smi (installed in System32
// with every NVIDIA driver) for the first GPU. No NVIDIA driver, no GPU.
// It runs as a separate process so Toru never loads the driver itself.
func probeGPU() GPU {
	smi := filepath.Join(os.Getenv("SystemRoot"), "System32", "nvidia-smi.exe")
	if !fileExists(smi) {
		return GPU{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, smi, "--query-gpu=name,driver_version,compute_cap", "--format=csv,noheader")
	configureProc(cmd)
	out, err := cmd.Output()
	if err != nil {
		logf("nvidia-smi: %v", err)
		return GPU{}
	}
	return parseNvidiaSMI(string(out))
}
