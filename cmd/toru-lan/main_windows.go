package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"

	"github.com/StephenSHorton/toru/internal/lan/adapter"
	"github.com/StephenSHorton/toru/internal/lan/api"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
)

func run(args []string) int {
	// Before anything can load a DLL: System32 and our own folder only.
	if err := adapter.HardenDLLSearch(); err != nil {
		fmt.Fprintln(os.Stderr, "toru-lan: restrict DLL search:", err)
		return 1
	}
	if is, err := svc.IsWindowsService(); err == nil && is {
		if err := runService(false); err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 1 {
		usage()
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		fmt.Println(version)
		return 0
	case "install":
		err = install()
	case "uninstall":
		err = uninstall()
	case "debug":
		err = runService(true)
	default:
		usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "toru-lan:", err)
		return 1
	}
	return 0
}

// Machine-wide locations (docs/lan.md, "Where things will live").
type paths struct {
	installDir string // %ProgramFiles%\Toru\lan
	dataDir    string // %ProgramData%\Toru\lan
}

func (p paths) exe() string     { return filepath.Join(p.installDir, api.LanExe) }
func (p paths) dll() string     { return filepath.Join(p.installDir, api.WintunDLL) }
func (p paths) keysDir() string { return filepath.Join(p.dataDir, "keys") }
func (p paths) logFile() string { return filepath.Join(p.dataDir, "service.log") }

func machinePaths() (paths, error) {
	pf, err := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if err != nil {
		return paths{}, fmt.Errorf("find the Program Files folder: %w", err)
	}
	pd, err := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if err != nil {
		return paths{}, fmt.Errorf("ProgramData: %w", err)
	}
	return paths{installDir: filepath.Join(pf, api.InstallSubdir), dataDir: filepath.Join(pd, api.DataSubdir)}, nil
}

func selfDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

func encodeKey(k [32]byte) string { return base64.StdEncoding.EncodeToString(k[:]) }

func elevated() bool { return windows.GetCurrentProcessToken().IsElevated() }
